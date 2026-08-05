package onchain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const ethereumSweepIdempotencyPrefix = "ethereum-sweep-v1:"

type EthereumSweepExecutionTask struct {
	ID                 int64
	IntentID           int64
	ChainID            int64
	DerivationIndex    int64
	SourceAddress      string
	DestinationAddress string
	BalanceSnapshotRaw string
	AmountRaw          string
	IdempotencyKey     string
	TransactionHash    string
	Nonce              uint64
	Versions           []EthereumTransactionVersionReference
	Status             TransferStatus
	RetryCount         int
	Version            int
}

type EthereumSweepReplacement struct {
	TaskID                     int64
	Version                    int
	SignerAuditID              string
	OriginalTransactionHash    string
	ReplacementTransactionHash string
	Nonce                      uint64
	NextAttemptAt              time.Time
}

type EthereumSweepExecutionStore interface {
	ListEthereumSweepExecutionTasks(ctx context.Context, network Network, now time.Time, limit int) ([]EthereumSweepExecutionTask, error)
	MarkEthereumSweepSigning(ctx context.Context, taskID int64, version int, requestDigest string) error
	RecordEthereumSweepBroadcast(ctx context.Context, taskID int64, version int, signerAuditID, transactionHash string, nonce uint64) error
	RecordEthereumSweepReplacement(ctx context.Context, input EthereumSweepReplacement) error
	ScheduleEthereumSweepCheck(ctx context.Context, taskID int64, version int, nextAttempt time.Time) error
	RecordEthereumSweepRetry(ctx context.Context, taskID int64, version int, status TransferStatus, failureCode, failureReason string, nextAttempt time.Time, review bool) error
	FinalizeEthereumSweep(ctx context.Context, input EthereumSweepFinalization) error
}

type EthereumSweepReceiptSource interface {
	TransactionReceipt(ctx context.Context, transactionHash string) (EthereumTransactionReceipt, error)
}

type EthereumSweepTrackingSource interface {
	EthereumSweepReceiptSource
	TransactionByHash(ctx context.Context, transactionHash string) (EthereumTransaction, error)
	FinalizedBlock(ctx context.Context) (EthereumBlockRef, error)
	BlockByNumber(ctx context.Context, number uint64) (EthereumBlockRef, error)
}

type EthereumSweepFinalization struct {
	TaskID       int64
	Version      int
	WinnerID     int64
	WinnerHash   string
	BlockHeight  int64
	BlockHash    string
	ActualFeeWei string
	FinalizedAt  time.Time
}

type ERC20SweepSigner interface {
	SweepERC20(ctx context.Context, request signerv1.SweepERC20Request) (*signerv1.OperationResponse, error)
}

type EthereumSweepWorkerOptions struct {
	Network                Network
	ChainID                uint64
	AccountXPub            string
	ContractAddress        string
	DestinationAddress     string
	MinimumAmountRaw       string
	MaxGasBudgetWei        string
	BatchSize              int
	MaxConsecutiveFailures int
	RetryBackoff           time.Duration
}

type EthereumSweepExecutionResult struct {
	Tasks     int
	Broadcast int
	GasWait   int
	Retried   int
	Review    int
	Failed    int
	Finalized int
}

type EthereumSweepWorker struct {
	store        EthereumSweepExecutionStore
	source       EthereumSweepPreflightSource
	signer       ERC20SweepSigner
	network      Network
	chainID      uint64
	accountXPub  string
	contract     string
	destination  string
	minimum      string
	maxGasBudget string
	batchSize    int
	maxFailures  int
	retryBackoff time.Duration
	now          func() time.Time
}

