package onchain

import (
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
)

type TRONSweepExecutionTask struct {
	ID                 int64
	IntentID           int64
	DerivationIndex    int64
	SourceAddress      string
	DestinationAddress string
	BalanceSnapshotRaw string
	AmountRaw          string
	IdempotencyKey     string
	TransactionID      string
	Status             TransferStatus
	RetryCount         int
	Version            int
}

type TRONSweepFinalization struct {
	TaskID        int64
	Version       int
	BlockHeight   int64
	BlockHash     string
	FeeRaw        string
	EnergyUsed    int64
	BandwidthUsed int64
	FinalizedAt   time.Time
}

type TRONSweepExecutionStore interface {
	ListTRONSweepExecutionTasks(ctx context.Context, network Network, now time.Time, limit int, includeSigning bool) ([]TRONSweepExecutionTask, error)
	MarkTRONSweepSigning(ctx context.Context, taskID int64, version int, requestDigest string) error
	RecordTRONSweepBroadcast(ctx context.Context, taskID int64, version int, signerAuditID, transactionID string) error
	RecordTRONSweepRetry(ctx context.Context, taskID int64, version int, status TransferStatus, failureCode, failureReason string, nextAttempt time.Time, review bool) error
	ScheduleTRONSweepCheck(ctx context.Context, taskID int64, version int, status TransferStatus, nextAttempt time.Time) error
	MarkTRONSweepConfirming(ctx context.Context, taskID int64, version int) error
	FinalizeTRONSweep(ctx context.Context, input TRONSweepFinalization) error
}

type TRONSweepExecutionNode interface {
	TRC20Balance(ctx context.Context, contract, address string) (*big.Int, error)
	SolidifiedTRONTransaction(ctx context.Context, transactionID string) (TRONSolidifiedTransaction, error)
}

type TRC20SweepSigner interface {
	SweepTRC20(ctx context.Context, request signerv1.SweepTRC20Request) (*signerv1.OperationResponse, error)
}

type TRONSweepExecutorOptions struct {
	Network                Network
	ContractAddress        string
	DestinationAddress     string
	BatchSize              int
	MaxConsecutiveFailures int
	RetryBackoff           time.Duration
	ConfirmationPoll       time.Duration
}

type TRONSweepExecutionResult struct {
	Tasks     int
	Broadcast int
	Pending   int
	Finalized int
	Retried   int
	Review    int
	Failed    int
}

type TRONSweepExecutor struct {
	store            TRONSweepExecutionStore
	node             TRONSweepExecutionNode
	signer           TRC20SweepSigner
	network          Network
	contract         string
	destination      string
	batchSize        int
	maxFailures      int
	retryBackoff     time.Duration
	confirmationPoll time.Duration
	now              func() time.Time
}

func NewTRONSweepExecutor(store TRONSweepExecutionStore, node TRONSweepExecutionNode, signer TRC20SweepSigner, options TRONSweepExecutorOptions) (*TRONSweepExecutor, error) {
	if signer == nil {
		return nil, fmt.Errorf("TRON sweep executor store, node, and signer are required")
	}
	return newTRONSweepExecutor(store, node, signer, options)
}

// NewTRONSweepTracker creates an executor that can only track transactions
// which were broadcast before new funds operations were disabled.
func NewTRONSweepTracker(store TRONSweepExecutionStore, node TRONSweepExecutionNode, options TRONSweepExecutorOptions) (*TRONSweepExecutor, error) {
	return newTRONSweepExecutor(store, node, nil, options)
}

