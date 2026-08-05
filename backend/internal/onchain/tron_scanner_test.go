package onchain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeTRONBlockSource struct {
	head             int64
	blocks           map[int64]TRONSolidifiedBlock
	receipts         map[int64][]TRONTransactionReceipt
	requestedBlocks  []int64
	requestedReceipt []int64
}

func (s *fakeTRONBlockSource) LatestSolidifiedHeight(context.Context) (int64, error) {
	return s.head, nil
}

func (s *fakeTRONBlockSource) SolidifiedBlockByHeight(_ context.Context, height int64) (TRONSolidifiedBlock, error) {
	s.requestedBlocks = append(s.requestedBlocks, height)
	block, exists := s.blocks[height]
	if !exists {
		return TRONSolidifiedBlock{}, errors.New("missing block fixture")
	}
	return block, nil
}

func (s *fakeTRONBlockSource) SolidifiedTransactionReceiptsByBlockHeight(_ context.Context, height int64) ([]TRONTransactionReceipt, error) {
	s.requestedReceipt = append(s.requestedReceipt, height)
	return s.receipts[height], nil
}

type fakeTRONScanStore struct {
	cursor              TRONScanCursor
	intents             map[string]TRONPaymentIntentReference
	commits             []TRONScanBlockCommit
	reconciles          []TRONScanBlockReconcile
	hashConflicts       []error
	commitResponseError error
}

func (s *fakeTRONScanStore) InitializeTRONScanCursor(_ context.Context, input TRONScanCursorInitialization) error {
	s.cursor = TRONScanCursor{
		FinalizedHeight: input.FinalizedHeight,
		FinalizedHash:   input.FinalizedHash,
		Health:          CursorHealthy,
	}
	return nil
}

func (s *fakeTRONScanStore) ReconcileTRONScanBlock(_ context.Context, input TRONScanBlockReconcile) error {
	s.reconciles = append(s.reconciles, input)
	return nil
}

func (s *fakeTRONScanStore) MarkTRONScanHashConflict(_ context.Context, _ Network, _ string, _ time.Time, cause error) error {
	s.hashConflicts = append(s.hashConflicts, cause)
	s.cursor.Health = CursorHashConflict
	return nil
}

func (s *fakeTRONScanStore) LoadTRONScanCursor(context.Context, Network) (TRONScanCursor, error) {
	return s.cursor, nil
}

func (s *fakeTRONScanStore) FindTRONPaymentIntentByAddress(_ context.Context, _ Network, address string) (TRONPaymentIntentReference, bool, error) {
	intent, exists := s.intents[address]
	return intent, exists, nil
}

func (s *fakeTRONScanStore) CommitTRONScanBlock(_ context.Context, input TRONScanBlockCommit) error {
	s.commits = append(s.commits, input)
	s.cursor.FinalizedHeight = input.Block.Height
	s.cursor.FinalizedHash = input.Block.Hash
	if s.commitResponseError != nil {
		err := s.commitResponseError
		s.commitResponseError = nil
		return err
	}
	return nil
}

