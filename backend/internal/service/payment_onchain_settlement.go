package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/onchaindeposit"
	"github.com/Wei-Shaw/sub2api/ent/onchainpaymentintent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

type onchainSettlementClaim struct {
	intentID       int64
	orderID        int64
	idempotencyKey string
	receivedRaw    string
	alreadySettled bool
}

// ConfirmOnchainDeposit is the domain boundary between the chain ledger and
// the existing balance fulfillment flow. Integer amounts decide eligibility;
// PaymentOrder floating-point fields remain display and compatibility fields.
func (s *PaymentService) ConfirmOnchainDeposit(ctx context.Context, intentID int64) error {
	claim, err := s.claimOnchainSettlement(ctx, intentID)
	if err != nil {
		return err
	}
	if claim.alreadySettled {
		return nil
	}
	if err := s.ExecuteBalanceFulfillment(ctx, claim.orderID); err != nil {
		return fmt.Errorf("fulfill onchain balance order: %w", err)
	}
	if err := s.finalizeOnchainSettlement(ctx, claim); err != nil {
		return err
	}
	return nil
}

func (s *PaymentService) claimOnchainSettlement(ctx context.Context, intentID int64) (onchainSettlementClaim, error) {
	if s == nil || s.entClient == nil {
		return onchainSettlementClaim{}, fmt.Errorf("payment service is unavailable")
	}
	if intentID <= 0 {
		return onchainSettlementClaim{}, infraerrors.BadRequest("INVALID_ONCHAIN_INTENT", "invalid onchain payment intent")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return onchainSettlementClaim{}, fmt.Errorf("begin onchain settlement claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)

	intent, err := tx.OnchainPaymentIntent.Get(txCtx, intentID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return onchainSettlementClaim{}, infraerrors.NotFound("ONCHAIN_INTENT_NOT_FOUND", "onchain payment intent not found")
		}
		return onchainSettlementClaim{}, fmt.Errorf("load onchain payment intent: %w", err)
	}
	key := onchainSettlementIdempotencyKey(intent.ID)
	claim := onchainSettlementClaim{intentID: intent.ID, orderID: intent.PaymentOrderID, idempotencyKey: key}
	if onchain.IntentStatus(intent.Status) == onchain.IntentSettled {
		claim.alreadySettled = true
		claim.receivedRaw = intent.CreditedAmountRaw
		if err := tx.Commit(); err != nil {
			return onchainSettlementClaim{}, fmt.Errorf("commit settled onchain lookup: %w", err)
		}
		return claim, nil
	}

	order, err := tx.PaymentOrder.Get(txCtx, intent.PaymentOrderID)
	if err != nil {
		return onchainSettlementClaim{}, fmt.Errorf("load onchain payment order: %w", err)
	}
	if order.OrderType != payment.OrderTypeBalance || !payment.IsOnchainUSDT(order.PaymentType) {
		return onchainSettlementClaim{}, infraerrors.Conflict("ONCHAIN_ORDER_MISMATCH", "onchain intent is not bound to an onchain USDT balance order")
	}
	total, err := sumFinalizedSettlementDeposits(txCtx, tx.Client(), intent.ID)
	if err != nil {
		return onchainSettlementClaim{}, err
	}
	expected, err := parseSettlementRaw(intent.ExpectedAmountRaw)
	if err != nil || expected.Sign() <= 0 {
		return onchainSettlementClaim{}, fmt.Errorf("invalid expected amount for intent %d", intent.ID)
	}
	if total.Cmp(expected) < 0 {
		return onchainSettlementClaim{}, infraerrors.Conflict("ONCHAIN_UNDERPAID", "onchain payment intent is not fully paid")
	}
	claim.receivedRaw = total.String()

	switch onchain.IntentStatus(intent.Status) {
	case onchain.IntentSettlementDue:
		if intent.SettlementIdempotencyKey != nil && *intent.SettlementIdempotencyKey != key {
			return onchainSettlementClaim{}, infraerrors.Conflict("ONCHAIN_IDEMPOTENCY_CONFLICT", "onchain settlement idempotency key mismatch")
		}
		updated, err := tx.OnchainPaymentIntent.Update().Where(
			onchainpaymentintent.IDEQ(intent.ID),
			onchainpaymentintent.VersionEQ(intent.Version),
			onchainpaymentintent.StatusEQ(string(onchain.IntentSettlementDue)),
		).SetStatus(string(onchain.IntentSettling)).
			SetSettlementIdempotencyKey(key).
			SetReceivedAmountRaw(total.String()).
			AddSettlementAttempts(1).
			ClearNextSettlementAt().
			ClearLastErrorCode().
			ClearLastErrorMessage().
			AddVersion(1).
			Save(txCtx)
		if err != nil {
			return onchainSettlementClaim{}, fmt.Errorf("claim onchain settlement: %w", err)
		}
		if updated != 1 {
			return onchainSettlementClaim{}, infraerrors.Conflict("ONCHAIN_CONCURRENT_SETTLEMENT", "onchain payment intent changed concurrently")
		}
	case onchain.IntentSettling:
		if intent.SettlementIdempotencyKey == nil || *intent.SettlementIdempotencyKey != key {
			return onchainSettlementClaim{}, infraerrors.Conflict("ONCHAIN_IDEMPOTENCY_CONFLICT", "onchain settlement idempotency key mismatch")
		}
	default:
		return onchainSettlementClaim{}, infraerrors.Conflict("ONCHAIN_INVALID_STATUS", "onchain payment intent cannot be settled from status "+intent.Status)
	}

	paidAmount, err := settlementRawToDisplayAmount(total)
	if err != nil {
		return onchainSettlementClaim{}, err
	}
	creditedBalance, err := calculateOnchainCreditedBalance(order.Amount, total, expected)
	if err != nil {
		return onchainSettlementClaim{}, err
	}
	switch order.Status {
	case OrderStatusPending, OrderStatusPartiallyPaid, OrderStatusExpired:
		previousStatus := order.Status
		updated, err := tx.PaymentOrder.Update().Where(
			paymentorder.IDEQ(order.ID),
			paymentorder.StatusEQ(order.Status),
		).SetStatus(OrderStatusPaid).
			SetAmount(creditedBalance).
			SetPayAmount(paidAmount).
			SetPaymentTradeNo(key).
			SetPaidAt(time.Now().UTC()).
			ClearFailedAt().
			ClearFailedReason().
			Save(txCtx)
		if err != nil {
			return onchainSettlementClaim{}, fmt.Errorf("mark onchain payment order paid: %w", err)
		}
		if updated != 1 {
			return onchainSettlementClaim{}, infraerrors.Conflict("ONCHAIN_ORDER_CONCURRENT_UPDATE", "onchain payment order changed concurrently")
		}
		if previousStatus == OrderStatusExpired {
			detail, _ := json.Marshal(map[string]any{
				"previous_status": previousStatus,
				"intent_id":       intent.ID,
				"received_raw":    total.String(),
				"reason":          "late finalized onchain payment reached the expected amount",
			})
			if _, err := tx.PaymentAuditLog.Create().
				SetOrderID(strconv.FormatInt(order.ID, 10)).
				SetAction("ONCHAIN_LATE_PAYMENT_RECOVERED").
				SetDetail(string(detail)).
				SetOperator("system").
				Save(txCtx); err != nil {
				return onchainSettlementClaim{}, fmt.Errorf("write late onchain payment audit: %w", err)
			}
		}
	case OrderStatusPaid, OrderStatusRecharging, OrderStatusFailed, OrderStatusCompleted:
		if order.PaymentTradeNo != "" && order.PaymentTradeNo != key {
			return onchainSettlementClaim{}, infraerrors.Conflict("ONCHAIN_IDEMPOTENCY_CONFLICT", "payment order trade number does not match onchain settlement")
		}
	default:
		return onchainSettlementClaim{}, infraerrors.Conflict("ONCHAIN_ORDER_INVALID_STATUS", "payment order cannot be confirmed from status "+order.Status)
	}
	if err := tx.Commit(); err != nil {
		return onchainSettlementClaim{}, fmt.Errorf("commit onchain settlement claim: %w", err)
	}
	return claim, nil
}

func (s *PaymentService) finalizeOnchainSettlement(ctx context.Context, claim onchainSettlementClaim) error {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin onchain settlement finalization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	intent, err := tx.OnchainPaymentIntent.Get(txCtx, claim.intentID)
	if err != nil {
		return fmt.Errorf("reload onchain payment intent: %w", err)
	}
	if onchain.IntentStatus(intent.Status) == onchain.IntentSettled {
		return tx.Commit()
	}
	if onchain.IntentStatus(intent.Status) != onchain.IntentSettling || intent.SettlementIdempotencyKey == nil || *intent.SettlementIdempotencyKey != claim.idempotencyKey {
		return infraerrors.Conflict("ONCHAIN_SETTLEMENT_LOST", "onchain settlement claim was lost")
	}
	order, err := tx.PaymentOrder.Get(txCtx, claim.orderID)
	if err != nil {
		return fmt.Errorf("reload fulfilled onchain order: %w", err)
	}
	if order.Status != OrderStatusCompleted {
		return infraerrors.Conflict("ONCHAIN_FULFILLMENT_INCOMPLETE", "onchain payment order fulfillment is incomplete")
	}
	total, err := sumFinalizedSettlementDeposits(txCtx, tx.Client(), intent.ID)
	if err != nil {
		return err
	}
	expected, err := parseSettlementRaw(intent.ExpectedAmountRaw)
	if err != nil {
		return fmt.Errorf("parse expected onchain amount: %w", err)
	}
	overpaid := new(big.Int).Sub(new(big.Int).Set(total), expected)
	if overpaid.Sign() < 0 {
		overpaid.SetInt64(0)
	}
	now := time.Now().UTC()
	updated, err := tx.OnchainPaymentIntent.Update().Where(
		onchainpaymentintent.IDEQ(intent.ID),
		onchainpaymentintent.VersionEQ(intent.Version),
		onchainpaymentintent.StatusEQ(string(onchain.IntentSettling)),
		onchainpaymentintent.SettlementIdempotencyKeyEQ(claim.idempotencyKey),
	).SetStatus(string(onchain.IntentSettled)).
		SetReceivedAmountRaw(total.String()).
		SetCreditedAmountRaw(total.String()).
		SetOverpaidAmountRaw(overpaid.String()).
		SetSettledAt(now).
		ClearNextSettlementAt().
		ClearLastErrorCode().
		ClearLastErrorMessage().
		AddVersion(1).
		Save(txCtx)
	if err != nil {
		return fmt.Errorf("finalize onchain payment intent: %w", err)
	}
	if updated != 1 {
		return infraerrors.Conflict("ONCHAIN_SETTLEMENT_LOST", "onchain settlement claim changed concurrently")
	}
	auditRef := fmt.Sprintf("payment_order:%d:recharge:%s", order.ID, order.RechargeCode)
	_, err = tx.OnchainDeposit.Update().Where(
		onchaindeposit.IntentIDEQ(intent.ID),
		onchaindeposit.FinalizedEQ(true),
		onchaindeposit.ReceiptSuccessEQ(true),
		onchaindeposit.StatusIn(string(onchain.DepositConfirmed), string(onchain.DepositCreditPending)),
	).SetStatus(string(onchain.DepositCredited)).
		SetCreditAuditRef(auditRef).
		SetCreditedAt(now).
		AddVersion(1).
		Save(txCtx)
	if err != nil {
		return fmt.Errorf("finalize onchain deposits: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit onchain settlement finalization: %w", err)
	}
	if !s.hasAuditLog(ctx, order.ID, "ONCHAIN_DEPOSIT_SETTLED") {
		s.writeAuditLog(ctx, order.ID, "ONCHAIN_DEPOSIT_SETTLED", "system", map[string]any{
			"intent_id": intent.ID, "network": intent.Network,
			"expected_amount_raw": expected.String(), "received_amount_raw": total.String(),
			"overpaid_amount_raw": overpaid.String(), "credited_balance": order.Amount,
			"fee_rate": order.FeeRate, "settlement_idempotency_key": claim.idempotencyKey,
		})
	}
	return nil
}

func sumFinalizedSettlementDeposits(ctx context.Context, client *dbent.Client, intentID int64) (*big.Int, error) {
	deposits, err := client.OnchainDeposit.Query().Where(
		onchaindeposit.IntentIDEQ(intentID),
		onchaindeposit.FinalizedEQ(true),
		onchaindeposit.ReceiptSuccessEQ(true),
		onchaindeposit.StatusIn(
			string(onchain.DepositConfirmed),
			string(onchain.DepositCreditPending),
			string(onchain.DepositCredited),
		),
	).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("load finalized onchain deposits: %w", err)
	}
	total := new(big.Int)
	for _, deposit := range deposits {
		amount, err := parseSettlementRaw(deposit.AmountRaw)
		if err != nil || amount.Sign() <= 0 {
			return nil, fmt.Errorf("invalid amount on deposit %d", deposit.ID)
		}
		total.Add(total, amount)
	}
	return total, nil
}

