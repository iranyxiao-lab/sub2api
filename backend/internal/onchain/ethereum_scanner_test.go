package onchain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

type fakeEthereumScanSource struct {
	finalized    EthereumBlockRef
	finalizedErr error
	blocks       map[uint64]EthereumBlockRef
	logs         []types.Log
	receipts     map[string]EthereumTransactionReceipt
	ranges       [][2]uint64
}

func (s *fakeEthereumScanSource) FinalizedBlock(context.Context) (EthereumBlockRef, error) {
	return s.finalized, s.finalizedErr
}

func (s *fakeEthereumScanSource) BlockByNumber(_ context.Context, number uint64) (EthereumBlockRef, error) {
	block, ok := s.blocks[number]
	if !ok {
		return EthereumBlockRef{}, fmt.Errorf("missing block %d", number)
	}
	return block, nil
}

func (s *fakeEthereumScanSource) Logs(_ context.Context, fromBlock, toBlock uint64, _ string, _ [][]common.Hash) ([]types.Log, error) {
	s.ranges = append(s.ranges, [2]uint64{fromBlock, toBlock})
	if toBlock-fromBlock+1 > 2 {
		return nil, &EthereumRPCError{Kind: EthereumRPCErrorRemote, Method: "eth_getLogs", Retryable: true, Cause: fmt.Errorf("range too wide")}
	}
	result := make([]types.Log, 0)
	for _, logEntry := range s.logs {
		if logEntry.BlockNumber >= fromBlock && logEntry.BlockNumber <= toBlock {
			result = append(result, logEntry)
		}
	}
	return result, nil
}

func (s *fakeEthereumScanSource) TransactionReceipt(_ context.Context, transactionHash string) (EthereumTransactionReceipt, error) {
	receipt, ok := s.receipts[transactionHash]
	if !ok {
		return EthereumTransactionReceipt{}, fmt.Errorf("missing receipt")
	}
	return receipt, nil
}

type fakeEthereumScanStore struct {
	cursor             EthereumScanCursor
	intents            map[string]EthereumPaymentIntentReference
	commits            []EthereumScanBlockCommit
	reconciles         []EthereumScanBlockReconcile
	conflict           error
	commitResponseLoss bool
	lostCommitResponse bool
}

func (s *fakeEthereumScanStore) ReconcileEthereumScanBlock(_ context.Context, input EthereumScanBlockReconcile) error {
	s.reconciles = append(s.reconciles, input)
	return nil
}

func (s *fakeEthereumScanStore) MarkEthereumScanHashConflict(_ context.Context, _ Network, _ string, _ time.Time, cause error) error {
	s.conflict = cause
	s.cursor.Health = CursorHashConflict
	return nil
}

func (s *fakeEthereumScanStore) LoadEthereumScanCursor(context.Context, Network) (EthereumScanCursor, error) {
	return s.cursor, nil
}

func (s *fakeEthereumScanStore) InitializeEthereumScanCursor(_ context.Context, input EthereumScanCursorInitialization) error {
	s.cursor.FinalizedHeight = input.FinalizedHeight
	s.cursor.FinalizedHash = input.FinalizedHash
	s.cursor.Health = CursorHealthy
	return nil
}

func (s *fakeEthereumScanStore) FindEthereumPaymentIntentByAddress(_ context.Context, _ Network, address string) (EthereumPaymentIntentReference, bool, error) {
	intent, ok := s.intents[address]
	return intent, ok, nil
}

func (s *fakeEthereumScanStore) CommitEthereumScanBlock(_ context.Context, input EthereumScanBlockCommit) error {
	if input.PreviousHeight != s.cursor.FinalizedHeight {
		return ErrEthereumScanCursorConflict
	}
	s.commits = append(s.commits, input)
	s.cursor.FinalizedHeight = input.Block.Number
	s.cursor.FinalizedHash = input.Block.Hash
	if s.commitResponseLoss && !s.lostCommitResponse {
		s.lostCommitResponse = true
		return fmt.Errorf("commit response lost")
	}
	return nil
}

