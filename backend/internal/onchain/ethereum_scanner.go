package onchain

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	ErrEthereumScanLeaseLost      = errors.New("Ethereum scan lease lost")
	ErrEthereumScanCursorConflict = errors.New("Ethereum scan cursor conflict")
	ErrEthereumScanCursorNotFound = errors.New("Ethereum scan cursor not found")
	ErrEthereumScanHashConflict   = errors.New("Ethereum finalized block hash conflict")
)

type EthereumScanSource interface {
	FinalizedBlock(ctx context.Context) (EthereumBlockRef, error)
	BlockByNumber(ctx context.Context, number uint64) (EthereumBlockRef, error)
	Logs(ctx context.Context, fromBlock, toBlock uint64, contract string, topics [][]common.Hash) ([]types.Log, error)
	TransactionReceipt(ctx context.Context, transactionHash string) (EthereumTransactionReceipt, error)
}

type EthereumScanCursor struct {
	FinalizedHeight uint64
	FinalizedHash   string
	LeaseOwner      string
	LeaseUntil      time.Time
	Health          CursorHealth
}

type EthereumPaymentIntentReference struct {
	ID             int64
	PaymentOrderID int64
	UserID         int64
	Network        Network
	ChainID        int64
	TokenContract  string
	DepositAddress string
}

type EthereumScanDeposit struct {
	Intent           EthereumPaymentIntentReference
	Transfer         ERC20Transfer
	TransactionIndex uint
}

type EthereumScanBlockCommit struct {
	Network        Network
	LeaseOwner     string
	Now            time.Time
	PreviousHeight uint64
	Block          EthereumBlockRef
	Deposits       []EthereumScanDeposit
}

type EthereumScanBlockReconcile struct {
	Network    Network
	LeaseOwner string
	Now        time.Time
	Block      EthereumBlockRef
	Deposits   []EthereumScanDeposit
}

type EthereumScanCursorInitialization struct {
	Network         Network
	ChainID         int64
	FinalizedHeight uint64
	FinalizedHash   string
	InitializedAt   time.Time
}

type EthereumScanStore interface {
	LoadEthereumScanCursor(ctx context.Context, network Network) (EthereumScanCursor, error)
	InitializeEthereumScanCursor(ctx context.Context, input EthereumScanCursorInitialization) error
	FindEthereumPaymentIntentByAddress(ctx context.Context, network Network, address string) (EthereumPaymentIntentReference, bool, error)
	CommitEthereumScanBlock(ctx context.Context, input EthereumScanBlockCommit) error
	ReconcileEthereumScanBlock(ctx context.Context, input EthereumScanBlockReconcile) error
	MarkEthereumScanHashConflict(ctx context.Context, network Network, leaseOwner string, now time.Time, cause error) error
}

type EthereumScannerOptions struct {
	Network         Network
	ContractAddress string
	BatchSize       uint64
	MaxLogRange     uint64
	SafetyWindow    uint64
}

type EthereumScanner struct {
	source          EthereumScanSource
	store           EthereumScanStore
	network         Network
	contractAddress string
	batchSize       uint64
	maxLogRange     uint64
	safetyWindow    uint64
	now             func() time.Time
}

type EthereumScanBatchResult struct {
	StartHeight   uint64
	EndHeight     uint64
	FinalizedHead uint64
	BlocksScanned int
	DepositsFound int
	LogRangeUsed  uint64
}

