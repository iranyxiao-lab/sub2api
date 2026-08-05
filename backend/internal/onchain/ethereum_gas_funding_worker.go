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
	"github.com/ethereum/go-ethereum/common"
)

const ethereumGasFundingIdempotencyPrefix = "ethereum-gas-v1:"

type EthereumGasFundingTask struct {
	ID              int64
	IntentID        int64
	ChainID         int64
	SponsorAddress  string
	TargetAddress   string
	DerivationIndex int64
	AmountWei       string
	IdempotencyKey  string
	TransactionHash string
	Nonce           uint64
	GasLimit        uint64
	Versions        []EthereumTransactionVersionReference
	Status          TransferStatus
	RetryCount      int
	Version         int
}

type EthereumTransactionVersionReference struct {
	ID              int64
	TransactionHash string
	Status          TransferStatus
}

type EthereumGasFundingFinalization struct {
	TaskID       int64
	Version      int
	WinnerID     int64
	WinnerHash   string
	BlockHeight  int64
	BlockHash    string
	ActualFeeWei string
	FinalizedAt  time.Time
}

type EthereumGasFundingReplacement struct {
	TaskID                     int64
	Version                    int
	SignerAuditID              string
	OriginalTransactionHash    string
	ReplacementTransactionHash string
	Nonce                      uint64
	GasLimit                   uint64
	MaxFeePerGasWei            string
	MaxPriorityFeePerGasWei    string
	NextAttemptAt              time.Time
}

type EthereumGasFundingStore interface {
	ListEthereumGasFundingTasks(ctx context.Context, chainID int64, now time.Time, limit int, includeSigning bool) ([]EthereumGasFundingTask, error)
	MarkEthereumGasFundingSigning(ctx context.Context, taskID int64, version int, requestDigest string) error
	RecordEthereumGasFundingBroadcast(ctx context.Context, taskID int64, version int, signerAuditID, transactionHash string) error
	RecordEthereumGasFundingReplacement(ctx context.Context, input EthereumGasFundingReplacement) error
	RecordEthereumGasFundingRetry(ctx context.Context, taskID int64, version int, status TransferStatus, failureCode, failureReason string, nextAttempt time.Time, review bool) error
	ScheduleEthereumGasFundingCheck(ctx context.Context, taskID int64, version int, status TransferStatus, nextAttempt time.Time) error
	MarkEthereumGasFundingConfirming(ctx context.Context, taskID int64, version int) error
	FinalizeEthereumGasFunding(ctx context.Context, input EthereumGasFundingFinalization) error
}

type EthereumGasFundingNode interface {
	FinalizedBlock(ctx context.Context) (EthereumBlockRef, error)
	BlockByNumber(ctx context.Context, number uint64) (EthereumBlockRef, error)
	TransactionReceipt(ctx context.Context, transactionHash string) (EthereumTransactionReceipt, error)
	TransactionByHash(ctx context.Context, transactionHash string) (EthereumTransaction, error)
}

type ERC20GasFundingSigner interface {
	FundERC20Gas(ctx context.Context, request signerv1.FundERC20GasRequest) (*signerv1.OperationResponse, error)
}

type EthereumGasFundingWorkerOptions struct {
	Network                Network
	ChainID                uint64
	AccountXPub            string
	BatchSize              int
	MaxConsecutiveFailures int
	RetryBackoff           time.Duration
	ConfirmationPoll       time.Duration
}

type EthereumGasFundingResult struct {
	Tasks     int
	Broadcast int
	Pending   int
	Finalized int
	Retried   int
	Review    int
	Failed    int
}

type EthereumGasFundingWorker struct {
	store            EthereumGasFundingStore
	node             EthereumGasFundingNode
	signer           ERC20GasFundingSigner
	network          Network
	chainID          uint64
	accountXPub      string
	batchSize        int
	maxFailures      int
	retryBackoff     time.Duration
	confirmationPoll time.Duration
	now              func() time.Time
}

