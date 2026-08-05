package onchain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
)

var (
	ErrTRONScanLeaseLost      = errors.New("TRON scan lease lost")
	ErrTRONScanCursorConflict = errors.New("TRON scan cursor conflict")
	ErrTRONScanCursorNotFound = errors.New("TRON scan cursor not found")
	ErrTRONScanHashConflict   = errors.New("TRON solidified block hash conflict")
)

type TRONBlockSource interface {
	LatestSolidifiedHeight(ctx context.Context) (int64, error)
	SolidifiedBlockByHeight(ctx context.Context, height int64) (TRONSolidifiedBlock, error)
	SolidifiedTransactionReceiptsByBlockHeight(ctx context.Context, height int64) ([]TRONTransactionReceipt, error)
}

type TRONScanCursor struct {
	FinalizedHeight int64
	FinalizedHash   string
	LeaseOwner      string
	LeaseUntil      time.Time
	Health          CursorHealth
}

type TRONPaymentIntentReference struct {
	ID             int64
	PaymentOrderID int64
	UserID         int64
	Network        Network
	ChainID        int64
	TokenContract  string
	DepositAddress string
}

type TRONScanDeposit struct {
	Intent           TRONPaymentIntentReference
	Transfer         TRC20Transfer
	TransactionIndex int
}

type TRONScanBlockCommit struct {
	Network        Network
	LeaseOwner     string
	Now            time.Time
	PreviousHeight int64
	Block          TRONSolidifiedBlock
	Deposits       []TRONScanDeposit
}

type TRONScanBlockReconcile struct {
	Network    Network
	LeaseOwner string
	Now        time.Time
	Block      TRONSolidifiedBlock
	Deposits   []TRONScanDeposit
}

type TRONScanCursorInitialization struct {
	Network         Network
	ChainID         int64
	FinalizedHeight int64
	FinalizedHash   string
	InitializedAt   time.Time
}

type TRONScanStore interface {
	LoadTRONScanCursor(ctx context.Context, network Network) (TRONScanCursor, error)
	InitializeTRONScanCursor(ctx context.Context, input TRONScanCursorInitialization) error
	FindTRONPaymentIntentByAddress(ctx context.Context, network Network, address string) (TRONPaymentIntentReference, bool, error)
	CommitTRONScanBlock(ctx context.Context, input TRONScanBlockCommit) error
	ReconcileTRONScanBlock(ctx context.Context, input TRONScanBlockReconcile) error
	MarkTRONScanHashConflict(ctx context.Context, network Network, leaseOwner string, now time.Time, cause error) error
}

type TRONScannerOptions struct {
	Network         Network
	ContractAddress string
	BatchSize       int
	SafetyWindow    int
}

type TRONScanner struct {
	source          TRONBlockSource
	store           TRONScanStore
	network         Network
	contractAddress string
	batchSize       int
	safetyWindow    int
	now             func() time.Time
}

type TRONScanBatchResult struct {
	StartHeight    int64
	EndHeight      int64
	SolidifiedHead int64
	BlocksScanned  int
	DepositsFound  int
}

