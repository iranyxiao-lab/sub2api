package onchain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const TRONBalanceReconciliationErrorCode = "BALANCE_RECONCILIATION_MISMATCH"

var ErrTRONReconciliationBoundaryMoved = errors.New("TRON solidified reconciliation boundary moved")

type TRONBalanceReconciliationTarget struct {
	IntentID            int64
	Network             Network
	TokenContract       string
	Address             string
	IntentStatus        IntentStatus
	RecordedDepositsRaw string
	CreditedAmountRaw   string
	FinalizedSweptRaw   string
	HasInFlightSweep    bool
}

type TRONBalanceReconciliationMismatch struct {
	IntentID            int64
	ReasonCode          string
	Reason              string
	RecordedDepositsRaw string
	CreditedAmountRaw   string
	FinalizedSweptRaw   string
	ExpectedBalanceRaw  string
	ActualBalanceRaw    string
}

type TRONBalanceReconciliationStore interface {
	LoadTRONScanCursor(ctx context.Context, network Network) (TRONScanCursor, error)
	ListTRONBalanceReconciliationTargets(ctx context.Context, network Network, afterIntentID int64, limit int) ([]TRONBalanceReconciliationTarget, error)
	MarkTRONBalanceReconciliationReview(ctx context.Context, mismatch TRONBalanceReconciliationMismatch) error
}

type TRONBalanceReconciliationNode interface {
	LatestSolidifiedHeight(ctx context.Context) (int64, error)
	SolidifiedTRC20Balance(ctx context.Context, contract, address string) (*big.Int, error)
}

type TRONBalanceReconcilerOptions struct {
	Network         Network
	ContractAddress string
	BatchSize       int
}

type TRONBalanceReconciliationResult struct {
	Targets    int
	Checked    int
	Skipped    int
	Mismatches int
	Reviewed   int
	Failed     int
	Deferred   bool
}

type TRONBalanceReconciler struct {
	store     TRONBalanceReconciliationStore
	node      TRONBalanceReconciliationNode
	network   Network
	contract  string
	batchSize int
}

func NewTRONBalanceReconciler(store TRONBalanceReconciliationStore, node TRONBalanceReconciliationNode, options TRONBalanceReconcilerOptions) (*TRONBalanceReconciler, error) {
	if store == nil || node == nil {
		return nil, fmt.Errorf("TRON balance reconciliation store and node are required")
	}
	if options.Network != NetworkTronMainnet && options.Network != NetworkTronNile {
		return nil, fmt.Errorf("TRON balance reconciliation does not support network %q", options.Network)
	}
	if err := ValidateAddress(options.Network, options.ContractAddress); err != nil {
		return nil, fmt.Errorf("invalid TRON reconciliation contract: %w", err)
	}
	if options.BatchSize < 1 || options.BatchSize > 1000 {
		return nil, fmt.Errorf("TRON reconciliation batch size must be between 1 and 1000")
	}
	return &TRONBalanceReconciler{
		store: store, node: node, network: options.Network,
		contract: strings.TrimSpace(options.ContractAddress), batchSize: options.BatchSize,
	}, nil
}

func (r *TRONBalanceReconciler) RunOnce(ctx context.Context) (TRONBalanceReconciliationResult, error) {
	var result TRONBalanceReconciliationResult
	var batchErr error
	cursor, err := r.store.LoadTRONScanCursor(ctx, r.network)
	if err != nil {
		return result, fmt.Errorf("load TRON reconciliation scan cursor: %w", err)
	}
	solidifiedHeight, err := r.node.LatestSolidifiedHeight(ctx)
	if err != nil {
		return result, fmt.Errorf("read TRON reconciliation solidified height: %w", err)
	}
	if cursor.Health != CursorHealthy || cursor.FinalizedHeight != solidifiedHeight {
		result.Deferred = true
		return result, nil
	}
	var afterIntentID int64
	for {
		targets, err := r.store.ListTRONBalanceReconciliationTargets(ctx, r.network, afterIntentID, r.batchSize)
		if err != nil {
			return result, errors.Join(batchErr, fmt.Errorf("list TRON reconciliation targets: %w", err))
		}
		if len(targets) == 0 {
			return result, batchErr
		}
		result.Targets += len(targets)
		for _, target := range targets {
			if target.IntentID <= afterIntentID {
				return result, errors.Join(batchErr, fmt.Errorf("TRON reconciliation targets are not ordered by intent ID"))
			}
			afterIntentID = target.IntentID
			if target.HasInFlightSweep {
				result.Skipped++
				continue
			}
			mismatch, reconcileErr := r.reconcileTarget(ctx, target, solidifiedHeight)
			if reconcileErr != nil {
				result.Failed++
				batchErr = errors.Join(batchErr, fmt.Errorf("reconcile TRON intent %d: %w", target.IntentID, reconcileErr))
				continue
			}
			result.Checked++
			if mismatch == nil {
				continue
			}
			result.Mismatches++
			if err := r.store.MarkTRONBalanceReconciliationReview(ctx, *mismatch); err != nil {
				result.Failed++
				batchErr = errors.Join(batchErr, fmt.Errorf("mark TRON intent %d for reconciliation review: %w", target.IntentID, err))
				continue
			}
			result.Reviewed++
		}
		if len(targets) < r.batchSize {
			return result, batchErr
		}
	}
}