func NewEthereumScanner(source EthereumScanSource, store EthereumScanStore, options EthereumScannerOptions) (*EthereumScanner, error) {
	if source == nil || store == nil {
		return nil, fmt.Errorf("Ethereum scanner source and store are required")
	}
	if options.Network != NetworkEthereumMainnet && options.Network != NetworkEthereumSepolia {
		return nil, fmt.Errorf("Ethereum scanner does not support network %q", options.Network)
	}
	if !common.IsHexAddress(options.ContractAddress) {
		return nil, fmt.Errorf("invalid Ethereum scanner contract")
	}
	if options.BatchSize < 1 || options.BatchSize > 10_000 || options.MaxLogRange < 1 || options.MaxLogRange > 10_000 || options.SafetyWindow < 1 || options.SafetyWindow > 10_000 {
		return nil, fmt.Errorf("Ethereum scanner batch size, max log range, and safety window must be between 1 and 10000")
	}
	return &EthereumScanner{
		source: source, store: store, network: options.Network,
		contractAddress: common.HexToAddress(options.ContractAddress).Hex(),
		batchSize:       options.BatchSize, maxLogRange: options.MaxLogRange, safetyWindow: options.SafetyWindow,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *EthereumScanner) ScanBatch(ctx context.Context, leaseOwner string) (EthereumScanBatchResult, error) {
	result := EthereumScanBatchResult{}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return result, fmt.Errorf("Ethereum scanner lease owner is required")
	}
	cursor, err := s.store.LoadEthereumScanCursor(ctx, s.network)
	if err != nil {
		return result, fmt.Errorf("load Ethereum scan cursor: %w", err)
	}
	if cursor.LeaseOwner != leaseOwner || !cursor.LeaseUntil.After(s.now()) {
		return result, ErrEthereumScanLeaseLost
	}
	if cursor.Health == CursorHashConflict {
		return result, ErrEthereumScanHashConflict
	}
	finalized, err := s.finalizedBoundary(ctx)
	if err != nil {
		if errors.Is(err, ErrEthereumFinalizedDivergence) {
			return result, s.failHashConflict(ctx, leaseOwner, err)
		}
		return result, fmt.Errorf("read Ethereum finalized head: %w", err)
	}
	result.FinalizedHead = finalized.Number
	result.StartHeight = cursor.FinalizedHeight + 1
	result.EndHeight = cursor.FinalizedHeight
	if finalized.Number < cursor.FinalizedHeight {
		return result, fmt.Errorf("%w: saved height %d is ahead of finalized head %d", ErrEthereumScanCursorConflict, cursor.FinalizedHeight, finalized.Number)
	}
	if err := s.reconcileSafetyWindow(ctx, cursor, finalized, leaseOwner); err != nil {
		return result, err
	}
	if result.StartHeight > finalized.Number {
		return result, nil
	}
	requestedEnd := min(finalized.Number, cursor.FinalizedHeight+s.batchSize)
	requestedEnd = min(requestedEnd, result.StartHeight+s.maxLogRange-1)
	logs, endHeight, err := s.readAdaptiveLogs(ctx, result.StartHeight, requestedEnd)
	if err != nil {
		return result, err
	}
	result.LogRangeUsed = endHeight - result.StartHeight + 1
	sort.SliceStable(logs, func(left, right int) bool {
		if logs[left].BlockNumber != logs[right].BlockNumber {
			return logs[left].BlockNumber < logs[right].BlockNumber
		}
		if logs[left].TxIndex != logs[right].TxIndex {
			return logs[left].TxIndex < logs[right].TxIndex
		}
		return logs[left].Index < logs[right].Index
	})
	byBlock := make(map[uint64][]types.Log)
	for _, logEntry := range logs {
		if logEntry.BlockNumber < result.StartHeight || logEntry.BlockNumber > endHeight {
			return result, fmt.Errorf("Ethereum RPC returned a log outside the requested range")
		}
		byBlock[logEntry.BlockNumber] = append(byBlock[logEntry.BlockNumber], logEntry)
	}
	for height := result.StartHeight; height <= endHeight; height++ {
		block, err := s.source.BlockByNumber(ctx, height)
		if err != nil {
			return result, fmt.Errorf("read Ethereum block %d: %w", height, err)
		}
		if block.Number != height || !common.IsHexHash(block.Hash) {
			return result, fmt.Errorf("Ethereum block %d has invalid identity", height)
		}
		deposits, err := s.scanBlockLogs(ctx, block, finalized, byBlock[height])
		if err != nil {
			return result, fmt.Errorf("validate Ethereum block %d: %w", height, err)
		}
		if err := s.store.CommitEthereumScanBlock(ctx, EthereumScanBlockCommit{
			Network: s.network, LeaseOwner: leaseOwner, Now: s.now(), PreviousHeight: height - 1,
			Block: block, Deposits: deposits,
		}); err != nil {
			return result, fmt.Errorf("commit Ethereum finalized block %d: %w", height, err)
		}
		result.EndHeight = height
		result.BlocksScanned++
		result.DepositsFound += len(deposits)
	}
	return result, nil
}

type ethereumConsistentFinalizedSource interface {
	CommonFinalizedBlock(ctx context.Context) (EthereumBlockRef, error)
}

func (s *EthereumScanner) finalizedBoundary(ctx context.Context) (EthereumBlockRef, error) {
	if source, ok := s.source.(ethereumConsistentFinalizedSource); ok {
		return source.CommonFinalizedBlock(ctx)
	}
	return s.source.FinalizedBlock(ctx)
}