func TestTRONScannerScansSequentialBoundedBatch(t *testing.T) {
	now := time.Now().UTC()
	source := &fakeTRONBlockSource{
		head: 13,
		blocks: map[int64]TRONSolidifiedBlock{
			10: tronScannerEmptyBlock(10),
			11: tronScannerEmptyBlock(11),
			12: tronScannerEmptyBlock(12),
		},
		receipts: map[int64][]TRONTransactionReceipt{10: {}, 11: {}, 12: {}},
	}
	store := &fakeTRONScanStore{cursor: TRONScanCursor{
		FinalizedHeight: 10,
		FinalizedHash:   tronScannerHash(10),
		LeaseOwner:      "scanner-a",
		LeaseUntil:      now.Add(time.Minute),
	}}
	scanner, err := NewTRONScanner(source, store, TRONScannerOptions{
		Network: NetworkTronMainnet, ContractAddress: TronMainnetUSDTContract, BatchSize: 2, SafetyWindow: 1,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	result, err := scanner.ScanBatch(context.Background(), "scanner-a")
	require.NoError(t, err)
	require.Equal(t, TRONScanBatchResult{
		StartHeight: 11, EndHeight: 12, SolidifiedHead: 13, BlocksScanned: 2,
	}, result)
	require.Equal(t, []int64{10, 11, 12}, source.requestedBlocks)
	require.Equal(t, []int64{10, 11, 12}, source.requestedReceipt)
	require.Len(t, store.reconciles, 1)
	require.Len(t, store.commits, 2)
	require.Equal(t, int64(10), store.commits[0].PreviousHeight)
	require.Equal(t, int64(11), store.commits[1].PreviousHeight)
}

func TestTRONScannerRecoversAfterCommitResponseLoss(t *testing.T) {
	now := time.Now().UTC()
	source := &fakeTRONBlockSource{
		head:     1,
		blocks:   map[int64]TRONSolidifiedBlock{1: tronScannerEmptyBlock(1)},
		receipts: map[int64][]TRONTransactionReceipt{1: {}},
	}
	store := &fakeTRONScanStore{
		cursor: TRONScanCursor{
			LeaseOwner: "scanner-a",
			LeaseUntil: now.Add(time.Minute),
			Health:     CursorHealthy,
		},
		commitResponseError: errors.New("database commit response lost"),
	}
	scanner, err := NewTRONScanner(source, store, TRONScannerOptions{
		Network: NetworkTronMainnet, ContractAddress: TronMainnetUSDTContract, BatchSize: 1, SafetyWindow: 1,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	_, err = scanner.ScanBatch(context.Background(), "scanner-a")
	require.ErrorContains(t, err, "database commit response lost")
	require.Equal(t, int64(1), store.cursor.FinalizedHeight, "the database commit completed before its response was lost")
	require.Len(t, store.commits, 1)

	result, err := scanner.ScanBatch(context.Background(), "scanner-a")
	require.NoError(t, err)
	require.Equal(t, int64(2), result.StartHeight)
	require.Equal(t, int64(1), result.EndHeight)
	require.Len(t, store.commits, 1, "retry must not submit the durable block twice")
	require.Len(t, store.reconciles, 1, "retry verifies the durable block through the safety window")
}

func TestTRONScannerFindsRegisteredTransfer(t *testing.T) {
	receipt, options, _ := validTRC20TransferFixture(t)
	block := TRONSolidifiedBlock{
		Height:         receipt.BlockHeight,
		Hash:           tronScannerHash(receipt.BlockHeight),
		ParentHash:     tronScannerHash(receipt.BlockHeight - 1),
		Timestamp:      receipt.BlockTimestamp,
		TransactionIDs: []string{receipt.TransactionID},
	}
	now := receipt.BlockTimestamp.Add(time.Minute)
	source := &fakeTRONBlockSource{
		head: receipt.BlockHeight,
		blocks: map[int64]TRONSolidifiedBlock{
			receipt.BlockHeight - 1: tronScannerEmptyBlock(receipt.BlockHeight - 1),
			receipt.BlockHeight:     block,
		},
		receipts: map[int64][]TRONTransactionReceipt{
			receipt.BlockHeight - 1: {},
			receipt.BlockHeight:     {receipt},
		},
	}
	intent := TRONPaymentIntentReference{
		ID: 1, PaymentOrderID: 2, UserID: 3, Network: NetworkTronMainnet,
		TokenContract: options.ContractAddress, DepositAddress: options.RecipientAddress,
	}
	store := &fakeTRONScanStore{
		cursor: TRONScanCursor{
			FinalizedHeight: receipt.BlockHeight - 1,
			FinalizedHash:   block.ParentHash,
			LeaseOwner:      "scanner-a",
			LeaseUntil:      now.Add(time.Minute),
		},
		intents: map[string]TRONPaymentIntentReference{options.RecipientAddress: intent},
	}
	scanner, err := NewTRONScanner(source, store, TRONScannerOptions{
		Network: NetworkTronMainnet, ContractAddress: options.ContractAddress, BatchSize: 10, SafetyWindow: 1,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	result, err := scanner.ScanBatch(context.Background(), "scanner-a")
	require.NoError(t, err)
	require.Equal(t, 1, result.DepositsFound)
	require.Len(t, store.commits, 1)
	require.Len(t, store.commits[0].Deposits, 1)
	require.Equal(t, "12500000", store.commits[0].Deposits[0].Transfer.AmountRaw)
}

func TestTRONScannerRejectsLostLeaseAndMismatchedReceiptSet(t *testing.T) {
	now := time.Now().UTC()
	source := &fakeTRONBlockSource{head: 1, blocks: map[int64]TRONSolidifiedBlock{1: tronScannerEmptyBlock(1)}}
	store := &fakeTRONScanStore{cursor: TRONScanCursor{
		LeaseOwner: "scanner-a", LeaseUntil: now.Add(-time.Second),
	}}
	scanner, err := NewTRONScanner(source, store, TRONScannerOptions{
		Network: NetworkTronMainnet, ContractAddress: TronMainnetUSDTContract, BatchSize: 1, SafetyWindow: 1,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	_, err = scanner.ScanBatch(context.Background(), "scanner-a")
	require.ErrorIs(t, err, ErrTRONScanLeaseLost)
	require.Empty(t, source.requestedBlocks)

	store.cursor.LeaseUntil = now.Add(time.Minute)
	source.blocks[1] = TRONSolidifiedBlock{
		Height: 1, Hash: tronScannerHash(1), ParentHash: tronScannerHash(0), Timestamp: now,
		TransactionIDs: []string{tronScannerHash(100)},
	}
	source.receipts = map[int64][]TRONTransactionReceipt{1: {}}
	_, err = scanner.ScanBatch(context.Background(), "scanner-a")
	require.Error(t, err)
	require.Empty(t, store.commits)
}

func TestTRONScannerStopsAndPersistsSolidifiedHashConflict(t *testing.T) {
	now := time.Now().UTC()
	source := &fakeTRONBlockSource{
		head: 10,
		blocks: map[int64]TRONSolidifiedBlock{
			10: tronScannerEmptyBlock(10),
		},
		receipts: map[int64][]TRONTransactionReceipt{10: {}},
	}
	store := &fakeTRONScanStore{cursor: TRONScanCursor{
		FinalizedHeight: 10,
		FinalizedHash:   strings.Repeat("f", 64),
		LeaseOwner:      "scanner-a",
		LeaseUntil:      now.Add(time.Minute),
		Health:          CursorHealthy,
	}}
	scanner, err := NewTRONScanner(source, store, TRONScannerOptions{
		Network: NetworkTronMainnet, ContractAddress: TronMainnetUSDTContract, BatchSize: 1, SafetyWindow: 1,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	_, err = scanner.ScanBatch(context.Background(), "scanner-a")
	require.ErrorIs(t, err, ErrTRONScanHashConflict)
	require.Len(t, store.hashConflicts, 1)
	require.Equal(t, CursorHashConflict, store.cursor.Health)
	require.Empty(t, store.commits)
}

func tronScannerEmptyBlock(height int64) TRONSolidifiedBlock {
	return TRONSolidifiedBlock{
		Height: height, Hash: tronScannerHash(height), ParentHash: tronScannerHash(height - 1),
		Timestamp: time.Unix(1700000000+height, 0).UTC(), TransactionIDs: []string{},
	}
}

func tronScannerHash(value int64) string {
	return fmt.Sprintf("%064x", value)
}