func NewTRONScanner(source TRONBlockSource, store TRONScanStore, options TRONScannerOptions) (*TRONScanner, error) {
	if source == nil || store == nil {
		return nil, fmt.Errorf("TRON scanner source and store are required")
	}
	if options.Network != NetworkTronMainnet && options.Network != NetworkTronNile {
		return nil, fmt.Errorf("TRON scanner does not support network %q", options.Network)
	}
	if err := ValidateAddress(options.Network, options.ContractAddress); err != nil {
		return nil, fmt.Errorf("invalid TRON scanner contract: %w", err)
	}
	if options.BatchSize < 1 || options.BatchSize > 1000 {
		return nil, fmt.Errorf("TRON scanner batch size must be between 1 and 1000")
	}
	if options.SafetyWindow < 1 || options.SafetyWindow > 1000 {
		return nil, fmt.Errorf("TRON scanner safety window must be between 1 and 1000")
	}
	return &TRONScanner{
		source:          source,
		store:           store,
		network:         options.Network,
		contractAddress: strings.TrimSpace(options.ContractAddress),
		batchSize:       options.BatchSize,
		safetyWindow:    options.SafetyWindow,
		now:             func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *TRONScanner) ScanBatch(ctx context.Context, leaseOwner string) (TRONScanBatchResult, error) {
	result := TRONScanBatchResult{}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return result, fmt.Errorf("TRON scanner lease owner is required")
	}
	now := s.now()
	cursor, err := s.store.LoadTRONScanCursor(ctx, s.network)
	if err != nil {
		return result, fmt.Errorf("load TRON scan cursor: %w", err)
	}
	if cursor.LeaseOwner != leaseOwner || !cursor.LeaseUntil.After(now) {
		return result, ErrTRONScanLeaseLost
	}
	if cursor.Health == CursorHashConflict {
		return result, ErrTRONScanHashConflict
	}
	if cursor.FinalizedHeight < 0 {
		return result, fmt.Errorf("%w: negative saved height", ErrTRONScanCursorConflict)
	}
	if err := s.reconcileSafetyWindow(ctx, cursor, leaseOwner); err != nil {
		return result, err
	}
	head, err := s.source.LatestSolidifiedHeight(ctx)
	if err != nil {
		return result, fmt.Errorf("read latest TRON solidified height: %w", err)
	}
	result.SolidifiedHead = head
	result.StartHeight = cursor.FinalizedHeight + 1
	result.EndHeight = cursor.FinalizedHeight
	if head < cursor.FinalizedHeight {
		return result, fmt.Errorf("%w: saved height %d is ahead of solidified head %d", ErrTRONScanCursorConflict, cursor.FinalizedHeight, head)
	}
	endHeight := min(head, cursor.FinalizedHeight+int64(s.batchSize))
	for height := result.StartHeight; height <= endHeight; height++ {
		block, deposits, err := s.loadBlockDeposits(ctx, height)
		if err != nil {
			return result, fmt.Errorf("read and validate TRON solidified block %d: %w", height, err)
		}
		if err := s.store.CommitTRONScanBlock(ctx, TRONScanBlockCommit{
			Network:        s.network,
			LeaseOwner:     leaseOwner,
			Now:            s.now(),
			PreviousHeight: height - 1,
			Block:          block,
			Deposits:       deposits,
		}); err != nil {
			return result, fmt.Errorf("commit TRON solidified block %d: %w", height, err)
		}
		result.EndHeight = height
		result.BlocksScanned++
		result.DepositsFound += len(deposits)
	}
	return result, nil
}

func (s *TRONScanner) reconcileSafetyWindow(ctx context.Context, cursor TRONScanCursor, leaseOwner string) error {
	if cursor.FinalizedHeight == 0 {
		return nil
	}
	if cursor.FinalizedHash == "" {
		return s.failHashConflict(ctx, leaseOwner, fmt.Errorf("saved cursor at height %d has no block hash", cursor.FinalizedHeight))
	}
	startHeight := max(int64(1), cursor.FinalizedHeight-int64(s.safetyWindow)+1)
	for height := startHeight; height <= cursor.FinalizedHeight; height++ {
		block, deposits, err := s.loadBlockDeposits(ctx, height)
		if err != nil {
			return fmt.Errorf("rescan TRON safety window block %d: %w", height, err)
		}
		if height == cursor.FinalizedHeight && !strings.EqualFold(block.Hash, cursor.FinalizedHash) {
			return s.failHashConflict(ctx, leaseOwner, fmt.Errorf(
				"saved block hash %s at height %d changed to %s",
				cursor.FinalizedHash,
				height,
				block.Hash,
			))
		}
		err = s.store.ReconcileTRONScanBlock(ctx, TRONScanBlockReconcile{
			Network: s.network, LeaseOwner: leaseOwner, Now: s.now(), Block: block, Deposits: deposits,
		})
		if err != nil {
			if errors.Is(err, ErrTRONScanHashConflict) || errors.Is(err, ErrTRONScanCursorConflict) {
				return s.failHashConflict(ctx, leaseOwner, err)
			}
			return fmt.Errorf("reconcile TRON safety window block %d: %w", height, err)
		}
	}
	return nil
}