func NewEthereumGasFundingWorker(store EthereumGasFundingStore, node EthereumGasFundingNode, signer ERC20GasFundingSigner, options EthereumGasFundingWorkerOptions) (*EthereumGasFundingWorker, error) {
	if signer == nil {
		return nil, fmt.Errorf("Ethereum gas funding signer is required")
	}
	return newEthereumGasFundingWorker(store, node, signer, options)
}

func NewEthereumGasFundingTracker(store EthereumGasFundingStore, node EthereumGasFundingNode, options EthereumGasFundingWorkerOptions) (*EthereumGasFundingWorker, error) {
	return newEthereumGasFundingWorker(store, node, nil, options)
}

func newEthereumGasFundingWorker(store EthereumGasFundingStore, node EthereumGasFundingNode, signer ERC20GasFundingSigner, options EthereumGasFundingWorkerOptions) (*EthereumGasFundingWorker, error) {
	if store == nil || node == nil {
		return nil, fmt.Errorf("Ethereum gas funding store and node are required")
	}
	definition, ok := NetworkDefinition(options.Network)
	if !ok || (options.Network != NetworkEthereumMainnet && options.Network != NetworkEthereumSepolia) || definition.ChainID != options.ChainID {
		return nil, fmt.Errorf("Ethereum gas funding network and chain ID are invalid")
	}
	if _, err := DeriveEthereumAddress(strings.TrimSpace(options.AccountXPub), 0); err != nil {
		return nil, fmt.Errorf("Ethereum gas funding xpub: %w", err)
	}
	if options.BatchSize < 1 || options.BatchSize > 1000 || options.MaxConsecutiveFailures < 1 || options.MaxConsecutiveFailures > 100 {
		return nil, fmt.Errorf("Ethereum gas funding batch and failure limits are invalid")
	}
	if options.RetryBackoff <= 0 || options.ConfirmationPoll <= 0 {
		return nil, fmt.Errorf("Ethereum gas funding intervals must be positive")
	}
	return &EthereumGasFundingWorker{
		store: store, node: node, signer: signer, network: options.Network, chainID: options.ChainID,
		accountXPub: strings.TrimSpace(options.AccountXPub), batchSize: options.BatchSize,
		maxFailures: options.MaxConsecutiveFailures, retryBackoff: options.RetryBackoff,
		confirmationPoll: options.ConfirmationPoll, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (w *EthereumGasFundingWorker) RunOnce(ctx context.Context) (EthereumGasFundingResult, error) {
	if w.signer == nil {
		return EthereumGasFundingResult{}, fmt.Errorf("Ethereum gas funding signing is disabled")
	}
	return w.runOnce(ctx, true)
}

func (w *EthereumGasFundingWorker) TrackFinalizationOnce(ctx context.Context) (EthereumGasFundingResult, error) {
	return w.runOnce(ctx, false)
}

func (w *EthereumGasFundingWorker) runOnce(ctx context.Context, signingEnabled bool) (EthereumGasFundingResult, error) {
	tasks, err := w.store.ListEthereumGasFundingTasks(ctx, int64(w.chainID), w.now(), w.batchSize, signingEnabled)
	if err != nil {
		return EthereumGasFundingResult{}, fmt.Errorf("list Ethereum gas funding tasks: %w", err)
	}
	result := EthereumGasFundingResult{Tasks: len(tasks)}
	var batchErr error
	for i := range tasks {
		task := &tasks[i]
		var taskErr error
		switch task.Status {
		case TransferPrepared, TransferSigning:
			if signingEnabled {
				taskErr = w.executeFunding(ctx, task, &result)
			}
		case TransferBroadcast, TransferConfirming:
			taskErr = w.trackFinalization(ctx, task, &result, signingEnabled)
		default:
			taskErr = fmt.Errorf("unsupported Ethereum gas funding status %s", task.Status)
		}
		if taskErr != nil {
			result.Failed++
			batchErr = errors.Join(batchErr, fmt.Errorf("Ethereum gas funding task %d: %w", task.ID, taskErr))
		}
	}
	return result, batchErr
}

func (w *EthereumGasFundingWorker) executeFunding(ctx context.Context, task *EthereumGasFundingTask, result *EthereumGasFundingResult) error {
	request, digest, err := w.signerRequest(*task)
	if err != nil {
		return w.review(ctx, task, "INVALID_FUNDING_TASK", err.Error(), result)
	}
	if task.Status == TransferPrepared {
		if err := w.store.MarkEthereumGasFundingSigning(ctx, task.ID, task.Version, digest); err != nil {
			return err
		}
		task.Status = TransferSigning
		task.Version++
	}
	response, err := w.signer.FundERC20Gas(ctx, request)
	if err != nil {
		return w.retry(ctx, task, "SIGNER_FAILED", err.Error(), result)
	}
	if err := validateEthereumGasFundingSignerResponse(request, response); err != nil {
		return w.review(ctx, task, "INVALID_SIGNER_RESPONSE", err.Error(), result)
	}
	if err := w.store.RecordEthereumGasFundingBroadcast(ctx, task.ID, task.Version, response.AuditID, response.TransactionID); err != nil {
		return err
	}
	result.Broadcast++
	return nil
}

func (w *EthereumGasFundingWorker) signerRequest(task EthereumGasFundingTask) (signerv1.FundERC20GasRequest, string, error) {
	if task.ID <= 0 || task.IntentID <= 0 || task.ChainID != int64(w.chainID) || task.DerivationIndex < 0 || task.DerivationIndex >= int64(hdkeychain.HardenedKeyStart) {
		return signerv1.FundERC20GasRequest{}, "", fmt.Errorf("Ethereum gas funding task identity is invalid")
	}
	derived, err := DeriveEthereumAddress(w.accountXPub, uint32(task.DerivationIndex))
	if err != nil {
		return signerv1.FundERC20GasRequest{}, "", fmt.Errorf("recompute registered Ethereum address: %w", err)
	}
	if !strings.EqualFold(derived, strings.TrimSpace(task.TargetAddress)) {
		return signerv1.FundERC20GasRequest{}, "", fmt.Errorf("funding target does not match its registered derivation index")
	}
	if !strings.HasPrefix(task.IdempotencyKey, ethereumGasFundingIdempotencyPrefix) {
		return signerv1.FundERC20GasRequest{}, "", fmt.Errorf("funding task does not use the Ethereum gas idempotency namespace")
	}
	request := signerv1.FundERC20GasRequest{
		TaskID: fmt.Sprintf("ethereum-gas-%d", task.ID), DerivationIndex: uint32(task.DerivationIndex),
		RawAmount: task.AmountWei, IdempotencyKey: task.IdempotencyKey,
	}
	if err := request.Validate(); err != nil {
		return signerv1.FundERC20GasRequest{}, "", err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return signerv1.FundERC20GasRequest{}, "", err
	}
	digest := sha256.Sum256(payload)
	return request, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (w *EthereumGasFundingWorker) trackFinalization(ctx context.Context, task *EthereumGasFundingTask, result *EthereumGasFundingResult, signingEnabled bool) error {
	if !common.IsHexHash(task.TransactionHash) {
		return w.review(ctx, task, "INVALID_TRANSACTION_HASH", "persisted Ethereum transaction hash is invalid", result)
	}
	finalized, err := w.node.FinalizedBlock(ctx)
	if err != nil {
		return w.retry(ctx, task, "FINALIZED_QUERY_FAILED", err.Error(), result)
	}
	versions := task.Versions
	if len(versions) == 0 {
		versions = []EthereumTransactionVersionReference{{ID: task.ID, TransactionHash: task.TransactionHash, Status: task.Status}}
	}
	for _, version := range versions {
		receipt, receiptErr := w.node.TransactionReceipt(ctx, version.TransactionHash)
		if errors.Is(receiptErr, ErrEthereumTransactionReceiptNotFound) {
			continue
		}
		if receiptErr != nil {
			return w.retry(ctx, task, "RECEIPT_QUERY_FAILED", receiptErr.Error(), result)
		}
		transaction, transactionErr := w.node.TransactionByHash(ctx, version.TransactionHash)
		if transactionErr != nil {
			return w.retry(ctx, task, "TRANSACTION_QUERY_FAILED", transactionErr.Error(), result)
		}
		if receipt.Status != 1 || !strings.EqualFold(receipt.TransactionHash, version.TransactionHash) || validateEthereumGasFundingSemantics(transaction, *task) != nil {
			return w.review(ctx, task, "FUNDING_RECEIPT_INVALID", "Ethereum gas funding receipt or transaction semantics are invalid", result)
		}
		if receipt.BlockNumber > finalized.Number {
			if task.Status == TransferBroadcast {
				if err := w.store.MarkEthereumGasFundingConfirming(ctx, task.ID, task.Version); err != nil {
					return err
				}
				task.Status = TransferConfirming
				task.Version++
			}
			return w.schedule(ctx, task, result)
		}
		block, blockErr := w.node.BlockByNumber(ctx, receipt.BlockNumber)
		if blockErr != nil {
			return w.retry(ctx, task, "FINALIZED_BLOCK_QUERY_FAILED", blockErr.Error(), result)
		}
		if block.Number != receipt.BlockNumber || !strings.EqualFold(block.Hash, receipt.BlockHash) {
			return w.review(ctx, task, "FINALIZED_HASH_MISMATCH", "funding receipt block hash does not match the finalized canonical block", result)
		}
		actualFee := new(big.Int)
		if receipt.EffectiveGasPrice != nil && receipt.EffectiveGasPrice.Sign() >= 0 {
			actualFee.Mul(new(big.Int).SetUint64(receipt.GasUsed), receipt.EffectiveGasPrice)
		}
		finalizedAt := w.now()
		if !block.Timestamp.IsZero() {
			finalizedAt = block.Timestamp.UTC()
		}
		if err := w.store.FinalizeEthereumGasFunding(ctx, EthereumGasFundingFinalization{TaskID: task.ID, Version: task.Version, WinnerID: version.ID, WinnerHash: version.TransactionHash, BlockHeight: int64(receipt.BlockNumber), BlockHash: receipt.BlockHash, ActualFeeWei: actualFee.String(), FinalizedAt: finalizedAt}); err != nil {
			return err
		}
		result.Finalized++
		return nil
	}
	if signingEnabled && task.Status == TransferBroadcast {
		return w.recoverFunding(ctx, task, result)
	}
	return w.schedule(ctx, task, result)
}

func validateEthereumGasFundingSemantics(transaction EthereumTransaction, task EthereumGasFundingTask) error {
	amount, ok := new(big.Int).SetString(task.AmountWei, 10)
	if !ok || !common.IsHexHash(transaction.Hash) || transaction.ChainID != uint64(task.ChainID) || !strings.EqualFold(transaction.From, task.SponsorAddress) || !strings.EqualFold(transaction.To, task.TargetAddress) || transaction.Nonce != task.Nonce || transaction.GasLimit != task.GasLimit || transaction.ValueWei != amount.String() || len(transaction.Input) != 0 {
		return fmt.Errorf("Ethereum gas funding transaction semantics mismatch")
	}
	return nil
}

func (w *EthereumGasFundingWorker) recoverFunding(ctx context.Context, task *EthereumGasFundingTask, result *EthereumGasFundingResult) error {
	request, _, err := w.signerRequest(*task)
	if err != nil {
		return w.review(ctx, task, "INVALID_FUNDING_TASK", err.Error(), result)
	}
	response, err := w.signer.FundERC20Gas(ctx, request)
	if err != nil {
		return w.retry(ctx, task, "REPLACEMENT_SIGNER_FAILED", err.Error(), result)
	}
	if err := validateEthereumGasFundingSignerResponse(request, response); err != nil {
		return w.review(ctx, task, "INVALID_REPLACEMENT_RESPONSE", err.Error(), result)
	}
	if strings.EqualFold(response.TransactionID, task.TransactionHash) && response.ReplacementOfTransactionID == "" {
		return w.schedule(ctx, task, result)
	}
	if !strings.EqualFold(response.ReplacementOfTransactionID, task.TransactionHash) || response.TransactionVersion < 2 || response.Nonce > uint64(^uint64(0)>>1) ||
		response.GasLimit < 21_000 || response.MaxFeePerGasWei == "" || response.MaxPriorityFeePerGasWei == "" {
		return w.review(ctx, task, "INVALID_REPLACEMENT_RESPONSE", "signer replacement metadata does not match the current transaction", result)
	}
	if err := w.store.RecordEthereumGasFundingReplacement(ctx, EthereumGasFundingReplacement{
		TaskID: task.ID, Version: task.Version, SignerAuditID: response.AuditID,
		OriginalTransactionHash: task.TransactionHash, ReplacementTransactionHash: response.TransactionID,
		Nonce: response.Nonce, GasLimit: response.GasLimit, MaxFeePerGasWei: response.MaxFeePerGasWei,
		MaxPriorityFeePerGasWei: response.MaxPriorityFeePerGasWei, NextAttemptAt: w.now().Add(w.confirmationPoll),
	}); err != nil {
		return err
	}
	result.Broadcast++
	return nil
}

func (w *EthereumGasFundingWorker) schedule(ctx context.Context, task *EthereumGasFundingTask, result *EthereumGasFundingResult) error {
	if err := w.store.ScheduleEthereumGasFundingCheck(ctx, task.ID, task.Version, task.Status, w.now().Add(w.confirmationPoll)); err != nil {
		return err
	}
	result.Pending++
	return nil
}

func (w *EthereumGasFundingWorker) retry(ctx context.Context, task *EthereumGasFundingTask, code, reason string, result *EthereumGasFundingResult) error {
	review := task.RetryCount+1 >= w.maxFailures
	if err := w.store.RecordEthereumGasFundingRetry(ctx, task.ID, task.Version, task.Status, code, reason, w.now().Add(w.retryBackoff), review); err != nil {
		return err
	}
	if review {
		result.Review++
	} else {
		result.Retried++
	}
	return nil
}

func (w *EthereumGasFundingWorker) review(ctx context.Context, task *EthereumGasFundingTask, code, reason string, result *EthereumGasFundingResult) error {
	if err := w.store.RecordEthereumGasFundingRetry(ctx, task.ID, task.Version, task.Status, code, reason, w.now().Add(w.retryBackoff), true); err != nil {
		return err
	}
	result.Review++
	return nil
}

func validateEthereumGasFundingSignerResponse(request signerv1.FundERC20GasRequest, response *signerv1.OperationResponse) error {
	if response == nil || response.TaskID != request.TaskID || response.IdempotencyKey != request.IdempotencyKey {
		return fmt.Errorf("signer response identity does not match the gas funding request")
	}
	if response.Status != "broadcast" || !common.IsHexHash(response.TransactionID) || strings.TrimSpace(response.AuditID) == "" {
		return fmt.Errorf("signer response does not contain a broadcast Ethereum transaction and audit identity")
	}
	return nil
}

func EthereumGasFundingIdempotencyKey(intentID, derivationIndex int64, fundingMarker int64, amountWei string) (string, error) {
	amount, ok := new(big.Int).SetString(strings.TrimSpace(amountWei), 10)
	if intentID <= 0 || derivationIndex < 0 || fundingMarker <= 0 || !ok || amount.Sign() <= 0 || amount.BitLen() > 256 || amount.String() != strings.TrimSpace(amountWei) {
		return "", fmt.Errorf("invalid Ethereum gas funding idempotency input")
	}
	payload := fmt.Sprintf("v1|%d|%d|%d|%s", intentID, derivationIndex, fundingMarker, amount.String())
	digest := sha256.Sum256([]byte(payload))
	return ethereumGasFundingIdempotencyPrefix + hex.EncodeToString(digest[:]), nil
}