func (r *TRONBalanceReconciler) reconcileTarget(ctx context.Context, target TRONBalanceReconciliationTarget, solidifiedHeight int64) (*TRONBalanceReconciliationMismatch, error) {
	base := TRONBalanceReconciliationMismatch{
		IntentID: target.IntentID, RecordedDepositsRaw: target.RecordedDepositsRaw,
		CreditedAmountRaw: target.CreditedAmountRaw, FinalizedSweptRaw: target.FinalizedSweptRaw,
	}
	if target.Network != r.network {
		return reconciliationMismatch(base, "NETWORK_MISMATCH", "intent network does not match the reconciliation network"), nil
	}
	if !strings.EqualFold(strings.TrimSpace(target.TokenContract), r.contract) {
		return reconciliationMismatch(base, "TOKEN_CONTRACT_MISMATCH", "intent token contract does not match the configured USDT contract"), nil
	}
	if target.IntentID <= 0 {
		return nil, fmt.Errorf("invalid intent ID")
	}
	if err := ValidateAddress(r.network, target.Address); err != nil {
		return reconciliationMismatch(base, "INVALID_DEPOSIT_ADDRESS", err.Error()), nil
	}

	recorded, err := reconciliationAmount(target.RecordedDepositsRaw)
	if err != nil {
		return reconciliationMismatch(base, "INVALID_RECORDED_DEPOSITS", err.Error()), nil
	}
	credited, err := reconciliationAmount(target.CreditedAmountRaw)
	if err != nil {
		return reconciliationMismatch(base, "INVALID_CREDITED_AMOUNT", err.Error()), nil
	}
	swept, err := reconciliationAmount(target.FinalizedSweptRaw)
	if err != nil {
		return reconciliationMismatch(base, "INVALID_FINALIZED_SWEEP_AMOUNT", err.Error()), nil
	}
	if swept.Cmp(recorded) > 0 {
		return reconciliationMismatch(base, "SWEEPS_EXCEED_DEPOSITS", "finalized sweep amount exceeds recorded finalized deposits"), nil
	}
	expectedBalance := new(big.Int).Sub(new(big.Int).Set(recorded), swept)
	base.ExpectedBalanceRaw = expectedBalance.String()
	if credited.Cmp(recorded) > 0 {
		return reconciliationMismatch(base, "CREDIT_EXCEEDS_DEPOSITS", "credited amount exceeds recorded finalized deposits"), nil
	}
	if target.IntentStatus == IntentSettled && credited.Cmp(recorded) != 0 {
		return reconciliationMismatch(base, "SETTLED_CREDIT_MISMATCH", "settled intent credited amount does not equal recorded finalized deposits"), nil
	}
	if target.IntentStatus != IntentSettled && target.IntentStatus != IntentReviewRequired && credited.Sign() != 0 {
		return reconciliationMismatch(base, "UNSETTLED_CREDIT_PRESENT", "unsettled intent has a non-zero credited amount"), nil
	}

	actualBalance, err := r.node.SolidifiedTRC20Balance(ctx, r.contract, target.Address)
	if err != nil {
		return nil, err
	}
	if actualBalance == nil || actualBalance.Sign() < 0 || actualBalance.BitLen() > 256 {
		return nil, fmt.Errorf("node returned an invalid TRC20 balance")
	}
	base.ActualBalanceRaw = actualBalance.String()
	if actualBalance.Cmp(expectedBalance) != 0 {
		currentHeight, err := r.node.LatestSolidifiedHeight(ctx)
		if err != nil {
			return nil, err
		}
		if currentHeight != solidifiedHeight {
			return nil, ErrTRONReconciliationBoundaryMoved
		}
		return reconciliationMismatch(base, "ONCHAIN_BALANCE_MISMATCH", "recorded deposits minus finalized sweeps does not equal the current onchain balance"), nil
	}
	return nil, nil
}

func reconciliationAmount(raw string) (*big.Int, error) {
	value := strings.TrimSpace(raw)
	amount, ok := new(big.Int).SetString(value, 10)
	if !ok || amount.Sign() < 0 || amount.BitLen() > 256 || amount.String() != value {
		return nil, fmt.Errorf("invalid canonical uint256 amount %q", raw)
	}
	return amount, nil
}

func reconciliationMismatch(base TRONBalanceReconciliationMismatch, reasonCode, reason string) *TRONBalanceReconciliationMismatch {
	base.ReasonCode = reasonCode
	base.Reason = fmt.Sprintf(
		"%s: %s (recorded=%s credited=%s finalized_swept=%s expected_balance=%s actual_balance=%s)",
		reasonCode, reason, base.RecordedDepositsRaw, base.CreditedAmountRaw,
		base.FinalizedSweptRaw, base.ExpectedBalanceRaw, base.ActualBalanceRaw,
	)
	return &base
}