func parseSettlementRaw(value string) (*big.Int, error) {
	return onchain.ParseDecimal(value, 0)
}

func settlementRawToDisplayAmount(raw *big.Int) (float64, error) {
	formatted, err := onchain.FormatRaw(raw, onchain.USDTDecimals)
	if err != nil {
		return 0, fmt.Errorf("format onchain payment amount: %w", err)
	}
	amount, err := strconv.ParseFloat(formatted, 64)
	if err != nil {
		return 0, fmt.Errorf("convert onchain payment amount for display: %w", err)
	}
	return amount, nil
}

func calculateOnchainCreditedBalance(expectedCreditedBalance float64, receivedRaw, expectedRaw *big.Int) (float64, error) {
	if expectedCreditedBalance <= 0 || receivedRaw == nil || expectedRaw == nil || expectedRaw.Sign() <= 0 || receivedRaw.Sign() < 0 {
		return 0, fmt.Errorf("invalid onchain credited balance inputs")
	}
	return decimal.NewFromFloat(expectedCreditedBalance).
		Mul(decimal.NewFromBigInt(receivedRaw, 0)).
		Div(decimal.NewFromBigInt(expectedRaw, 0)).
		Round(2).
		InexactFloat64(), nil
}

func onchainSettlementIdempotencyKey(intentID int64) string {
	return fmt.Sprintf("onchain-deposit:intent:%d", intentID)
}

