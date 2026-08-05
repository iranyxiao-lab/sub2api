package repository

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/ethereumgasfunding"
	"github.com/Wei-Shaw/sub2api/ent/ethereumnoncestate"
	"github.com/Wei-Shaw/sub2api/ent/onchaindeposit"
	"github.com/Wei-Shaw/sub2api/ent/onchainpaymentintent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/walletsweep"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *OnchainRepository) ListAdminOnchainHealth(ctx context.Context) ([]service.AdminOnchainHealth, error) {
	db := r.db(ctx)
	cursors, err := db.ChainScanCursor.Query().Order(dbent.Asc("network")).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]service.AdminOnchainHealth, 0, len(cursors))
	for _, cursor := range cursors {
		pending, err := db.OnchainPaymentIntent.Query().Where(
			onchainpaymentintent.NetworkEQ(cursor.Network),
			onchainpaymentintent.StatusIn("PENDING", "PARTIALLY_PAID", "SETTLEMENT_DUE", "SETTLING"),
		).Count(ctx)
		if err != nil {
			return nil, err
		}
		reviews, err := db.OnchainDeposit.Query().Where(
			onchaindeposit.NetworkEQ(cursor.Network),
			onchaindeposit.StatusEQ("REVIEW_REQUIRED"),
		).Count(ctx)
		if err != nil {
			return nil, err
		}
		reconciliationMismatches, err := db.OnchainPaymentIntent.Query().Where(
			onchainpaymentintent.NetworkEQ(cursor.Network),
			onchainpaymentintent.LastErrorCodeEQ(onchain.TRONBalanceReconciliationErrorCode),
		).Count(ctx)
		if err != nil {
			return nil, err
		}
		deposits, err := db.OnchainDeposit.Query().Where(
			onchaindeposit.NetworkEQ(cursor.Network),
			onchaindeposit.FinalizedEQ(true),
			onchaindeposit.ReceiptSuccessEQ(true),
		).All(ctx)
		if err != nil {
			return nil, err
		}
		sweeps, err := db.WalletSweep.Query().Where(walletsweep.NetworkEQ(cursor.Network)).All(ctx)
		if err != nil {
			return nil, err
		}
		depositTotal := new(big.Int)
		for _, deposit := range deposits {
			if err := addAdminRawAmount(depositTotal, deposit.AmountRaw); err != nil {
				return nil, fmt.Errorf("sum %s deposits: %w", cursor.Network, err)
			}
		}
		sweptTotal := new(big.Int)
		gasCostTotal := new(big.Int)
		resourceWaits, retries, maxRetries, failures := 0, 0, 0, 0
		rpcErrors, pendingTransactions, replacements := 0, 0, 0
		var oldestPending time.Time
		for _, sweep := range sweeps {
			retries += sweep.RetryCount
			if sweep.RetryCount > maxRetries {
				maxRetries = sweep.RetryCount
			}
			switch sweep.Status {
			case string(onchain.TransferResourceWait):
				resourceWaits++
			case string(onchain.TransferFailed), string(onchain.TransferReviewRequired):
				failures++
			}
			if sweep.Status == string(onchain.TransferFinalized) {
				if err := addAdminRawAmount(sweptTotal, sweep.AmountRaw); err != nil {
					return nil, fmt.Errorf("sum %s finalized sweeps: %w", cursor.Network, err)
				}
				if cursor.Network == string(onchain.NetworkEthereumMainnet) || cursor.Network == string(onchain.NetworkEthereumSepolia) {
					if err := addAdminRawAmount(gasCostTotal, sweep.FeeRaw); err != nil {
						return nil, fmt.Errorf("sum %s sweep gas costs: %w", cursor.Network, err)
					}
				}
			}
			if cursor.Network == string(onchain.NetworkEthereumMainnet) || cursor.Network == string(onchain.NetworkEthereumSepolia) {
				if sweep.Status == string(onchain.TransferBroadcast) || sweep.Status == string(onchain.TransferConfirming) {
					pendingTransactions++
					oldestPending = earlierAdminMetricTime(oldestPending, sweep.UpdatedAt)
				}
				if sweep.ReplacementOfID != nil {
					replacements++
				}
				if sweep.FailureCode != nil && strings.Contains(strings.ToUpper(*sweep.FailureCode), "RPC") {
					rpcErrors++
				}
			}
		}
		pendingNonces, nonceConflicts := 0, 0
		gasSponsorAddress := ""
		if cursor.Network == string(onchain.NetworkEthereumMainnet) || cursor.Network == string(onchain.NetworkEthereumSepolia) {
			fundings, err := db.EthereumGasFunding.Query().Where(ethereumgasfunding.ChainIDEQ(cursor.ChainID)).All(ctx)
			if err != nil {
				return nil, err
			}
			for _, funding := range fundings {
				if gasSponsorAddress == "" {
					gasSponsorAddress = funding.SponsorAddress
				}
				if funding.Status == string(onchain.TransferBroadcast) || funding.Status == string(onchain.TransferConfirming) {
					pendingTransactions++
					oldestPending = earlierAdminMetricTime(oldestPending, funding.UpdatedAt)
				}
				if funding.ReplacementOfID != nil {
					replacements++
				}
				if funding.FailureCode != nil && strings.Contains(strings.ToUpper(*funding.FailureCode), "RPC") {
					rpcErrors++
				}
				if funding.Finalized && funding.ActualFeeWei != nil {
					if err := addAdminRawAmount(gasCostTotal, *funding.ActualFeeWei); err != nil {
						return nil, fmt.Errorf("sum %s gas funding costs: %w", cursor.Network, err)
					}
				}
			}
			nonceStates, err := db.EthereumNonceState.Query().Where(ethereumnoncestate.ChainIDEQ(cursor.ChainID)).All(ctx)
			if err != nil {
				return nil, err
			}
			for _, state := range nonceStates {
				if state.NextNonce > state.ObservedPendingNonce {
					pendingNonces += int(state.NextNonce - state.ObservedPendingNonce)
				}
				if state.Status == string(onchain.NonceConflict) {
					nonceConflicts++
				}
			}
		}
		if cursor.LastErrorCode != nil && strings.Contains(strings.ToUpper(*cursor.LastErrorCode), "RPC") {
			rpcErrors++
		}
		unswept := new(big.Int).Sub(depositTotal, sweptTotal)
		if unswept.Sign() < 0 {
			unswept.SetInt64(0)
		}
		stuckAgeSeconds := int64(0)
		if !oldestPending.IsZero() {
			stuckAgeSeconds = int64(time.Since(oldestPending.UTC()) / time.Second)
			if stuckAgeSeconds < 0 {
				stuckAgeSeconds = 0
			}
		}
		result = append(result, service.AdminOnchainHealth{
			Network: cursor.Network, ChainID: cursor.ChainID, CursorHealth: cursor.Health,
			FinalizedHeight: cursor.FinalizedHeight, FinalizedHash: cursor.FinalizedHash,
			LeaseOwner: cursor.LeaseOwner, LeaseUntil: cursor.LeaseUntil, LastSuccessAt: cursor.LastSuccessAt,
			LastErrorCode: cursor.LastErrorCode, LastErrorMessage: cursor.LastErrorMessage,
			PendingSettlements: pending, ReviewRequired: reviews,
			ReconciliationMismatches: reconciliationMismatches,
			UnsweptBalanceRaw:        unswept.String(), ResourceWaitSweeps: resourceWaits,
			SweepRetryCount: retries, SweepMaxRetryCount: maxRetries,
			SweepFailureCount: failures, UpdatedAt: cursor.UpdatedAt,
			RPCErrorCount: rpcErrors, PendingNonceCount: pendingNonces,
			NonceConflictCount: nonceConflicts, PendingTransactionCount: pendingTransactions,
			StuckTransactionAgeSeconds: stuckAgeSeconds, ReplacementTransactionCount: replacements,
			GasCostWei: gasCostTotal.String(), GasSponsorAddress: gasSponsorAddress,
		})
	}
	return result, nil
}