func (s *TRONScanner) failHashConflict(ctx context.Context, leaseOwner string, cause error) error {
	now := s.now()
	if err := s.store.MarkTRONScanHashConflict(ctx, s.network, leaseOwner, now, cause); err != nil {
		return fmt.Errorf("%w: %v (persist conflict state: %v)", ErrTRONScanHashConflict, cause, err)
	}
	return fmt.Errorf("%w: %v", ErrTRONScanHashConflict, cause)
}

func (s *TRONScanner) loadBlockDeposits(ctx context.Context, height int64) (TRONSolidifiedBlock, []TRONScanDeposit, error) {
	block, err := s.source.SolidifiedBlockByHeight(ctx, height)
	if err != nil {
		return TRONSolidifiedBlock{}, nil, err
	}
	receipts, err := s.source.SolidifiedTransactionReceiptsByBlockHeight(ctx, height)
	if err != nil {
		return TRONSolidifiedBlock{}, nil, err
	}
	deposits, err := s.scanBlockDeposits(ctx, block, receipts)
	if err != nil {
		return TRONSolidifiedBlock{}, nil, err
	}
	return block, deposits, nil
}

func (s *TRONScanner) scanBlockDeposits(ctx context.Context, block TRONSolidifiedBlock, receipts []TRONTransactionReceipt) ([]TRONScanDeposit, error) {
	transactionIndexes := make(map[string]int, len(block.TransactionIDs))
	for index, transactionID := range block.TransactionIDs {
		transactionIndexes[transactionID] = index
	}
	if len(receipts) != len(transactionIndexes) {
		return nil, fmt.Errorf("block contains %d transactions but node returned %d transaction infos", len(transactionIndexes), len(receipts))
	}
	deposits := make([]TRONScanDeposit, 0)
	for _, receipt := range receipts {
		transactionIndex, exists := transactionIndexes[receipt.TransactionID]
		if !exists {
			return nil, fmt.Errorf("transaction info %s is not present in the block", receipt.TransactionID)
		}
		if receipt.BlockHeight != block.Height || !receipt.BlockTimestamp.Equal(block.Timestamp) {
			return nil, fmt.Errorf("transaction info %s block identity does not match its block", receipt.TransactionID)
		}
		for logIndex, logEntry := range receipt.Logs {
			recipientAddress, candidate := trc20CandidateRecipient(logEntry)
			if !candidate {
				continue
			}
			intent, found, err := s.store.FindTRONPaymentIntentByAddress(ctx, s.network, recipientAddress)
			if err != nil {
				return nil, fmt.Errorf("find payment intent for %s: %w", recipientAddress, err)
			}
			if !found {
				continue
			}
			transfer, err := ParseTRC20Transfer(receipt, logIndex, TRC20TransferParseOptions{
				Network:          s.network,
				ContractAddress:  s.contractAddress,
				RecipientAddress: intent.DepositAddress,
			})
			if err != nil {
				continue
			}
			deposits = append(deposits, TRONScanDeposit{
				Intent:           intent,
				Transfer:         transfer,
				TransactionIndex: transactionIndex,
			})
		}
	}
	return deposits, nil
}

func trc20CandidateRecipient(logEntry TRONTransactionLog) (string, bool) {
	if len(logEntry.Topics) != 3 {
		return "", false
	}
	topic0, err := normalizeFixedTRONHex("topic0", logEntry.Topics[0], 32)
	if err != nil || topic0 != TRC20TransferTopic {
		return "", false
	}
	payload, err := decodeTRONAddressTopic("to", logEntry.Topics[2])
	if err != nil {
		return "", false
	}
	return base58.CheckEncode(payload, 0x41), true
}