func ClassifyOnchainSettlementError(err error) onchain.SettlementFailureClass {
	reason := infraerrors.Reason(err)
	switch reason {
	case "ONCHAIN_INTENT_NOT_FOUND", "ONCHAIN_ORDER_MISMATCH", "ONCHAIN_UNDERPAID",
		"ONCHAIN_IDEMPOTENCY_CONFLICT", "ONCHAIN_INVALID_STATUS", "ONCHAIN_ORDER_INVALID_STATUS":
		return onchain.SettlementFailureClass{Code: reason, Retryable: false}
	case "":
		return onchain.SettlementFailureClass{Code: "SETTLEMENT_TRANSIENT", Retryable: true}
	default:
		return onchain.SettlementFailureClass{Code: reason, Retryable: true}
	}
}

// RetryOnchainSettlement is the administrator-facing domain entry. It only
// advances an existing retryable settlement; all amount and order checks are
// still performed by ConfirmOnchainDeposit when the worker picks it up.
func (s *PaymentService) RetryOnchainSettlement(ctx context.Context, intentID int64, operator string) error {
	if s == nil || s.entClient == nil || intentID <= 0 {
		return infraerrors.BadRequest("INVALID_ONCHAIN_INTENT", "invalid onchain payment intent")
	}
	if operator == "" {
		operator = "admin"
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin onchain settlement retry: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	intent, err := tx.OnchainPaymentIntent.Get(txCtx, intentID)
	if err != nil {
		return fmt.Errorf("load onchain settlement retry intent: %w", err)
	}
	status := onchain.IntentStatus(intent.Status)
	if status != onchain.IntentSettlementDue && status != onchain.IntentSettling {
		return infraerrors.Conflict("ONCHAIN_RETRY_INVALID_STATUS", "onchain settlement is not retryable from status "+intent.Status)
	}
	updated, err := tx.OnchainPaymentIntent.Update().Where(
		onchainpaymentintent.IDEQ(intent.ID),
		onchainpaymentintent.VersionEQ(intent.Version),
		onchainpaymentintent.StatusEQ(intent.Status),
	).SetStatus(string(onchain.IntentSettlementDue)).
		SetNextSettlementAt(time.Now().UTC()).
		ClearLastErrorCode().
		ClearLastErrorMessage().
		AddVersion(1).
		Save(txCtx)
	if err != nil {
		return fmt.Errorf("schedule immediate onchain settlement retry: %w", err)
	}
	if updated != 1 {
		return infraerrors.Conflict("ONCHAIN_CONCURRENT_SETTLEMENT", "onchain settlement changed concurrently")
	}
	exists, err := tx.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(intent.PaymentOrderID, 10)),
		paymentauditlog.ActionEQ("ONCHAIN_SETTLEMENT_RETRY_REQUESTED"),
	).Exist(txCtx)
	if err != nil {
		return fmt.Errorf("check onchain settlement retry audit: %w", err)
	}
	if !exists {
		detail, _ := json.Marshal(map[string]any{"intent_id": intent.ID, "previous_status": intent.Status})
		if _, err := tx.PaymentAuditLog.Create().
			SetOrderID(strconv.FormatInt(intent.PaymentOrderID, 10)).
			SetAction("ONCHAIN_SETTLEMENT_RETRY_REQUESTED").
			SetDetail(string(detail)).
			SetOperator(operator).
			Save(txCtx); err != nil {
			return fmt.Errorf("write onchain settlement retry audit: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit onchain settlement retry: %w", err)
	}
	return nil
}