func earlierAdminMetricTime(current, candidate time.Time) time.Time {
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}

func addAdminRawAmount(total *big.Int, raw string) error {
	amount, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	if !ok || amount.Sign() < 0 || amount.BitLen() > 256 {
		return fmt.Errorf("invalid raw amount %q", raw)
	}
	total.Add(total, amount)
	return nil
}

func (r *OnchainRepository) ListAdminOnchainDeposits(ctx context.Context, params service.AdminOnchainDepositQuery) ([]service.AdminOnchainDeposit, int, error) {
	query := r.db(ctx).OnchainDeposit.Query()
	if value := strings.TrimSpace(params.Network); value != "" {
		query.Where(onchaindeposit.NetworkEQ(value))
	}
	if value := strings.TrimSpace(params.Status); value != "" {
		query.Where(onchaindeposit.StatusEQ(value))
	}
	if value := strings.TrimSpace(params.TransactionID); value != "" {
		query.Where(onchaindeposit.TransactionIDEQ(value))
	}
	if value := strings.TrimSpace(params.Address); value != "" {
		query.Where(onchaindeposit.Or(onchaindeposit.FromAddressEQ(value), onchaindeposit.ToAddressEQ(value)))
	}
	if params.PaymentOrderID > 0 {
		query.Where(onchaindeposit.PaymentOrderIDEQ(params.PaymentOrderID))
	}
	if params.UserID > 0 {
		query.Where(onchaindeposit.UserIDEQ(params.UserID))
	}
	if params.LogIndex != nil {
		query.Where(onchaindeposit.LogIndexEQ(*params.LogIndex))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	entities, err := query.
		Order(dbent.Desc(onchaindeposit.FieldTransactionTime), dbent.Desc(onchaindeposit.FieldID)).
		Offset((params.Page - 1) * params.PageSize).
		Limit(params.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	result := make([]service.AdminOnchainDeposit, 0, len(entities))
	for _, entity := range entities {
		result = append(result, adminOnchainDeposit(entity))
	}
	return result, total, nil
}

func (r *OnchainRepository) GetAdminOnchainOrderTrace(ctx context.Context, orderID int64) (*service.AdminOnchainOrderTrace, error) {
	intent, err := r.db(ctx).OnchainPaymentIntent.Query().
		Where(onchainpaymentintent.PaymentOrderIDEQ(orderID)).
		WithDeposits(func(query *dbent.OnchainDepositQuery) {
			query.Order(dbent.Asc(onchaindeposit.FieldBlockHeight), dbent.Asc(onchaindeposit.FieldLogIndex))
		}).
		WithWalletSweeps().
		WithEthereumGasFundings().
		Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	audits, err := r.db(ctx).PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10))).
		Order(dbent.Asc(paymentauditlog.FieldCreatedAt), dbent.Asc(paymentauditlog.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	senders := make(map[string]struct{})
	for _, funding := range intent.Edges.EthereumGasFundings {
		senders[strings.ToLower(funding.SponsorAddress)] = struct{}{}
	}
	for _, sweep := range intent.Edges.WalletSweeps {
		if sweep.Network == string(onchain.NetworkEthereumMainnet) || sweep.Network == string(onchain.NetworkEthereumSepolia) {
			senders[strings.ToLower(sweep.SourceAddress)] = struct{}{}
		}
	}
	nonceStates := make([]*dbent.EthereumNonceState, 0)
	if len(senders) > 0 {
		candidates, err := r.db(ctx).EthereumNonceState.Query().
			Where(ethereumnoncestate.ChainIDEQ(intent.ChainID)).
			Order(dbent.Asc(ethereumnoncestate.FieldSenderAddress)).
			All(ctx)
		if err != nil {
			return nil, err
		}
		for _, state := range candidates {
			if _, ok := senders[strings.ToLower(state.SenderAddress)]; ok {
				nonceStates = append(nonceStates, state)
			}
		}
	}

	trace := &service.AdminOnchainOrderTrace{
		Intent: &service.AdminOnchainIntent{
			ID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
			Network: intent.Network, ChainID: intent.ChainID, TokenContract: intent.TokenContract,
			DepositAddress: intent.DepositAddress, DerivationIndex: intent.DerivationIndex,
			ExpectedAmountRaw: intent.ExpectedAmountRaw, ReceivedAmountRaw: intent.ReceivedAmountRaw,
			CreditedAmountRaw: intent.CreditedAmountRaw, OverpaidAmountRaw: intent.OverpaidAmountRaw,
			ConfigVersion: intent.ConfigVersion, Status: intent.Status,
			SettlementIdempotencyKey: intent.SettlementIdempotencyKey, SettlementAttempts: intent.SettlementAttempts,
			NextSettlementAt: intent.NextSettlementAt, LastErrorCode: intent.LastErrorCode,
			LastErrorMessage: intent.LastErrorMessage, SettledAt: intent.SettledAt,
			CreatedAt: intent.CreatedAt, UpdatedAt: intent.UpdatedAt,
		},
		Deposits:      make([]service.AdminOnchainDeposit, 0, len(intent.Edges.Deposits)),
		BalanceAudits: make([]service.AdminOnchainBalanceAudit, 0, len(audits)),
		Sweeps:        make([]service.AdminWalletSweep, 0, len(intent.Edges.WalletSweeps)),
		GasFundings:   make([]service.AdminEthereumGasFunding, 0, len(intent.Edges.EthereumGasFundings)),
		NonceStates:   make([]service.AdminEthereumNonceState, 0, len(nonceStates)),
	}
	for _, audit := range audits {
		trace.BalanceAudits = append(trace.BalanceAudits, service.AdminOnchainBalanceAudit{
			ID: audit.ID, Action: audit.Action, Detail: audit.Detail,
			Operator: audit.Operator, CreatedAt: audit.CreatedAt,
		})
	}
	for _, deposit := range intent.Edges.Deposits {
		trace.Deposits = append(trace.Deposits, adminOnchainDeposit(deposit))
	}
	for _, sweep := range intent.Edges.WalletSweeps {
		trace.Sweeps = append(trace.Sweeps, service.AdminWalletSweep{
			ID: sweep.ID, IntentID: sweep.IntentID, Network: sweep.Network, ChainID: sweep.ChainID,
			SourceAddress: sweep.SourceAddress, DestinationAddress: sweep.DestinationAddress,
			BalanceSnapshotRaw: sweep.BalanceSnapshotRaw, AmountRaw: sweep.AmountRaw,
			IdempotencyKey: sweep.IdempotencyKey, SignerAuditID: sweep.SignerAuditID,
			TransactionID: sweep.TransactionID, Nonce: sweep.Nonce, ReplacementOfID: sweep.ReplacementOfID,
			FeeRaw: sweep.FeeRaw, EnergyUsed: sweep.EnergyUsed, BandwidthUsed: sweep.BandwidthUsed,
			FinalizedBlockHeight: sweep.FinalizedBlockHeight, FinalizedBlockHash: sweep.FinalizedBlockHash,
			Status: sweep.Status, RetryCount: sweep.RetryCount, FailureCode: sweep.FailureCode,
			FailureReason: sweep.FailureReason, NextAttemptAt: sweep.NextAttemptAt,
			FinalizedAt: sweep.FinalizedAt, CreatedAt: sweep.CreatedAt, UpdatedAt: sweep.UpdatedAt,
		})
	}
	for _, funding := range intent.Edges.EthereumGasFundings {
		trace.GasFundings = append(trace.GasFundings, service.AdminEthereumGasFunding{
			ID: funding.ID, IntentID: funding.IntentID, ChainID: funding.ChainID,
			SponsorAddress: funding.SponsorAddress, TargetAddress: funding.TargetAddress,
			DerivationIndex: funding.DerivationIndex, AmountWei: funding.AmountWei,
			IdempotencyKey: funding.IdempotencyKey, Nonce: funding.Nonce, GasLimit: funding.GasLimit,
			MaxFeePerGasWei:         funding.MaxFeePerGasWei,
			MaxPriorityFeePerGasWei: funding.MaxPriorityFeePerGasWei,
			TransactionHash:         funding.TransactionHash, ReplacementOfID: funding.ReplacementOfID,
			Status: funding.Status, Finalized: funding.Finalized,
			FinalizedBlockHeight: funding.FinalizedBlockHeight, FinalizedBlockHash: funding.FinalizedBlockHash,
			ActualFeeWei: funding.ActualFeeWei, RetryCount: funding.RetryCount,
			FailureCode: funding.FailureCode, FailureReason: funding.FailureReason,
			NextAttemptAt: funding.NextAttemptAt, FinalizedAt: funding.FinalizedAt,
			CreatedAt: funding.CreatedAt, UpdatedAt: funding.UpdatedAt,
		})
	}
	for _, state := range nonceStates {
		trace.NonceStates = append(trace.NonceStates, service.AdminEthereumNonceState{
			ID: state.ID, ChainID: state.ChainID, SenderAddress: state.SenderAddress,
			NextNonce: state.NextNonce, ObservedPendingNonce: state.ObservedPendingNonce,
			Status: state.Status, LeaseOwner: state.LeaseOwner, LeaseUntil: state.LeaseUntil,
			LastReconciledAt: state.LastReconciledAt, LastErrorCode: state.LastErrorCode,
			LastErrorMessage: state.LastErrorMessage, CreatedAt: state.CreatedAt, UpdatedAt: state.UpdatedAt,
		})
	}
	return trace, nil
}

func adminOnchainDeposit(entity *dbent.OnchainDeposit) service.AdminOnchainDeposit {
	return service.AdminOnchainDeposit{
		ID: entity.ID, IntentID: entity.IntentID, PaymentOrderID: entity.PaymentOrderID, UserID: entity.UserID,
		Network: entity.Network, ChainID: entity.ChainID, TransactionID: entity.TransactionID,
		LogIndex: entity.LogIndex, TransactionIndex: entity.TransactionIndex,
		BlockHeight: entity.BlockHeight, BlockHash: entity.BlockHash, TokenContract: entity.TokenContract,
		FromAddress: entity.FromAddress, ToAddress: entity.ToAddress, AmountRaw: entity.AmountRaw,
		TransactionTime: entity.TransactionTime, ReceiptSuccess: entity.ReceiptSuccess,
		Finalized: entity.Finalized, Status: entity.Status,
		ValidationError: entity.ValidationError, CreditAuditRef: entity.CreditAuditRef,
		CreditedAt: entity.CreditedAt, CreatedAt: entity.CreatedAt, UpdatedAt: entity.UpdatedAt,
	}
}