func TestEthereumScannerShrinksRangesSortsLogsAndCommitsEachBlock(t *testing.T) {
	now := time.Now().UTC()
	contract := common.HexToAddress(EthereumMainnetUSDTContract)
	recipient := common.HexToAddress("0x0000000000000000000000000000000000000003")
	blocks := map[uint64]EthereumBlockRef{}
	for height := uint64(1); height <= 4; height++ {
		blocks[height] = EthereumBlockRef{
			Number: height, Hash: common.BigToHash(new(big.Int).SetUint64(height)).Hex(),
			Timestamp: now.Add(time.Duration(height) * time.Second),
		}
	}
	first := ethereumScannerLog(contract, recipient, blocks[1], 2, 7, 10_000_000)
	second := ethereumScannerLog(contract, recipient, blocks[1], 1, 8, 20_000_000)
	third := ethereumScannerLog(contract, recipient, blocks[2], 0, 1, 30_000_000)
	source := &fakeEthereumScanSource{
		finalized: blocks[4], blocks: blocks, logs: []types.Log{first, third, second},
		receipts: map[string]EthereumTransactionReceipt{},
	}
	for _, logEntry := range source.logs {
		source.receipts[logEntry.TxHash.Hex()] = EthereumTransactionReceipt{
			TransactionHash: logEntry.TxHash.Hex(), BlockHash: logEntry.BlockHash.Hex(),
			BlockNumber: logEntry.BlockNumber, Status: types.ReceiptStatusSuccessful, Logs: []types.Log{logEntry},
		}
	}
	store := &fakeEthereumScanStore{
		cursor: EthereumScanCursor{LeaseOwner: "scanner-a", LeaseUntil: now.Add(time.Minute), Health: CursorHealthy},
		intents: map[string]EthereumPaymentIntentReference{recipient.Hex(): {
			ID: 1, PaymentOrderID: 2, UserID: 3, Network: NetworkEthereumMainnet, ChainID: 1,
			TokenContract: contract.Hex(), DepositAddress: recipient.Hex(),
		}},
	}
	scanner, err := NewEthereumScanner(source, store, EthereumScannerOptions{
		Network: NetworkEthereumMainnet, ContractAddress: contract.Hex(), BatchSize: 4, MaxLogRange: 4, SafetyWindow: 2,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	result, err := scanner.ScanBatch(context.Background(), "scanner-a")
	require.NoError(t, err)
	require.Equal(t, EthereumScanBatchResult{
		StartHeight: 1, EndHeight: 2, FinalizedHead: 4, BlocksScanned: 2, DepositsFound: 3, LogRangeUsed: 2,
	}, result)
	require.Equal(t, [][2]uint64{{1, 4}, {1, 2}}, source.ranges)
	require.Len(t, store.commits, 2)
	require.Len(t, store.commits[0].Deposits, 2)
	require.Equal(t, uint(1), store.commits[0].Deposits[0].TransactionIndex)
	require.Equal(t, uint(2), store.commits[0].Deposits[1].TransactionIndex)
	require.Equal(t, uint64(2), store.cursor.FinalizedHeight)
}

func TestEthereumScannerRescansSafetyWindowAndPersistsHashConflict(t *testing.T) {
	now := time.Now().UTC()
	blocks := map[uint64]EthereumBlockRef{}
	for height := uint64(1); height <= 4; height++ {
		blocks[height] = EthereumBlockRef{
			Number: height, Hash: common.BigToHash(new(big.Int).SetUint64(height)).Hex(),
			Timestamp: now.Add(time.Duration(height) * time.Second),
		}
	}
	source := &fakeEthereumScanSource{finalized: blocks[4], blocks: blocks, receipts: map[string]EthereumTransactionReceipt{}}
	store := &fakeEthereumScanStore{cursor: EthereumScanCursor{
		FinalizedHeight: 2, FinalizedHash: blocks[2].Hash,
		LeaseOwner: "scanner-a", LeaseUntil: now.Add(time.Minute), Health: CursorHealthy,
	}}
	scanner, err := NewEthereumScanner(source, store, EthereumScannerOptions{
		Network: NetworkEthereumMainnet, ContractAddress: EthereumMainnetUSDTContract,
		BatchSize: 2, MaxLogRange: 2, SafetyWindow: 2,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	result, err := scanner.ScanBatch(context.Background(), "scanner-a")
	require.NoError(t, err)
	require.Equal(t, 2, result.BlocksScanned)
	require.Len(t, store.reconciles, 2)
	require.Equal(t, uint64(1), store.reconciles[0].Block.Number)
	require.Equal(t, uint64(2), store.reconciles[1].Block.Number)

	conflictStore := &fakeEthereumScanStore{cursor: EthereumScanCursor{
		FinalizedHeight: 2, FinalizedHash: common.HexToHash("0x9999").Hex(),
		LeaseOwner: "scanner-b", LeaseUntil: now.Add(time.Minute), Health: CursorHealthy,
	}}
	conflictScanner, err := NewEthereumScanner(source, conflictStore, EthereumScannerOptions{
		Network: NetworkEthereumMainnet, ContractAddress: EthereumMainnetUSDTContract,
		BatchSize: 2, MaxLogRange: 2, SafetyWindow: 2,
	})
	require.NoError(t, err)
	conflictScanner.now = func() time.Time { return now }
	_, err = conflictScanner.ScanBatch(context.Background(), "scanner-b")
	require.ErrorIs(t, err, ErrEthereumScanHashConflict)
	require.Error(t, conflictStore.conflict)
	require.Equal(t, CursorHashConflict, conflictStore.cursor.Health)
	require.Empty(t, conflictStore.commits)
}

func TestEthereumScannerRecoversAfterCommitResponseLoss(t *testing.T) {
	now := time.Now().UTC()
	blocks := map[uint64]EthereumBlockRef{
		1: {Number: 1, Hash: common.HexToHash("0x01").Hex(), Timestamp: now},
		2: {Number: 2, Hash: common.HexToHash("0x02").Hex(), Timestamp: now.Add(12 * time.Second)},
	}
	source := &fakeEthereumScanSource{finalized: blocks[2], blocks: blocks, receipts: map[string]EthereumTransactionReceipt{}}
	store := &fakeEthereumScanStore{
		cursor:             EthereumScanCursor{LeaseOwner: "scanner-a", LeaseUntil: now.Add(time.Minute), Health: CursorHealthy},
		commitResponseLoss: true,
	}
	scanner, err := NewEthereumScanner(source, store, EthereumScannerOptions{
		Network: NetworkEthereumMainnet, ContractAddress: EthereumMainnetUSDTContract,
		BatchSize: 2, MaxLogRange: 2, SafetyWindow: 2,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	_, err = scanner.ScanBatch(context.Background(), "scanner-a")
	require.ErrorContains(t, err, "commit response lost")
	require.Equal(t, uint64(1), store.cursor.FinalizedHeight, "the database commit succeeded despite the lost response")

	result, err := scanner.ScanBatch(context.Background(), "scanner-a")
	require.NoError(t, err)
	require.Equal(t, uint64(2), result.EndHeight)
	require.Len(t, store.reconciles, 1)
	require.Equal(t, uint64(1), store.reconciles[0].Block.Number)
	require.Len(t, store.commits, 2, "recovery advances only the remaining block")
}

func TestEthereumScannerDoesNotAdvanceWithoutFinalizedHead(t *testing.T) {
	now := time.Now().UTC()
	source := &fakeEthereumScanSource{finalizedErr: errors.New("finalized unavailable")}
	store := &fakeEthereumScanStore{cursor: EthereumScanCursor{
		LeaseOwner: "scanner-a", LeaseUntil: now.Add(time.Minute), Health: CursorHealthy,
	}}
	scanner, err := NewEthereumScanner(source, store, EthereumScannerOptions{
		Network: NetworkEthereumMainnet, ContractAddress: EthereumMainnetUSDTContract,
		BatchSize: 2, MaxLogRange: 2, SafetyWindow: 2,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	_, err = scanner.ScanBatch(context.Background(), "scanner-a")
	require.ErrorContains(t, err, "finalized unavailable")
	require.Empty(t, store.commits)
}

func ethereumScannerLog(contract, recipient common.Address, block EthereumBlockRef, txIndex, logIndex uint, amount int64) types.Log {
	transactionHash := common.BigToHash(big.NewInt(int64(block.Number*100 + uint64(logIndex))))
	return types.Log{
		Address: contract,
		Topics: []common.Hash{
			ERC20TransferTopic,
			common.BytesToHash(common.HexToAddress("0x0000000000000000000000000000000000000002").Bytes()),
			common.BytesToHash(recipient.Bytes()),
		},
		Data:        new(big.Int).SetInt64(amount).FillBytes(make([]byte, 32)),
		BlockNumber: block.Number, TxHash: transactionHash, TxIndex: txIndex,
		BlockHash: common.HexToHash(block.Hash), Index: logIndex,
	}
}