func newTRONSweepExecutor(store TRONSweepExecutionStore, node TRONSweepExecutionNode, signer TRC20SweepSigner, options TRONSweepExecutorOptions) (*TRONSweepExecutor, error) {
	if store == nil || node == nil {
		return nil, fmt.Errorf("TRON sweep execution store and node are required")
	}
	if options.Network != NetworkTronMainnet && options.Network != NetworkTronNile {
		return nil, fmt.Errorf("TRON sweep executor does not support network %q", options.Network)
	}
	if err := ValidateAddress(options.Network, options.ContractAddress); err != nil {
		return nil, fmt.Errorf("invalid TRON sweep executor contract: %w", err)
	}
	if err := ValidateAddress(options.Network, options.DestinationAddress); err != nil {
		return nil, fmt.Errorf("invalid TRON sweep executor destination: %w", err)
	}
	if options.BatchSize < 1 || options.BatchSize > 1000 || options.MaxConsecutiveFailures < 1 || options.MaxConsecutiveFailures > 100 {
		return nil, fmt.Errorf("TRON sweep executor batch and failure limits are invalid")
	}
	if options.RetryBackoff <= 0 || options.ConfirmationPoll <= 0 {
		return nil, fmt.Errorf("TRON sweep executor intervals must be positive")
	}
	return &TRONSweepExecutor{
		store: store, node: node, signer: signer, network: options.Network,
		contract: strings.TrimSpace(options.ContractAddress), destination: strings.TrimSpace(options.DestinationAddress),
		batchSize: options.BatchSize, maxFailures: options.MaxConsecutiveFailures,
		retryBackoff: options.RetryBackoff, confirmationPoll: options.ConfirmationPoll,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (e *TRONSweepExecutor) RunOnce(ctx context.Context) (TRONSweepExecutionResult, error) {
	if e.signer == nil {
		return TRONSweepExecutionResult{}, fmt.Errorf("TRON sweep signing is disabled")
	}
	return e.runOnce(ctx, true)
}

// TrackSolidificationOnce keeps already-broadcast transactions moving while
// policy gates pause new signing and broadcast operations.
func (e *TRONSweepExecutor) TrackSolidificationOnce(ctx context.Context) (TRONSweepExecutionResult, error) {
	return e.runOnce(ctx, false)
}

func (e *TRONSweepExecutor) runOnce(ctx context.Context, signingEnabled bool) (TRONSweepExecutionResult, error) {
	tasks, err := e.store.ListTRONSweepExecutionTasks(ctx, e.network, e.now(), e.batchSize, signingEnabled)
	if err != nil {
		return TRONSweepExecutionResult{}, fmt.Errorf("list TRON sweep execution tasks: %w", err)
	}
	result := TRONSweepExecutionResult{Tasks: len(tasks)}
	var batchErr error
	for _, task := range tasks {
		var taskErr error
		switch task.Status {
		case TransferPrepared, TransferSigning:
			if signingEnabled {
				taskErr = e.executeSigning(ctx, &task, &result)
			}
		case TransferBroadcast, TransferConfirming:
			taskErr = e.trackSolidification(ctx, &task, &result)
		default:
			taskErr = fmt.Errorf("unsupported TRON sweep execution status %s", task.Status)
		}
		if taskErr != nil {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("TRON sweep task %d: %w", task.ID, taskErr))
		}
	}
	return result, batchErr
}

func (e *TRONSweepExecutor) executeSigning(ctx context.Context, task *TRONSweepExecutionTask, result *TRONSweepExecutionResult) error {
	request, digest, err := tronSweepSignerRequest(*task)
	if err != nil {
		return e.review(ctx, task, "INVALID_SIGNER_REQUEST", err.Error(), result)
	}
	if task.Status == TransferPrepared {
		if err := e.store.MarkTRONSweepSigning(ctx, task.ID, task.Version, digest); err != nil {
			return err
		}
		task.Status = TransferSigning
		task.Version++
	}
	amount, _ := new(big.Int).SetString(task.AmountRaw, 10)
	balanceBefore, err := e.node.TRC20Balance(ctx, e.contract, task.SourceAddress)
	if err != nil {
		return e.retry(ctx, task, "BALANCE_QUERY_FAILED", err.Error(), result)
	}
	if balanceBefore == nil || balanceBefore.Sign() < 0 {
		return e.retry(ctx, task, "BALANCE_QUERY_INVALID", "node returned an invalid TRC20 balance", result)
	}
	if balanceBefore.Cmp(amount) < 0 {
		return e.review(ctx, task, "BALANCE_CHANGED", "source balance is below the persisted sweep amount", result)
	}

	response, signerErr := e.signer.SweepTRC20(ctx, request)
	if signerErr != nil {
		balanceAfter, balanceErr := e.node.TRC20Balance(ctx, e.contract, task.SourceAddress)
		if balanceErr == nil && balanceAfter != nil && balanceAfter.Cmp(balanceBefore) != 0 {
			return e.review(ctx, task, "BROADCAST_OUTCOME_UNKNOWN", "source balance changed and signer did not recover the transaction ID", result)
		}
		if balanceErr != nil {
			signerErr = errors.Join(signerErr, fmt.Errorf("recheck source balance: %w", balanceErr))
		}
		return e.retry(ctx, task, "SIGNER_CALL_FAILED", signerErr.Error(), result)
	}
	if err := validateTRONSweepSignerResponse(request, response); err != nil {
		return e.review(ctx, task, "INVALID_SIGNER_RESPONSE", err.Error(), result)
	}
	if err := e.store.RecordTRONSweepBroadcast(ctx, task.ID, task.Version, response.AuditID, response.TransactionID); err != nil {
		return err
	}
	result.Broadcast++
	return nil
}

func (e *TRONSweepExecutor) trackSolidification(ctx context.Context, task *TRONSweepExecutionTask, result *TRONSweepExecutionResult) error {
	if strings.TrimSpace(task.TransactionID) == "" {
		return e.review(ctx, task, "MISSING_TRANSACTION_ID", "broadcast task has no transaction ID", result)
	}
	status, err := e.node.SolidifiedTRONTransaction(ctx, task.TransactionID)
	if err != nil {
		return e.retry(ctx, task, "SOLIDIFIED_QUERY_FAILED", err.Error(), result)
	}
	if !status.Found {
		if err := e.store.ScheduleTRONSweepCheck(ctx, task.ID, task.Version, task.Status, e.now().Add(e.confirmationPoll)); err != nil {
			return err
		}
		result.Pending++
		return nil
	}
	if !status.Success {
		return e.review(ctx, task, "ONCHAIN_EXECUTION_FAILED", "solidified TRON transaction receipt failed", result)
	}
	if err := validateFinalizedTRONSweep(status.Receipt, e.network, e.contract, task.SourceAddress, e.destination, task.AmountRaw); err != nil {
		return e.review(ctx, task, "FINALIZED_SEMANTIC_MISMATCH", err.Error(), result)
	}
	if task.Status == TransferBroadcast {
		if err := e.store.MarkTRONSweepConfirming(ctx, task.ID, task.Version); err != nil {
			return err
		}
		task.Status = TransferConfirming
		task.Version++
	}
	if err := e.store.FinalizeTRONSweep(ctx, TRONSweepFinalization{
		TaskID: task.ID, Version: task.Version, BlockHeight: status.BlockHeight, BlockHash: status.BlockHash,
		FeeRaw: fmt.Sprintf("%d", status.FeeSun), EnergyUsed: status.EnergyUsed,
		BandwidthUsed: status.BandwidthUsed, FinalizedAt: e.now(),
	}); err != nil {
		return err
	}
	result.Finalized++
	return nil
}

func (e *TRONSweepExecutor) retry(ctx context.Context, task *TRONSweepExecutionTask, code, reason string, result *TRONSweepExecutionResult) error {
	review := task.RetryCount+1 >= e.maxFailures
	if err := e.store.RecordTRONSweepRetry(
		ctx, task.ID, task.Version, task.Status, code, reason, e.now().Add(e.retryBackoff), review,
	); err != nil {
		return err
	}
	if review {
		result.Review++
	} else {
		result.Retried++
	}
	return nil
}

func (e *TRONSweepExecutor) review(ctx context.Context, task *TRONSweepExecutionTask, code, reason string, result *TRONSweepExecutionResult) error {
	if err := e.store.RecordTRONSweepRetry(ctx, task.ID, task.Version, task.Status, code, reason, e.now(), true); err != nil {
		return err
	}
	result.Review++
	return nil
}

func tronSweepSignerRequest(task TRONSweepExecutionTask) (signerv1.SweepTRC20Request, string, error) {
	if task.ID <= 0 || task.DerivationIndex < 0 || task.DerivationIndex >= int64(hdkeychain.HardenedKeyStart) {
		return signerv1.SweepTRC20Request{}, "", fmt.Errorf("task identity or derivation index is invalid")
	}
	request := signerv1.SweepTRC20Request{
		TaskID: fmt.Sprintf("tron-sweep-%d", task.ID), DerivationIndex: uint32(task.DerivationIndex),
		RawAmount: task.AmountRaw, IdempotencyKey: task.IdempotencyKey,
	}
	if err := request.Validate(); err != nil {
		return signerv1.SweepTRC20Request{}, "", err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return signerv1.SweepTRC20Request{}, "", err
	}
	digest := sha256.Sum256(payload)
	return request, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateTRONSweepSignerResponse(request signerv1.SweepTRC20Request, response *signerv1.OperationResponse) error {
	if response == nil || response.TaskID != request.TaskID || response.IdempotencyKey != request.IdempotencyKey {
		return fmt.Errorf("signer response identity does not match the request")
	}
	if response.Status != "broadcast" || strings.TrimSpace(response.TransactionID) == "" || strings.TrimSpace(response.AuditID) == "" {
		return fmt.Errorf("signer response does not contain a broadcast transaction and audit identity")
	}
	if _, err := normalizeFixedTRONHex("signer transaction ID", response.TransactionID, 32); err != nil {
		return err
	}
	return nil
}

func validateFinalizedTRONSweep(receipt TRONTransactionReceipt, network Network, contract, source, destination, amountRaw string) error {
	expectedAmount, ok := new(big.Int).SetString(amountRaw, 10)
	if !ok || expectedAmount.Sign() <= 0 {
		return fmt.Errorf("sweep amount is invalid")
	}
	for logIndex := range receipt.Logs {
		transfer, err := ParseTRC20Transfer(receipt, logIndex, TRC20TransferParseOptions{
			Network: network, ContractAddress: contract, RecipientAddress: destination,
		})
		if err != nil {
			continue
		}
		amount, ok := new(big.Int).SetString(transfer.AmountRaw, 10)
		if ok && transfer.FromAddress == source && amount.Cmp(expectedAmount) == 0 {
			return nil
		}
	}
	return fmt.Errorf("solidified receipt does not contain the expected USDT transfer")
}