func (s *EthereumScanner) reconcileSafetyWindow(ctx context.Context, cursor EthereumScanCursor, finalized EthereumBlockRef, leaseOwner string) error {
	if cursor.FinalizedHeight == 0 {
		return nil
	}
	if !common.IsHexHash(cursor.FinalizedHash) {
		return s.failHashConflict(ctx, leaseOwner, fmt.Errorf("saved cursor at height %d has no valid block hash", cursor.FinalizedHeight))
	}
	startHeight := max(uint64(1), cursor.FinalizedHeight-safetyWindowOffset(cursor.FinalizedHeight, s.safetyWindow))
	for height := startHeight; height <= cursor.FinalizedHeight; height++ {
		logs, err := s.source.Logs(ctx, height, height, s.contractAddress, [][]common.Hash{{ERC20TransferTopic}})
		if err != nil {
			return fmt.Errorf("rescan Ethereum safety window block %d logs: %w", height, err)
		}
		sort.SliceStable(logs, func(left, right int) bool {
			if logs[left].TxIndex != logs[right].TxIndex {
				return logs[left].TxIndex < logs[right].TxIndex
			}
			return logs[left].Index < logs[right].Index
		})
		block, err := s.source.BlockByNumber(ctx, height)
		if err != nil {
			return fmt.Errorf("rescan Ethereum safety window block %d header: %w", height, err)
		}
		if height == cursor.FinalizedHeight && !strings.EqualFold(block.Hash, cursor.FinalizedHash) {
			return s.failHashConflict(ctx, leaseOwner, fmt.Errorf("saved block hash %s at height %d changed to %s", cursor.FinalizedHash, height, block.Hash))
		}
		deposits, err := s.scanBlockLogs(ctx, block, finalized, logs)
		if err != nil {
			return fmt.Errorf("rescan Ethereum safety window block %d: %w", height, err)
		}
		if err := s.store.ReconcileEthereumScanBlock(ctx, EthereumScanBlockReconcile{
			Network: s.network, LeaseOwner: leaseOwner, Now: s.now(), Block: block, Deposits: deposits,
		}); err != nil {
			if errors.Is(err, ErrEthereumScanHashConflict) || errors.Is(err, ErrEthereumScanCursorConflict) {
				return s.failHashConflict(ctx, leaseOwner, err)
			}
			return fmt.Errorf("reconcile Ethereum safety window block %d: %w", height, err)
		}
	}
	return nil
}

func safetyWindowOffset(height, window uint64) uint64 {
	if height == 0 || window <= 1 {
		return 0
	}
	return min(height-1, window-1)
}

func (s *EthereumScanner) failHashConflict(ctx context.Context, leaseOwner string, cause error) error {
	if err := s.store.MarkEthereumScanHashConflict(ctx, s.network, leaseOwner, s.now(), cause); err != nil {
		return fmt.Errorf("%w: %v (persist conflict state: %v)", ErrEthereumScanHashConflict, cause, err)
	}
	return fmt.Errorf("%w: %v", ErrEthereumScanHashConflict, cause)
}

func (s *EthereumScanner) readAdaptiveLogs(ctx context.Context, startHeight, requestedEnd uint64) ([]types.Log, uint64, error) {
	endHeight := requestedEnd
	for {
		logs, err := s.source.Logs(ctx, startHeight, endHeight, s.contractAddress, [][]common.Hash{{ERC20TransferTopic}})
		if err == nil {
			return logs, endHeight, nil
		}
		if !IsEthereumRPCRetryable(err) || endHeight == startHeight {
			return nil, 0, fmt.Errorf("read Ethereum logs %d-%d: %w", startHeight, endHeight, err)
		}
		endHeight = startHeight + (endHeight-startHeight)/2
	}
}

func (s *EthereumScanner) scanBlockLogs(ctx context.Context, block, finalized EthereumBlockRef, logs []types.Log) ([]EthereumScanDeposit, error) {
	receipts := make(map[common.Hash]EthereumTransactionReceipt)
	deposits := make([]EthereumScanDeposit, 0, len(logs))
	for _, logEntry := range logs {
		recipient, candidate := erc20CandidateRecipient(logEntry)
		if !candidate {
			continue
		}
		intent, found, err := s.store.FindEthereumPaymentIntentByAddress(ctx, s.network, recipient.Hex())
		if err != nil {
			return nil, fmt.Errorf("find Ethereum payment intent for %s: %w", recipient.Hex(), err)
		}
		if !found {
			continue
		}
		receipt, ok := receipts[logEntry.TxHash]
		if !ok {
			receipt, err = s.source.TransactionReceipt(ctx, logEntry.TxHash.Hex())
			if err != nil {
				return nil, fmt.Errorf("read receipt %s: %w", logEntry.TxHash.Hex(), err)
			}
			receipts[logEntry.TxHash] = receipt
		}
		transfer, err := ParseERC20Transfer(logEntry, receipt, ERC20TransferParseOptions{
			Network: s.network, ContractAddress: s.contractAddress, RecipientAddress: intent.DepositAddress,
			Block: block, FinalizedHead: finalized,
		})
		if err != nil {
			continue
		}
		deposits = append(deposits, EthereumScanDeposit{Intent: intent, Transfer: transfer, TransactionIndex: logEntry.TxIndex})
	}
	return deposits, nil
}

func erc20CandidateRecipient(logEntry types.Log) (common.Address, bool) {
	if logEntry.Address == (common.Address{}) || len(logEntry.Topics) != 3 || logEntry.Topics[0] != ERC20TransferTopic {
		return common.Address{}, false
	}
	recipient, err := decodeEthereumAddressTopic("to", logEntry.Topics[2])
	return recipient, err == nil
}