func NewEthereumSweepWorker(store EthereumSweepExecutionStore, source EthereumSweepPreflightSource, signer ERC20SweepSigner, options EthereumSweepWorkerOptions) (*EthereumSweepWorker, error) {
	if store == nil || source == nil || signer == nil {
		return nil, fmt.Errorf("Ethereum sweep store, source, and signer are required")
	}
	definition, ok := NetworkDefinition(options.Network)
	if !ok || (options.Network != NetworkEthereumMainnet && options.Network != NetworkEthereumSepolia) || definition.ChainID != options.ChainID {
		return nil, fmt.Errorf("Ethereum sweep network and chain ID are invalid")
	}
	if _, err := DeriveEthereumAddress(strings.TrimSpace(options.AccountXPub), 0); err != nil {
		return nil, fmt.Errorf("Ethereum sweep xpub: %w", err)
	}
	if err := ValidateAddress(options.Network, options.ContractAddress); err != nil {
		return nil, fmt.Errorf("Ethereum sweep contract: %w", err)
	}
	if err := ValidateAddress(options.Network, options.DestinationAddress); err != nil {
		return nil, fmt.Errorf("Ethereum sweep destination: %w", err)
	}
	if _, err := positiveCanonicalInteger(options.MinimumAmountRaw, DefaultEthereumMinimumSweepRaw); err != nil {
		return nil, fmt.Errorf("Ethereum sweep minimum: %w", err)
	}
	if _, err := positiveCanonicalInteger(options.MaxGasBudgetWei, DefaultEthereumMaxGasBudgetWei); err != nil {
		return nil, fmt.Errorf("Ethereum sweep gas budget: %w", err)
	}
	if options.BatchSize < 1 || options.BatchSize > 1000 || options.MaxConsecutiveFailures < 1 || options.MaxConsecutiveFailures > 100 || options.RetryBackoff <= 0 {
		return nil, fmt.Errorf("Ethereum sweep worker limits are invalid")
	}
	return &EthereumSweepWorker{
		store: store, source: source, signer: signer, network: options.Network, chainID: options.ChainID,
		accountXPub: strings.TrimSpace(options.AccountXPub), contract: strings.TrimSpace(options.ContractAddress),
		destination: strings.TrimSpace(options.DestinationAddress), minimum: strings.TrimSpace(options.MinimumAmountRaw),
		maxGasBudget: strings.TrimSpace(options.MaxGasBudgetWei), batchSize: options.BatchSize,
		maxFailures: options.MaxConsecutiveFailures, retryBackoff: options.RetryBackoff,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (w *EthereumSweepWorker) RunOnce(ctx context.Context) (EthereumSweepExecutionResult, error) {
	tasks, err := w.store.ListEthereumSweepExecutionTasks(ctx, w.network, w.now(), w.batchSize)
	if err != nil {
		return EthereumSweepExecutionResult{}, fmt.Errorf("list Ethereum sweep tasks: %w", err)
	}
	result := EthereumSweepExecutionResult{Tasks: len(tasks)}
	var batchErr error
	for i := range tasks {
		if err := w.execute(ctx, &tasks[i], &result); err != nil {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("Ethereum sweep task %d: %w", tasks[i].ID, err))
		}
	}
	return result, batchErr
}

func (w *EthereumSweepWorker) execute(ctx context.Context, task *EthereumSweepExecutionTask, result *EthereumSweepExecutionResult) error {
	request, digest, err := w.signerRequest(*task)
	if err != nil {
		return w.review(ctx, task, "INVALID_SWEEP_TASK", err.Error(), result)
	}
	if task.Status == TransferBroadcast {
		return w.recoverSweep(ctx, task, request, result)
	}
	preflight, err := EvaluateEthereumSweep(ctx, w.source, EthereumSweepPreflightOptions{
		ContractAddress: w.contract, SourceAddress: task.SourceAddress, SweepAddress: w.destination,
		MinimumAmountRaw: w.minimum, MaxGasBudgetWei: w.maxGasBudget,
	})
	if err != nil {
		return w.retry(ctx, task, "PREFLIGHT_FAILED", err.Error(), result)
	}
	if !preflight.Eligible {
		return w.review(ctx, task, preflight.Reason, "persisted sweep is no longer economically eligible", result)
	}
	if preflight.RequiredFundingWei != "0" {
		result.GasWait++
		return w.retry(ctx, task, "GAS_FUNDING_NOT_FINALIZED", "source ETH balance cannot cover the current bounded sweep fee", result)
	}
	if preflight.USDTBalanceRaw != task.BalanceSnapshotRaw || preflight.USDTBalanceRaw != task.AmountRaw {
		return w.review(ctx, task, "BALANCE_CHANGED", "source USDT balance changed after the sweep task was created", result)
	}
	if task.Status == TransferPrepared {
		if err := w.store.MarkEthereumSweepSigning(ctx, task.ID, task.Version, digest); err != nil {
			return err
		}
		task.Status = TransferSigning
		task.Version++
	}
	response, err := w.signer.SweepERC20(ctx, request)
	if err != nil {
		return w.retry(ctx, task, "SIGNER_FAILED", err.Error(), result)
	}
	if err := validateEthereumSweepSignerResponse(request, response); err != nil {
		return w.review(ctx, task, "INVALID_SIGNER_RESPONSE", err.Error(), result)
	}
	if err := w.store.RecordEthereumSweepBroadcast(ctx, task.ID, task.Version, response.AuditID, response.TransactionID, response.Nonce); err != nil {
		return err
	}
	result.Broadcast++
	return nil
}

func (w *EthereumSweepWorker) recoverSweep(ctx context.Context, task *EthereumSweepExecutionTask, request signerv1.SweepERC20Request, result *EthereumSweepExecutionResult) error {
	if !common.IsHexHash(task.TransactionHash) {
		return w.review(ctx, task, "INVALID_TRANSACTION_HASH", "persisted Ethereum sweep hash is invalid", result)
	}
	receiptSource, ok := w.source.(EthereumSweepTrackingSource)
	if !ok {
		return w.review(ctx, task, "RECEIPT_SOURCE_UNAVAILABLE", "Ethereum sweep source cannot verify the current transaction before replacement", result)
	}
	finalized, err := receiptSource.FinalizedBlock(ctx)
	if err != nil {
		return w.retry(ctx, task, "FINALIZED_QUERY_FAILED", err.Error(), result)
	}
	versions := task.Versions
	if len(versions) == 0 {
		versions = []EthereumTransactionVersionReference{{ID: task.ID, TransactionHash: task.TransactionHash, Status: task.Status}}
	}
	for _, version := range versions {
		receipt, receiptErr := receiptSource.TransactionReceipt(ctx, version.TransactionHash)
		if errors.Is(receiptErr, ErrEthereumTransactionReceiptNotFound) {
			continue
		}
		if receiptErr != nil {
			return w.retry(ctx, task, "RECEIPT_QUERY_FAILED", receiptErr.Error(), result)
		}
		transaction, transactionErr := receiptSource.TransactionByHash(ctx, version.TransactionHash)
		if transactionErr != nil {
			return w.retry(ctx, task, "TRANSACTION_QUERY_FAILED", transactionErr.Error(), result)
		}
		if receipt.Status != 1 || !strings.EqualFold(receipt.TransactionHash, version.TransactionHash) || validateEthereumSweepSemantics(transaction, receipt, *task, w.contract, w.destination) != nil {
			return w.review(ctx, task, "SWEEP_RECEIPT_INVALID", "Ethereum sweep receipt, call, or Transfer semantics are invalid", result)
		}
		if receipt.BlockNumber > finalized.Number {
			return w.scheduleSweep(ctx, task, result)
		}
		block, blockErr := receiptSource.BlockByNumber(ctx, receipt.BlockNumber)
		if blockErr != nil {
			return w.retry(ctx, task, "FINALIZED_BLOCK_QUERY_FAILED", blockErr.Error(), result)
		}
		if block.Number != receipt.BlockNumber || !strings.EqualFold(block.Hash, receipt.BlockHash) {
			return w.review(ctx, task, "FINALIZED_HASH_MISMATCH", "sweep receipt block hash does not match the finalized canonical block", result)
		}
		actualFee := new(big.Int)
		if receipt.EffectiveGasPrice != nil && receipt.EffectiveGasPrice.Sign() >= 0 {
			actualFee.Mul(new(big.Int).SetUint64(receipt.GasUsed), receipt.EffectiveGasPrice)
		}
		finalizedAt := w.now()
		if !block.Timestamp.IsZero() {
			finalizedAt = block.Timestamp.UTC()
		}
		if err := w.store.FinalizeEthereumSweep(ctx, EthereumSweepFinalization{TaskID: task.ID, Version: task.Version, WinnerID: version.ID, WinnerHash: version.TransactionHash, BlockHeight: int64(receipt.BlockNumber), BlockHash: receipt.BlockHash, ActualFeeWei: actualFee.String(), FinalizedAt: finalizedAt}); err != nil {
			return err
		}
		result.Finalized++
		return nil
	}
	response, err := w.signer.SweepERC20(ctx, request)
	if err != nil {
		return w.retry(ctx, task, "REPLACEMENT_SIGNER_FAILED", err.Error(), result)
	}
	if err := validateEthereumSweepSignerResponse(request, response); err != nil {
		return w.review(ctx, task, "INVALID_REPLACEMENT_RESPONSE", err.Error(), result)
	}
	if strings.EqualFold(response.TransactionID, task.TransactionHash) && response.ReplacementOfTransactionID == "" {
		return w.scheduleSweep(ctx, task, result)
	}
	if !strings.EqualFold(response.ReplacementOfTransactionID, task.TransactionHash) || response.TransactionVersion < 2 || response.Nonce > uint64(^uint64(0)>>1) {
		return w.review(ctx, task, "INVALID_REPLACEMENT_RESPONSE", "signer replacement metadata does not match the current sweep", result)
	}
	if err := w.store.RecordEthereumSweepReplacement(ctx, EthereumSweepReplacement{TaskID: task.ID, Version: task.Version, SignerAuditID: response.AuditID, OriginalTransactionHash: task.TransactionHash, ReplacementTransactionHash: response.TransactionID, Nonce: response.Nonce, NextAttemptAt: w.now().Add(w.retryBackoff)}); err != nil {
		return err
	}
	result.Broadcast++
	return nil
}

func validateEthereumSweepSemantics(transaction EthereumTransaction, receipt EthereumTransactionReceipt, task EthereumSweepExecutionTask, contract, destination string) error {
	amount, ok := new(big.Int).SetString(task.AmountRaw, 10)
	if !ok || transaction.ChainID != uint64(task.ChainID) || !strings.EqualFold(transaction.From, task.SourceAddress) || !strings.EqualFold(transaction.To, contract) || transaction.Nonce != task.Nonce || transaction.ValueWei != "0" || len(transaction.Input) != 68 {
		return fmt.Errorf("Ethereum sweep transaction semantics mismatch")
	}
	selector := crypto.Keccak256([]byte("transfer(address,uint256)"))[:4]
	if !bytes.Equal(transaction.Input[:4], selector) || !bytes.Equal(transaction.Input[4:16], make([]byte, 12)) || common.BytesToAddress(transaction.Input[16:36]) != common.HexToAddress(destination) || new(big.Int).SetBytes(transaction.Input[36:]).Cmp(amount) != 0 {
		return fmt.Errorf("Ethereum sweep calldata semantics mismatch")
	}
	expectedFrom := common.HexToAddress(task.SourceAddress)
	expectedTo := common.HexToAddress(destination)
	matched := 0
	for _, logEntry := range receipt.Logs {
		if logEntry.Address != common.HexToAddress(contract) || len(logEntry.Topics) != 3 || logEntry.Topics[0] != ERC20TransferTopic || len(logEntry.Data) != 32 {
			continue
		}
		from, fromErr := decodeEthereumAddressTopic("from", logEntry.Topics[1])
		to, toErr := decodeEthereumAddressTopic("to", logEntry.Topics[2])
		if fromErr == nil && toErr == nil && from == expectedFrom && to == expectedTo && new(big.Int).SetBytes(logEntry.Data).Cmp(amount) == 0 {
			matched++
		}
	}
	if matched != 1 {
		return fmt.Errorf("Ethereum sweep Transfer receipt semantics mismatch")
	}
	return nil
}

func (w *EthereumSweepWorker) scheduleSweep(ctx context.Context, task *EthereumSweepExecutionTask, result *EthereumSweepExecutionResult) error {
	if err := w.store.ScheduleEthereumSweepCheck(ctx, task.ID, task.Version, w.now().Add(w.retryBackoff)); err != nil {
		return err
	}
	return nil
}

func (w *EthereumSweepWorker) signerRequest(task EthereumSweepExecutionTask) (signerv1.SweepERC20Request, string, error) {
	if task.ID <= 0 || task.IntentID <= 0 || task.ChainID != int64(w.chainID) || task.DerivationIndex < 0 || task.DerivationIndex >= int64(hdkeychain.HardenedKeyStart) {
		return signerv1.SweepERC20Request{}, "", fmt.Errorf("Ethereum sweep task identity is invalid")
	}
	derived, err := DeriveEthereumAddress(w.accountXPub, uint32(task.DerivationIndex))
	if err != nil || !strings.EqualFold(derived, task.SourceAddress) {
		return signerv1.SweepERC20Request{}, "", fmt.Errorf("Ethereum sweep source does not match its registered derivation index")
	}
	if !strings.EqualFold(task.DestinationAddress, w.destination) {
		return signerv1.SweepERC20Request{}, "", fmt.Errorf("Ethereum sweep destination is not the fixed collection address")
	}
	if !strings.HasPrefix(task.IdempotencyKey, ethereumSweepIdempotencyPrefix) {
		return signerv1.SweepERC20Request{}, "", fmt.Errorf("Ethereum sweep does not use its dedicated idempotency namespace")
	}
	amount, ok := new(big.Int).SetString(task.AmountRaw, 10)
	if !ok || amount.Sign() <= 0 || amount.BitLen() > 256 || amount.String() != task.AmountRaw {
		return signerv1.SweepERC20Request{}, "", fmt.Errorf("Ethereum sweep amount is invalid")
	}
	request := signerv1.SweepERC20Request{
		TaskID: fmt.Sprintf("ethereum-sweep-%d", task.ID), DerivationIndex: uint32(task.DerivationIndex),
		RawAmount: task.AmountRaw, IdempotencyKey: task.IdempotencyKey,
	}
	if err := request.Validate(); err != nil {
		return signerv1.SweepERC20Request{}, "", err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return signerv1.SweepERC20Request{}, "", err
	}
	digest := sha256.Sum256(payload)
	return request, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (w *EthereumSweepWorker) retry(ctx context.Context, task *EthereumSweepExecutionTask, code, reason string, result *EthereumSweepExecutionResult) error {
	review := task.RetryCount+1 >= w.maxFailures
	if err := w.store.RecordEthereumSweepRetry(ctx, task.ID, task.Version, task.Status, code, reason, w.now().Add(w.retryBackoff), review); err != nil {
		return err
	}
	if review {
		result.Review++
	} else {
		result.Retried++
	}
	return nil
}

func (w *EthereumSweepWorker) review(ctx context.Context, task *EthereumSweepExecutionTask, code, reason string, result *EthereumSweepExecutionResult) error {
	if err := w.store.RecordEthereumSweepRetry(ctx, task.ID, task.Version, task.Status, code, reason, w.now().Add(w.retryBackoff), true); err != nil {
		return err
	}
	result.Review++
	return nil
}

func validateEthereumSweepSignerResponse(request signerv1.SweepERC20Request, response *signerv1.OperationResponse) error {
	if response == nil || response.TaskID != request.TaskID || response.IdempotencyKey != request.IdempotencyKey ||
		response.Status != "broadcast" || strings.TrimSpace(response.TransactionID) == "" || strings.TrimSpace(response.AuditID) == "" {
		return fmt.Errorf("signer response does not identify the expected broadcast sweep")
	}
	return nil
}

func EthereumSweepIdempotencyKey(intentID, derivationIndex, fundingMarker int64, amountRaw string) (string, error) {
	amount, ok := new(big.Int).SetString(strings.TrimSpace(amountRaw), 10)
	if intentID <= 0 || derivationIndex < 0 || fundingMarker <= 0 || !ok || amount.Sign() <= 0 || amount.BitLen() > 256 || amount.String() != strings.TrimSpace(amountRaw) {
		return "", fmt.Errorf("invalid Ethereum sweep idempotency input")
	}
	payload := fmt.Sprintf("v1|%d|%d|%d|%s", intentID, derivationIndex, fundingMarker, amount.String())
	digest := sha256.Sum256([]byte(payload))
	return ethereumSweepIdempotencyPrefix + hex.EncodeToString(digest[:]), nil
}
