package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/chainscancursor"
	"github.com/Wei-Shaw/sub2api/ent/ethereumgasfunding"
	"github.com/Wei-Shaw/sub2api/ent/ethereumnoncestate"
	"github.com/Wei-Shaw/sub2api/ent/onchaindeposit"
	"github.com/Wei-Shaw/sub2api/ent/onchainpaymentintent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/walletderivationcursor"
	"github.com/Wei-Shaw/sub2api/ent/walletsweep"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrOnchainConflict            = errors.New("onchain persistence conflict")
	ErrOnchainConcurrentUpdate    = errors.New("onchain entity changed concurrently")
	ErrOnchainInvalidTransition   = errors.New("invalid onchain state transition")
	ErrOnchainDerivationExhausted = errors.New("onchain derivation index space exhausted")
)

type OnchainRepository struct {
	client *dbent.Client
}

func NewOnchainRepository(client *dbent.Client) *OnchainRepository {
	return &OnchainRepository{client: client}
}

func (r *OnchainRepository) WithTx(ctx context.Context, fn func(context.Context, *OnchainRepository) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, &OnchainRepository{client: tx.Client()})
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin onchain transaction: %w", err)
	}
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx, &OnchainRepository{client: tx.Client()}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit onchain transaction: %w", err)
	}
	return nil
}

type PaymentIntentCreate struct {
	PaymentOrderID    int64
	UserID            int64
	Network           string
	ChainID           int64
	TokenContract     string
	DepositAddress    string
	DerivationIndex   int64
	ExpectedAmountRaw string
	ConfigSnapshot    map[string]any
	ConfigVersion     string
}

type TRONAddressAllocation struct {
	Network onchain.Network
	Index   uint32
	Address string
}

type EthereumAddressAllocation struct {
	Network onchain.Network
	Index   uint32
	Address string
}

func (r *OnchainRepository) AllocateTRONAddress(ctx context.Context, network onchain.Network, accountXPub string) (TRONAddressAllocation, error) {
	if network != onchain.NetworkTronMainnet && network != onchain.NetworkTronNile {
		return TRONAddressAllocation{}, fmt.Errorf("TRON address allocation does not support network %q", network)
	}
	allocation, err := r.allocateAddress(ctx, network, accountXPub, "TRON", onchain.DeriveTRONAddress)
	if err != nil {
		return TRONAddressAllocation{}, err
	}
	return TRONAddressAllocation(allocation), nil
}

func (r *OnchainRepository) AllocateEthereumAddress(ctx context.Context, network onchain.Network, accountXPub string) (EthereumAddressAllocation, error) {
	if network != onchain.NetworkEthereumMainnet && network != onchain.NetworkEthereumSepolia {
		return EthereumAddressAllocation{}, fmt.Errorf("Ethereum address allocation does not support network %q", network)
	}
	return r.allocateAddress(ctx, network, accountXPub, "Ethereum", onchain.DeriveEthereumAddress)
}

func (r *OnchainRepository) allocateAddress(ctx context.Context, network onchain.Network, accountXPub, chainName string, derive func(string, uint32) (string, error)) (EthereumAddressAllocation, error) {
	var allocation EthereumAddressAllocation
	err := r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		db := txRepo.db(txCtx)
		nextIndex := int64(0)
		lastIntent, err := db.OnchainPaymentIntent.Query().
			Where(onchainpaymentintent.NetworkEQ(string(network))).
			Order(dbent.Desc(onchainpaymentintent.FieldDerivationIndex)).
			First(txCtx)
		if err == nil {
			if lastIntent.DerivationIndex >= int64(hdkeychain.HardenedKeyStart)-1 {
				return ErrOnchainDerivationExhausted
			}
			nextIndex = lastIntent.DerivationIndex + 1
		} else if !dbent.IsNotFound(err) {
			return fmt.Errorf("read latest %s derivation index: %w", chainName, err)
		}

		if err := db.WalletDerivationCursor.Create().
			SetNetwork(string(network)).
			SetNextIndex(nextIndex).
			OnConflictColumns(walletderivationcursor.FieldNetwork).
			DoNothing().
			Exec(txCtx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("initialize %s derivation cursor: %w", chainName, err)
		}

		for {
			updated, err := db.WalletDerivationCursor.Update().
				Where(
					walletderivationcursor.NetworkEQ(string(network)),
					walletderivationcursor.NextIndexLT(int64(hdkeychain.HardenedKeyStart)),
				).
				AddNextIndex(1).
				AddVersion(1).
				Save(txCtx)
			if err != nil {
				return fmt.Errorf("advance %s derivation cursor: %w", chainName, err)
			}
			if updated != 1 {
				return ErrOnchainDerivationExhausted
			}
			cursor, err := db.WalletDerivationCursor.Query().
				Where(walletderivationcursor.NetworkEQ(string(network))).
				Only(txCtx)
			if err != nil {
				return fmt.Errorf("read %s derivation cursor: %w", chainName, err)
			}
			index := cursor.NextIndex - 1
			address, err := derive(accountXPub, uint32(index))
			if err != nil {
				return err
			}
			used, err := db.OnchainPaymentIntent.Query().
				Where(
					onchainpaymentintent.NetworkEQ(string(network)),
					onchainpaymentintent.Or(
						onchainpaymentintent.DerivationIndexEQ(index),
						onchainpaymentintent.DepositAddressEQ(address),
					),
				).
				Exist(txCtx)
			if err != nil {
				return fmt.Errorf("check %s address uniqueness: %w", chainName, err)
			}
			if used {
				continue
			}
			allocation = EthereumAddressAllocation{Network: network, Index: uint32(index), Address: address}
			return nil
		}
	})
	if err != nil {
		return EthereumAddressAllocation{}, err
	}
	return allocation, nil
}

func (r *OnchainRepository) AllocateTRONAddressForOrder(ctx context.Context, network onchain.Network, accountXPub string) (service.TRONAddressAllocation, error) {
	allocation, err := r.AllocateTRONAddress(ctx, network, accountXPub)
	if err != nil {
		return service.TRONAddressAllocation{}, err
	}
	return service.TRONAddressAllocation{
		Network: allocation.Network,
		Index:   allocation.Index,
		Address: allocation.Address,
	}, nil
}

func (r *OnchainRepository) AllocateEthereumAddressForOrder(ctx context.Context, network onchain.Network, accountXPub string) (service.EthereumAddressAllocation, error) {
	allocation, err := r.AllocateEthereumAddress(ctx, network, accountXPub)
	if err != nil {
		return service.EthereumAddressAllocation{}, err
	}
	return service.EthereumAddressAllocation{
		Network: allocation.Network, Index: allocation.Index, Address: allocation.Address,
	}, nil
}

func (r *OnchainRepository) CreatePaymentIntent(ctx context.Context, input PaymentIntentCreate) (*dbent.OnchainPaymentIntent, error) {
	entity, err := r.db(ctx).OnchainPaymentIntent.Create().
		SetPaymentOrderID(input.PaymentOrderID).
		SetUserID(input.UserID).
		SetNetwork(input.Network).
		SetChainID(input.ChainID).
		SetTokenContract(input.TokenContract).
		SetDepositAddress(input.DepositAddress).
		SetDerivationIndex(input.DerivationIndex).
		SetExpectedAmountRaw(input.ExpectedAmountRaw).
		SetConfigSnapshot(input.ConfigSnapshot).
		SetConfigVersion(input.ConfigVersion).
		Save(ctx)
	return entity, translateOnchainWriteError(err)
}

func (r *OnchainRepository) CreateOnchainPaymentIntent(ctx context.Context, input service.OnchainPaymentIntentCreate) error {
	_, err := r.CreatePaymentIntent(ctx, PaymentIntentCreate{
		PaymentOrderID: input.PaymentOrderID, UserID: input.UserID, Network: input.Network,
		ChainID: input.ChainID, TokenContract: input.TokenContract, DepositAddress: input.DepositAddress,
		DerivationIndex: input.DerivationIndex, ExpectedAmountRaw: input.ExpectedAmountRaw,
		ConfigSnapshot: input.ConfigSnapshot, ConfigVersion: input.ConfigVersion,
	})
	return err
}

type DepositCreate struct {
	IntentID         int64
	PaymentOrderID   int64
	UserID           int64
	Network          string
	ChainID          int64
	TransactionID    string
	LogIndex         int64
	TransactionIndex int64
	BlockHeight      int64
	BlockHash        string
	TokenContract    string
	FromAddress      string
	ToAddress        string
	AmountRaw        string
	TransactionTime  time.Time
}

func (r *OnchainRepository) CreateDeposit(ctx context.Context, input DepositCreate) (*dbent.OnchainDeposit, error) {
	entity, err := buildDepositCreate(r.db(ctx), input).Save(ctx)
	return entity, translateOnchainWriteError(err)
}

func (r *OnchainRepository) EnsureDeposit(ctx context.Context, input DepositCreate) error {
	db := r.db(ctx)
	err := buildDepositCreate(db, input).
		OnConflictColumns(
			onchaindeposit.FieldNetwork,
			onchaindeposit.FieldTransactionID,
			onchaindeposit.FieldLogIndex,
		).
		DoNothing().
		Exec(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return translateOnchainWriteError(err)
	}
	stored, err := db.OnchainDeposit.Query().
		Where(
			onchaindeposit.NetworkEQ(input.Network),
			onchaindeposit.TransactionIDEQ(input.TransactionID),
			onchaindeposit.LogIndexEQ(input.LogIndex),
		).
		Only(ctx)
	if err != nil {
		return err
	}
	if !depositMatchesInput(stored, input) {
		return fmt.Errorf("%w: onchain event payload mismatch", ErrOnchainConflict)
	}
	return nil
}

func (r *OnchainRepository) ListSettlementCandidates(ctx context.Context, network onchain.Network, limit int) ([]int64, error) {
	if strings.TrimSpace(string(network)) == "" || limit <= 0 {
		return nil, fmt.Errorf("invalid settlement candidate query")
	}
	cursor, cursorErr := r.db(ctx).ChainScanCursor.Query().Where(chainscancursor.NetworkEQ(string(network))).Only(ctx)
	if cursorErr != nil && !dbent.IsNotFound(cursorErr) {
		return nil, fmt.Errorf("load settlement network cursor: %w", cursorErr)
	}
	if cursorErr == nil && onchain.CursorHealth(cursor.Health) == onchain.CursorHashConflict {
		if network == onchain.NetworkEthereumMainnet || network == onchain.NetworkEthereumSepolia {
			return nil, onchain.ErrEthereumScanHashConflict
		}
		return nil, onchain.ErrTRONScanHashConflict
	}
	intents, err := r.db(ctx).OnchainPaymentIntent.Query().
		Where(
			onchainpaymentintent.NetworkEQ(string(network)),
			onchainpaymentintent.StatusIn(
				string(onchain.IntentPending),
				string(onchain.IntentPartiallyPaid),
				string(onchain.IntentSettlementDue),
				string(onchain.IntentSettled),
				string(onchain.IntentReviewRequired),
			),
			onchainpaymentintent.Or(
				onchainpaymentintent.And(
					onchainpaymentintent.StatusEQ(string(onchain.IntentSettlementDue)),
					onchainpaymentintent.Or(
						onchainpaymentintent.NextSettlementAtIsNil(),
						onchainpaymentintent.NextSettlementAtLTE(time.Now().UTC()),
					),
				),
				onchainpaymentintent.HasDepositsWith(
					onchaindeposit.FinalizedEQ(true),
					onchaindeposit.ReceiptSuccessEQ(true),
					onchaindeposit.StatusEQ(string(onchain.DepositConfirmed)),
				),
				onchainpaymentintent.And(
					onchainpaymentintent.StatusEQ(string(onchain.IntentPartiallyPaid)),
					onchainpaymentintent.HasPaymentOrderWith(paymentorder.StatusEQ(payment.OrderStatusExpired)),
				),
			),
		).
		Order(dbent.Asc(onchainpaymentintent.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(intents))
	for _, intent := range intents {
		ids = append(ids, intent.ID)
	}
	return ids, nil
}

func (r *OnchainRepository) PrepareIntentSettlement(ctx context.Context, intentID int64) (onchain.SettlementPreparation, error) {
	if intentID <= 0 {
		return onchain.SettlementPreparation{}, fmt.Errorf("invalid settlement intent id")
	}
	var result onchain.SettlementPreparation
	err := r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		db := txRepo.db(txCtx)
		intent, err := db.OnchainPaymentIntent.Query().Where(onchainpaymentintent.IDEQ(intentID)).Only(txCtx)
		if err != nil {
			return err
		}
		currentStatus := onchain.IntentStatus(intent.Status)
		if currentStatus != onchain.IntentPending && currentStatus != onchain.IntentPartiallyPaid &&
			currentStatus != onchain.IntentSettlementDue && currentStatus != onchain.IntentSettled &&
			currentStatus != onchain.IntentReviewRequired {
			return nil
		}
		order, err := db.PaymentOrder.Get(txCtx, intent.PaymentOrderID)
		if err != nil {
			return err
		}

		deposits, err := db.OnchainDeposit.Query().Where(
			onchaindeposit.IntentIDEQ(intentID),
			onchaindeposit.FinalizedEQ(true),
			onchaindeposit.ReceiptSuccessEQ(true),
			onchaindeposit.StatusIn(
				string(onchain.DepositConfirmed),
				string(onchain.DepositCreditPending),
				string(onchain.DepositCredited),
				string(onchain.DepositReviewRequired),
			),
		).All(txCtx)
		if err != nil {
			return err
		}
		total := new(big.Int)
		confirmedCount := 0
		for _, deposit := range deposits {
			amount, err := parseOnchainRawAmount(deposit.AmountRaw)
			if err != nil {
				return fmt.Errorf("invalid amount on deposit %d: %w", deposit.ID, err)
			}
			if amount.Sign() <= 0 {
				return fmt.Errorf("invalid amount on deposit %d: amount must be positive", deposit.ID)
			}
			total.Add(total, amount)
			if onchain.DepositStatus(deposit.Status) == onchain.DepositConfirmed {
				confirmedCount++
			}
		}
		expiredUnderpaymentReview := currentStatus == onchain.IntentPartiallyPaid && order.Status == payment.OrderStatusExpired
		retryDue := currentStatus == onchain.IntentSettlementDue
		if confirmedCount == 0 && !expiredUnderpaymentReview && !retryDue {
			return nil
		}
		expected, err := parseOnchainRawAmount(intent.ExpectedAmountRaw)
		if err != nil {
			return fmt.Errorf("invalid expected amount on intent %d: %w", intent.ID, err)
		}
		if expected.Sign() <= 0 {
			return fmt.Errorf("invalid expected amount on intent %d: amount must be positive", intent.ID)
		}
		if retryDue {
			if total.Cmp(expected) < 0 {
				return fmt.Errorf("settlement due intent %d is underpaid", intent.ID)
			}
			result = onchain.SettlementPreparation{
				IntentID: intent.ID, Status: onchain.IntentSettlementDue,
				ExpectedAmountRaw: expected.String(), ReceivedAmountRaw: total.String(),
				PendingAmountRaw: "0", Ready: true, SettlementAttempts: intent.SettlementAttempts,
			}
			return nil
		}

		pending := new(big.Int)
		reviewRequired := currentStatus == onchain.IntentSettled || currentStatus == onchain.IntentReviewRequired
		nextStatus := onchain.IntentSettlementDue
		ready := total.Cmp(expected) >= 0
		if !ready {
			pending.Sub(expected, total)
			if order.Status == payment.OrderStatusExpired || order.Status == payment.OrderStatusReviewRequired {
				reviewRequired = true
			} else {
				nextStatus = onchain.IntentPartiallyPaid
			}
		}
		if reviewRequired {
			nextStatus = onchain.IntentReviewRequired
			ready = false
		}
		updated, err := db.OnchainPaymentIntent.Update().Where(
			onchainpaymentintent.IDEQ(intent.ID),
			onchainpaymentintent.VersionEQ(intent.Version),
			onchainpaymentintent.StatusEQ(intent.Status),
		).SetReceivedAmountRaw(total.String()).SetStatus(string(nextStatus)).AddVersion(1).Save(txCtx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return ErrOnchainConcurrentUpdate
		}
		depositTargetStatus := onchain.DepositCreditPending
		depositSourceStatuses := []string{string(onchain.DepositConfirmed)}
		expectedDepositUpdates := confirmedCount
		if reviewRequired {
			depositTargetStatus = onchain.DepositReviewRequired
			depositSourceStatuses = append(depositSourceStatuses, string(onchain.DepositCreditPending))
			expectedDepositUpdates = 0
			for _, deposit := range deposits {
				status := onchain.DepositStatus(deposit.Status)
				if status == onchain.DepositConfirmed || status == onchain.DepositCreditPending {
					expectedDepositUpdates++
				}
			}
		}
		updatedDeposits, err := db.OnchainDeposit.Update().Where(
			onchaindeposit.IntentIDEQ(intent.ID),
			onchaindeposit.FinalizedEQ(true),
			onchaindeposit.ReceiptSuccessEQ(true),
			onchaindeposit.StatusIn(depositSourceStatuses...),
		).SetStatus(string(depositTargetStatus)).AddVersion(1).Save(txCtx)
		if err != nil {
			return err
		}
		if updatedDeposits != expectedDepositUpdates {
			return ErrOnchainConcurrentUpdate
		}
		if reviewRequired && order.Status == payment.OrderStatusExpired {
			_, err = db.PaymentOrder.Update().Where(
				paymentorder.IDEQ(intent.PaymentOrderID),
				paymentorder.StatusEQ(payment.OrderStatusExpired),
			).SetStatus(payment.OrderStatusReviewRequired).Save(txCtx)
			if err != nil {
				return err
			}
		} else if !ready && !reviewRequired {
			_, err = db.PaymentOrder.Update().Where(
				paymentorder.IDEQ(intent.PaymentOrderID),
				paymentorder.StatusEQ(payment.OrderStatusPending),
			).SetStatus(payment.OrderStatusPartiallyPaid).Save(txCtx)
			if err != nil {
				return err
			}
		}
		result = onchain.SettlementPreparation{
			IntentID: intent.ID, Status: nextStatus,
			ExpectedAmountRaw: expected.String(), ReceivedAmountRaw: total.String(),
			PendingAmountRaw: pending.String(), Ready: ready, ReviewRequired: reviewRequired,
			SettlementAttempts: intent.SettlementAttempts,
		}
		return nil
	})
	return result, err
}

func (r *OnchainRepository) RecordSettlementFailure(ctx context.Context, failure onchain.SettlementFailure) error {
	if failure.IntentID <= 0 || strings.TrimSpace(failure.Code) == "" {
		return fmt.Errorf("invalid settlement failure")
	}
	message := strings.TrimSpace(failure.Message)
	if len(message) > 2048 {
		message = message[:2048]
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		db := txRepo.db(txCtx)
		intent, err := db.OnchainPaymentIntent.Get(txCtx, failure.IntentID)
		if err != nil {
			return err
		}
		status := onchain.IntentStatus(intent.Status)
		if status == onchain.IntentSettled || status == onchain.IntentReviewRequired {
			return nil
		}
		if status != onchain.IntentSettling && status != onchain.IntentSettlementDue {
			return ErrOnchainConcurrentUpdate
		}
		update := db.OnchainPaymentIntent.Update().Where(
			onchainpaymentintent.IDEQ(intent.ID),
			onchainpaymentintent.VersionEQ(intent.Version),
			onchainpaymentintent.StatusEQ(intent.Status),
		).SetLastErrorCode(failure.Code).SetLastErrorMessage(message).AddVersion(1)
		if failure.Retryable {
			if failure.NextRetryAt.IsZero() {
				return fmt.Errorf("retryable settlement failure requires next retry time")
			}
			update.SetStatus(string(onchain.IntentSettlementDue)).SetNextSettlementAt(failure.NextRetryAt.UTC())
		} else {
			update.SetStatus(string(onchain.IntentReviewRequired)).ClearNextSettlementAt()
		}
		updated, err := update.Save(txCtx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return ErrOnchainConcurrentUpdate
		}
		if failure.Retryable {
			return nil
		}
		_, err = db.OnchainDeposit.Update().Where(
			onchaindeposit.IntentIDEQ(intent.ID),
			onchaindeposit.StatusIn(string(onchain.DepositConfirmed), string(onchain.DepositCreditPending)),
		).SetStatus(string(onchain.DepositReviewRequired)).AddVersion(1).Save(txCtx)
		if err != nil {
			return err
		}
		_, err = db.PaymentOrder.Update().Where(
			paymentorder.IDEQ(intent.PaymentOrderID),
			paymentorder.StatusNotIn(payment.OrderStatusCompleted, payment.OrderStatusRefunded),
		).SetStatus(payment.OrderStatusReviewRequired).Save(txCtx)
		return err
	})
}

func parseOnchainRawAmount(value string) (*big.Int, error) {
	amount, err := onchain.ParseDecimal(value, 0)
	if err != nil {
		return nil, err
	}
	return amount, nil
}

func buildDepositCreate(db *dbent.Client, input DepositCreate) *dbent.OnchainDepositCreate {
	return db.OnchainDeposit.Create().
		SetIntentID(input.IntentID).
		SetPaymentOrderID(input.PaymentOrderID).
		SetUserID(input.UserID).
		SetNetwork(input.Network).
		SetChainID(input.ChainID).
		SetTransactionID(input.TransactionID).
		SetLogIndex(input.LogIndex).
		SetTransactionIndex(input.TransactionIndex).
		SetBlockHeight(input.BlockHeight).
		SetBlockHash(input.BlockHash).
		SetTokenContract(input.TokenContract).
		SetFromAddress(input.FromAddress).
		SetToAddress(input.ToAddress).
		SetAmountRaw(input.AmountRaw).
		SetTransactionTime(input.TransactionTime).
		SetReceiptSuccess(true).
		SetFinalized(true)
}

func depositMatchesInput(stored *dbent.OnchainDeposit, input DepositCreate) bool {
	return stored != nil &&
		stored.IntentID == input.IntentID &&
		stored.PaymentOrderID == input.PaymentOrderID &&
		stored.UserID == input.UserID &&
		stored.Network == input.Network &&
		stored.ChainID == input.ChainID &&
		stored.TransactionID == input.TransactionID &&
		stored.LogIndex == input.LogIndex &&
		stored.TransactionIndex == input.TransactionIndex &&
		stored.BlockHeight == input.BlockHeight &&
		stored.BlockHash == input.BlockHash &&
		stored.TokenContract == input.TokenContract &&
		stored.FromAddress == input.FromAddress &&
		stored.ToAddress == input.ToAddress &&
		stored.AmountRaw == input.AmountRaw &&
		stored.TransactionTime.Equal(input.TransactionTime) &&
		stored.ReceiptSuccess &&
		stored.Finalized
}

func (r *OnchainRepository) ListTRONBalanceReconciliationTargets(ctx context.Context, network onchain.Network, afterIntentID int64, limit int) ([]onchain.TRONBalanceReconciliationTarget, error) {
	if (network != onchain.NetworkTronMainnet && network != onchain.NetworkTronNile) || afterIntentID < 0 || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid TRON balance reconciliation query")
	}
	intents, err := r.db(ctx).OnchainPaymentIntent.Query().
		Where(
			onchainpaymentintent.NetworkEQ(string(network)),
			onchainpaymentintent.IDGT(afterIntentID),
		).
		WithDeposits(func(query *dbent.OnchainDepositQuery) {
			query.Where(onchaindeposit.FinalizedEQ(true), onchaindeposit.ReceiptSuccessEQ(true))
		}).
		WithWalletSweeps().
		Order(dbent.Asc(onchainpaymentintent.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query TRON reconciliation intents: %w", err)
	}
	result := make([]onchain.TRONBalanceReconciliationTarget, 0, len(intents))
	for _, intent := range intents {
		recorded := new(big.Int)
		for _, deposit := range intent.Edges.Deposits {
			amount, parseErr := parseOnchainRawAmount(deposit.AmountRaw)
			if parseErr != nil {
				return nil, fmt.Errorf("invalid deposit amount on intent %d: %w", intent.ID, parseErr)
			}
			if amount.Sign() < 0 {
				return nil, fmt.Errorf("invalid negative deposit amount on intent %d", intent.ID)
			}
			recorded.Add(recorded, amount)
		}
		finalizedSwept := new(big.Int)
		hasInFlightSweep := false
		for _, sweep := range intent.Edges.WalletSweeps {
			switch onchain.TransferStatus(sweep.Status) {
			case onchain.TransferFinalized:
				amount, parseErr := parseOnchainRawAmount(sweep.AmountRaw)
				if parseErr != nil {
					return nil, fmt.Errorf("invalid finalized sweep amount on intent %d: %w", intent.ID, parseErr)
				}
				if amount.Sign() < 0 {
					return nil, fmt.Errorf("invalid negative finalized sweep amount on intent %d", intent.ID)
				}
				finalizedSwept.Add(finalizedSwept, amount)
			case onchain.TransferSigning, onchain.TransferSigned, onchain.TransferBroadcast, onchain.TransferConfirming:
				hasInFlightSweep = true
			}
		}
		result = append(result, onchain.TRONBalanceReconciliationTarget{
			IntentID: intent.ID, Network: network, TokenContract: intent.TokenContract,
			Address: intent.DepositAddress, IntentStatus: onchain.IntentStatus(intent.Status),
			RecordedDepositsRaw: recorded.String(), CreditedAmountRaw: intent.CreditedAmountRaw,
			FinalizedSweptRaw: finalizedSwept.String(), HasInFlightSweep: hasInFlightSweep,
		})
	}
	return result, nil
}

func (r *OnchainRepository) MarkTRONBalanceReconciliationReview(ctx context.Context, mismatch onchain.TRONBalanceReconciliationMismatch) error {
	if mismatch.IntentID <= 0 || strings.TrimSpace(mismatch.ReasonCode) == "" || strings.TrimSpace(mismatch.Reason) == "" {
		return fmt.Errorf("invalid TRON balance reconciliation mismatch")
	}
	message := strings.TrimSpace(mismatch.Reason)
	if len(message) > 2048 {
		message = message[:2048]
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		db := txRepo.db(txCtx)
		intent, err := db.OnchainPaymentIntent.Get(txCtx, mismatch.IntentID)
		if err != nil {
			return err
		}
		status := onchain.IntentStatus(intent.Status)
		if status == onchain.IntentReviewRequired && intent.LastErrorCode != nil && *intent.LastErrorCode == onchain.TRONBalanceReconciliationErrorCode &&
			intent.LastErrorMessage != nil && *intent.LastErrorMessage == message {
			return nil
		}
		update := db.OnchainPaymentIntent.Update().Where(
			onchainpaymentintent.IDEQ(intent.ID),
			onchainpaymentintent.VersionEQ(intent.Version),
			onchainpaymentintent.StatusEQ(intent.Status),
		).SetLastErrorCode(onchain.TRONBalanceReconciliationErrorCode).SetLastErrorMessage(message).ClearNextSettlementAt().AddVersion(1)
		if status != onchain.IntentReviewRequired && status != onchain.IntentDisabled {
			if !onchain.CanTransitionIntent(status, onchain.IntentReviewRequired) {
				return invalidTransition(status, onchain.IntentReviewRequired)
			}
			update.SetStatus(string(onchain.IntentReviewRequired))
		}
		updated, err := update.Save(txCtx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return ErrOnchainConcurrentUpdate
		}

		_, err = db.OnchainDeposit.Update().Where(
			onchaindeposit.IntentIDEQ(intent.ID),
			onchaindeposit.FinalizedEQ(true),
			onchaindeposit.ReceiptSuccessEQ(true),
			onchaindeposit.StatusIn(
				string(onchain.DepositConfirmed),
				string(onchain.DepositCreditPending),
				string(onchain.DepositCredited),
			),
		).SetStatus(string(onchain.DepositReviewRequired)).SetValidationError(message).AddVersion(1).Save(txCtx)
		if err != nil {
			return err
		}
		_, err = db.PaymentOrder.Update().Where(
			paymentorder.IDEQ(intent.PaymentOrderID),
			paymentorder.StatusNotIn(payment.OrderStatusCompleted, payment.OrderStatusRefunded, payment.OrderStatusReviewRequired),
		).SetStatus(payment.OrderStatusReviewRequired).Save(txCtx)
		return err
	})
}

type ChainScanCursorCreate struct {
	Network string
	ChainID int64
}

func (r *OnchainRepository) CreateChainScanCursor(ctx context.Context, input ChainScanCursorCreate) (*dbent.ChainScanCursor, error) {
	entity, err := r.db(ctx).ChainScanCursor.Create().
		SetNetwork(input.Network).
		SetChainID(input.ChainID).
		Save(ctx)
	return entity, translateOnchainWriteError(err)
}

func (r *OnchainRepository) LoadTRONScanCursor(ctx context.Context, network onchain.Network) (onchain.TRONScanCursor, error) {
	cursor, err := r.db(ctx).ChainScanCursor.Query().
		Where(chainscancursor.NetworkEQ(string(network))).
		Only(ctx)
	if dbent.IsNotFound(err) {
		return onchain.TRONScanCursor{}, onchain.ErrTRONScanCursorNotFound
	}
	if err != nil {
		return onchain.TRONScanCursor{}, err
	}
	result := onchain.TRONScanCursor{
		FinalizedHeight: cursor.FinalizedHeight,
		FinalizedHash:   cursor.FinalizedHash,
		Health:          onchain.CursorHealth(cursor.Health),
	}
	if cursor.LeaseOwner != nil {
		result.LeaseOwner = *cursor.LeaseOwner
	}
	if cursor.LeaseUntil != nil {
		result.LeaseUntil = *cursor.LeaseUntil
	}
	return result, nil
}

func (r *OnchainRepository) InitializeTRONScanCursor(ctx context.Context, input onchain.TRONScanCursorInitialization) error {
	if (input.Network != onchain.NetworkTronMainnet && input.Network != onchain.NetworkTronNile) ||
		input.ChainID < 0 || input.FinalizedHeight < 0 || strings.TrimSpace(input.FinalizedHash) == "" || input.InitializedAt.IsZero() {
		return fmt.Errorf("invalid TRON scan cursor initialization")
	}
	exists, err := r.db(ctx).ChainScanCursor.Query().Where(chainscancursor.NetworkEQ(string(input.Network))).Exist(ctx)
	if err != nil {
		return fmt.Errorf("check TRON scan cursor: %w", err)
	}
	if exists {
		return nil
	}
	_, err = r.db(ctx).ChainScanCursor.Create().
		SetNetwork(string(input.Network)).
		SetChainID(input.ChainID).
		SetFinalizedHeight(input.FinalizedHeight).
		SetFinalizedHash(strings.TrimSpace(input.FinalizedHash)).
		SetHealth(string(onchain.CursorHealthy)).
		SetLastSuccessAt(input.InitializedAt.UTC()).
		Save(ctx)
	if isUniqueConstraintViolation(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("initialize TRON scan cursor: %w", err)
	}
	return nil
}

func (r *OnchainRepository) FindTRONPaymentIntentByAddress(ctx context.Context, network onchain.Network, address string) (onchain.TRONPaymentIntentReference, bool, error) {
	intent, err := r.db(ctx).OnchainPaymentIntent.Query().
		Where(
			onchainpaymentintent.NetworkEQ(string(network)),
			onchainpaymentintent.DepositAddressEQ(address),
		).
		Only(ctx)
	if dbent.IsNotFound(err) {
		return onchain.TRONPaymentIntentReference{}, false, nil
	}
	if err != nil {
		return onchain.TRONPaymentIntentReference{}, false, err
	}
	return onchain.TRONPaymentIntentReference{
		ID:             intent.ID,
		PaymentOrderID: intent.PaymentOrderID,
		UserID:         intent.UserID,
		Network:        onchain.Network(intent.Network),
		ChainID:        intent.ChainID,
		TokenContract:  intent.TokenContract,
		DepositAddress: intent.DepositAddress,
	}, true, nil
}

func (r *OnchainRepository) CommitTRONScanBlock(ctx context.Context, input onchain.TRONScanBlockCommit) error {
	if strings.TrimSpace(string(input.Network)) == "" || strings.TrimSpace(input.LeaseOwner) == "" || input.Now.IsZero() {
		return fmt.Errorf("invalid TRON scan block commit identity")
	}
	if input.PreviousHeight < 0 || input.Block.Height != input.PreviousHeight+1 || input.Block.Hash == "" {
		return fmt.Errorf("%w: invalid block sequence", onchain.ErrTRONScanCursorConflict)
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		cursor, err := txRepo.db(txCtx).ChainScanCursor.Query().
			Where(chainscancursor.NetworkEQ(string(input.Network))).
			Only(txCtx)
		if err != nil {
			return err
		}
		if cursor.LeaseOwner == nil || *cursor.LeaseOwner != input.LeaseOwner || cursor.LeaseUntil == nil || !cursor.LeaseUntil.After(input.Now) {
			return onchain.ErrTRONScanLeaseLost
		}
		if onchain.CursorHealth(cursor.Health) == onchain.CursorHashConflict {
			return onchain.ErrTRONScanHashConflict
		}
		if cursor.FinalizedHeight != input.PreviousHeight {
			return onchain.ErrTRONScanCursorConflict
		}
		for _, deposit := range input.Deposits {
			if deposit.Intent.Network != input.Network || deposit.Transfer.BlockHeight != input.Block.Height || !deposit.Transfer.BlockTimestamp.Equal(input.Block.Timestamp) {
				return fmt.Errorf("TRON scan deposit does not belong to committed block")
			}
			intent, err := txRepo.db(txCtx).OnchainPaymentIntent.Query().
				Where(
					onchainpaymentintent.IDEQ(deposit.Intent.ID),
					onchainpaymentintent.NetworkEQ(string(input.Network)),
					onchainpaymentintent.DepositAddressEQ(deposit.Transfer.ToAddress),
				).
				Only(txCtx)
			if err != nil {
				return fmt.Errorf("verify TRON payment intent binding: %w", err)
			}
			if intent.TokenContract != deposit.Transfer.ContractAddress {
				return fmt.Errorf("TRON payment intent contract does not match deposit")
			}
			if err := txRepo.EnsureDeposit(txCtx, DepositCreate{
				IntentID:         intent.ID,
				PaymentOrderID:   intent.PaymentOrderID,
				UserID:           intent.UserID,
				Network:          string(input.Network),
				ChainID:          intent.ChainID,
				TransactionID:    deposit.Transfer.TransactionID,
				LogIndex:         int64(deposit.Transfer.LogIndex),
				TransactionIndex: int64(deposit.TransactionIndex),
				BlockHeight:      input.Block.Height,
				BlockHash:        input.Block.Hash,
				TokenContract:    deposit.Transfer.ContractAddress,
				FromAddress:      deposit.Transfer.FromAddress,
				ToAddress:        deposit.Transfer.ToAddress,
				AmountRaw:        deposit.Transfer.AmountRaw,
				TransactionTime:  deposit.Transfer.BlockTimestamp,
			}); err != nil {
				return fmt.Errorf("create TRON deposit: %w", err)
			}
		}
		updated, err := txRepo.db(txCtx).ChainScanCursor.Update().
			Where(
				chainscancursor.IDEQ(cursor.ID),
				chainscancursor.VersionEQ(cursor.Version),
				chainscancursor.FinalizedHeightEQ(input.PreviousHeight),
				chainscancursor.LeaseOwnerEQ(input.LeaseOwner),
				chainscancursor.LeaseUntilGT(input.Now),
			).
			SetFinalizedHeight(input.Block.Height).
			SetFinalizedHash(input.Block.Hash).
			SetHealth(string(onchain.CursorHealthy)).
			SetLastSuccessAt(input.Now).
			ClearLastErrorCode().
			ClearLastErrorMessage().
			AddVersion(1).
			Save(txCtx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return onchain.ErrTRONScanCursorConflict
		}
		return nil
	})
}

func (r *OnchainRepository) ReconcileTRONScanBlock(ctx context.Context, input onchain.TRONScanBlockReconcile) error {
	if strings.TrimSpace(string(input.Network)) == "" || strings.TrimSpace(input.LeaseOwner) == "" || input.Now.IsZero() || input.Block.Height < 1 || input.Block.Hash == "" {
		return fmt.Errorf("invalid TRON scan block reconciliation")
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		cursor, err := txRepo.db(txCtx).ChainScanCursor.Query().
			Where(chainscancursor.NetworkEQ(string(input.Network))).
			Only(txCtx)
		if err != nil {
			return err
		}
		if cursor.LeaseOwner == nil || *cursor.LeaseOwner != input.LeaseOwner || cursor.LeaseUntil == nil || !cursor.LeaseUntil.After(input.Now) {
			return onchain.ErrTRONScanLeaseLost
		}
		if onchain.CursorHealth(cursor.Health) == onchain.CursorHashConflict {
			return onchain.ErrTRONScanHashConflict
		}
		if input.Block.Height > cursor.FinalizedHeight {
			return onchain.ErrTRONScanCursorConflict
		}

		expected := make(map[onchainEventKey]DepositCreate, len(input.Deposits))
		for _, deposit := range input.Deposits {
			create, err := txRepo.resolveTRONDepositCreate(txCtx, input.Network, input.Block, deposit)
			if err != nil {
				return err
			}
			key := onchainEventKey{transactionID: create.TransactionID, logIndex: create.LogIndex}
			if previous, exists := expected[key]; exists && !depositCreateEqual(previous, create) {
				return onchain.ErrTRONScanHashConflict
			}
			expected[key] = create
		}
		stored, err := txRepo.db(txCtx).OnchainDeposit.Query().
			Where(
				onchaindeposit.NetworkEQ(string(input.Network)),
				onchaindeposit.BlockHeightEQ(input.Block.Height),
			).
			All(txCtx)
		if err != nil {
			return err
		}
		for _, existing := range stored {
			key := onchainEventKey{transactionID: existing.TransactionID, logIndex: existing.LogIndex}
			candidate, exists := expected[key]
			if !exists || existing.BlockHash != input.Block.Hash || !depositMatchesInput(existing, candidate) {
				return onchain.ErrTRONScanHashConflict
			}
		}
		for _, candidate := range expected {
			if err := txRepo.EnsureDeposit(txCtx, candidate); err != nil {
				if errors.Is(err, ErrOnchainConflict) {
					return onchain.ErrTRONScanHashConflict
				}
				return err
			}
		}
		return nil
	})
}

func (r *OnchainRepository) MarkTRONScanHashConflict(ctx context.Context, network onchain.Network, leaseOwner string, now time.Time, cause error) error {
	if strings.TrimSpace(string(network)) == "" || strings.TrimSpace(leaseOwner) == "" || now.IsZero() {
		return fmt.Errorf("invalid TRON hash conflict identity")
	}
	message := "solidified block hash or event set conflict"
	if cause != nil {
		message = cause.Error()
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	updated, err := r.db(ctx).ChainScanCursor.Update().
		Where(
			chainscancursor.NetworkEQ(string(network)),
			chainscancursor.LeaseOwnerEQ(leaseOwner),
			chainscancursor.LeaseUntilGT(now),
		).
		SetHealth(string(onchain.CursorHashConflict)).
		SetLastErrorCode("SOLIDIFIED_HASH_CONFLICT").
		SetLastErrorMessage(message).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		return err
	}
	if updated != 1 {
		return onchain.ErrTRONScanLeaseLost
	}
	return nil
}

func (r *OnchainRepository) LoadEthereumScanCursor(ctx context.Context, network onchain.Network) (onchain.EthereumScanCursor, error) {
	cursor, err := r.db(ctx).ChainScanCursor.Query().Where(chainscancursor.NetworkEQ(string(network))).Only(ctx)
	if dbent.IsNotFound(err) {
		return onchain.EthereumScanCursor{}, onchain.ErrEthereumScanCursorNotFound
	}
	if err != nil {
		return onchain.EthereumScanCursor{}, err
	}
	if cursor.FinalizedHeight < 0 {
		return onchain.EthereumScanCursor{}, onchain.ErrEthereumScanCursorConflict
	}
	result := onchain.EthereumScanCursor{
		FinalizedHeight: uint64(cursor.FinalizedHeight), FinalizedHash: cursor.FinalizedHash,
		Health: onchain.CursorHealth(cursor.Health),
	}
	if cursor.LeaseOwner != nil {
		result.LeaseOwner = *cursor.LeaseOwner
	}
	if cursor.LeaseUntil != nil {
		result.LeaseUntil = *cursor.LeaseUntil
	}
	return result, nil
}

func (r *OnchainRepository) InitializeEthereumScanCursor(ctx context.Context, input onchain.EthereumScanCursorInitialization) error {
	const maxInt64 = uint64(1<<63 - 1)
	if (input.Network != onchain.NetworkEthereumMainnet && input.Network != onchain.NetworkEthereumSepolia) ||
		input.ChainID <= 0 || input.FinalizedHeight > maxInt64 || !common.IsHexHash(input.FinalizedHash) || input.InitializedAt.IsZero() {
		return fmt.Errorf("invalid Ethereum scan cursor initialization")
	}
	exists, err := r.db(ctx).ChainScanCursor.Query().Where(chainscancursor.NetworkEQ(string(input.Network))).Exist(ctx)
	if err != nil {
		return fmt.Errorf("check Ethereum scan cursor: %w", err)
	}
	if exists {
		return nil
	}
	_, err = r.db(ctx).ChainScanCursor.Create().SetNetwork(string(input.Network)).SetChainID(input.ChainID).
		SetFinalizedHeight(int64(input.FinalizedHeight)).SetFinalizedHash(input.FinalizedHash).
		SetHealth(string(onchain.CursorHealthy)).SetLastSuccessAt(input.InitializedAt.UTC()).Save(ctx)
	if isUniqueConstraintViolation(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("initialize Ethereum scan cursor: %w", err)
	}
	return nil
}

func (r *OnchainRepository) FindEthereumPaymentIntentByAddress(ctx context.Context, network onchain.Network, address string) (onchain.EthereumPaymentIntentReference, bool, error) {
	intent, err := r.db(ctx).OnchainPaymentIntent.Query().Where(
		onchainpaymentintent.NetworkEQ(string(network)),
		onchainpaymentintent.DepositAddressEQ(address),
	).Only(ctx)
	if dbent.IsNotFound(err) {
		return onchain.EthereumPaymentIntentReference{}, false, nil
	}
	if err != nil {
		return onchain.EthereumPaymentIntentReference{}, false, err
	}
	return onchain.EthereumPaymentIntentReference{
		ID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: onchain.Network(intent.Network), ChainID: intent.ChainID,
		TokenContract: intent.TokenContract, DepositAddress: intent.DepositAddress,
	}, true, nil
}

func (r *OnchainRepository) CommitEthereumScanBlock(ctx context.Context, input onchain.EthereumScanBlockCommit) error {
	const maxInt64 = uint64(1<<63 - 1)
	if (input.Network != onchain.NetworkEthereumMainnet && input.Network != onchain.NetworkEthereumSepolia) ||
		strings.TrimSpace(input.LeaseOwner) == "" || input.Now.IsZero() || input.Block.Timestamp.IsZero() {
		return fmt.Errorf("invalid Ethereum scan block commit identity")
	}
	if input.Block.Number > maxInt64 || input.PreviousHeight > maxInt64 ||
		input.Block.Number != input.PreviousHeight+1 || strings.TrimSpace(input.Block.Hash) == "" {
		return fmt.Errorf("%w: invalid block sequence", onchain.ErrEthereumScanCursorConflict)
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		cursor, err := txRepo.db(txCtx).ChainScanCursor.Query().Where(
			chainscancursor.NetworkEQ(string(input.Network)),
		).Only(txCtx)
		if err != nil {
			return err
		}
		if cursor.LeaseOwner == nil || *cursor.LeaseOwner != input.LeaseOwner || cursor.LeaseUntil == nil || !cursor.LeaseUntil.After(input.Now) {
			return onchain.ErrEthereumScanLeaseLost
		}
		if onchain.CursorHealth(cursor.Health) == onchain.CursorHashConflict {
			return onchain.ErrEthereumScanHashConflict
		}
		if cursor.FinalizedHeight != int64(input.PreviousHeight) {
			return onchain.ErrEthereumScanCursorConflict
		}
		for _, deposit := range input.Deposits {
			if deposit.Intent.Network != input.Network || deposit.Transfer.BlockNumber != input.Block.Number ||
				!strings.EqualFold(deposit.Transfer.BlockHash, input.Block.Hash) {
				return fmt.Errorf("Ethereum scan deposit does not belong to committed block")
			}
			intent, err := txRepo.db(txCtx).OnchainPaymentIntent.Query().Where(
				onchainpaymentintent.IDEQ(deposit.Intent.ID),
				onchainpaymentintent.NetworkEQ(string(input.Network)),
				onchainpaymentintent.DepositAddressEQ(deposit.Transfer.ToAddress),
			).Only(txCtx)
			if err != nil {
				return fmt.Errorf("verify Ethereum payment intent binding: %w", err)
			}
			if !strings.EqualFold(intent.TokenContract, deposit.Transfer.ContractAddress) {
				return fmt.Errorf("Ethereum payment intent contract does not match deposit")
			}
			if err := txRepo.EnsureDeposit(txCtx, DepositCreate{
				IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
				Network: string(input.Network), ChainID: intent.ChainID,
				TransactionID: deposit.Transfer.TransactionHash, LogIndex: int64(deposit.Transfer.LogIndex),
				TransactionIndex: int64(deposit.TransactionIndex), BlockHeight: int64(input.Block.Number),
				BlockHash: input.Block.Hash, TokenContract: deposit.Transfer.ContractAddress,
				FromAddress: deposit.Transfer.FromAddress, ToAddress: deposit.Transfer.ToAddress,
				AmountRaw: deposit.Transfer.AmountRaw, TransactionTime: input.Block.Timestamp,
			}); err != nil {
				return fmt.Errorf("create Ethereum deposit: %w", err)
			}
		}
		updated, err := txRepo.db(txCtx).ChainScanCursor.Update().Where(
			chainscancursor.IDEQ(cursor.ID), chainscancursor.VersionEQ(cursor.Version),
			chainscancursor.FinalizedHeightEQ(int64(input.PreviousHeight)),
			chainscancursor.LeaseOwnerEQ(input.LeaseOwner), chainscancursor.LeaseUntilGT(input.Now),
		).SetFinalizedHeight(int64(input.Block.Number)).SetFinalizedHash(input.Block.Hash).
			SetHealth(string(onchain.CursorHealthy)).SetLastSuccessAt(input.Now).
			ClearLastErrorCode().ClearLastErrorMessage().AddVersion(1).Save(txCtx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return onchain.ErrEthereumScanCursorConflict
		}
		return nil
	})
}

func (r *OnchainRepository) ReconcileEthereumScanBlock(ctx context.Context, input onchain.EthereumScanBlockReconcile) error {
	const maxInt64 = uint64(1<<63 - 1)
	if (input.Network != onchain.NetworkEthereumMainnet && input.Network != onchain.NetworkEthereumSepolia) ||
		strings.TrimSpace(input.LeaseOwner) == "" || input.Now.IsZero() || input.Block.Number < 1 ||
		input.Block.Number > maxInt64 || strings.TrimSpace(input.Block.Hash) == "" || input.Block.Timestamp.IsZero() {
		return fmt.Errorf("invalid Ethereum scan block reconciliation")
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		cursor, err := txRepo.db(txCtx).ChainScanCursor.Query().Where(
			chainscancursor.NetworkEQ(string(input.Network)),
		).Only(txCtx)
		if err != nil {
			return err
		}
		if cursor.LeaseOwner == nil || *cursor.LeaseOwner != input.LeaseOwner || cursor.LeaseUntil == nil || !cursor.LeaseUntil.After(input.Now) {
			return onchain.ErrEthereumScanLeaseLost
		}
		if onchain.CursorHealth(cursor.Health) == onchain.CursorHashConflict {
			return onchain.ErrEthereumScanHashConflict
		}
		if int64(input.Block.Number) > cursor.FinalizedHeight {
			return onchain.ErrEthereumScanCursorConflict
		}

		expected := make(map[onchainEventKey]DepositCreate, len(input.Deposits))
		for _, deposit := range input.Deposits {
			candidate, err := txRepo.resolveEthereumDepositCreate(txCtx, input.Network, input.Block, deposit)
			if err != nil {
				return err
			}
			key := onchainEventKey{transactionID: candidate.TransactionID, logIndex: candidate.LogIndex}
			if previous, exists := expected[key]; exists && !depositCreateEqual(previous, candidate) {
				return onchain.ErrEthereumScanHashConflict
			}
			expected[key] = candidate
		}
		stored, err := txRepo.db(txCtx).OnchainDeposit.Query().Where(
			onchaindeposit.NetworkEQ(string(input.Network)), onchaindeposit.BlockHeightEQ(int64(input.Block.Number)),
		).All(txCtx)
		if err != nil {
			return err
		}
		for _, existing := range stored {
			key := onchainEventKey{transactionID: existing.TransactionID, logIndex: existing.LogIndex}
			candidate, exists := expected[key]
			if !exists || !strings.EqualFold(existing.BlockHash, input.Block.Hash) || !depositMatchesInput(existing, candidate) {
				return onchain.ErrEthereumScanHashConflict
			}
		}
		for _, candidate := range expected {
			if err := txRepo.EnsureDeposit(txCtx, candidate); err != nil {
				if errors.Is(err, ErrOnchainConflict) {
					return onchain.ErrEthereumScanHashConflict
				}
				return err
			}
		}
		return nil
	})
}

func (r *OnchainRepository) MarkEthereumScanHashConflict(ctx context.Context, network onchain.Network, leaseOwner string, now time.Time, cause error) error {
	if strings.TrimSpace(string(network)) == "" || strings.TrimSpace(leaseOwner) == "" || now.IsZero() {
		return fmt.Errorf("invalid Ethereum hash conflict identity")
	}
	message := "finalized block hash or event set conflict"
	if cause != nil {
		message = cause.Error()
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	updated, err := r.db(ctx).ChainScanCursor.Update().Where(
		chainscancursor.NetworkEQ(string(network)), chainscancursor.LeaseOwnerEQ(leaseOwner),
		chainscancursor.LeaseUntilGT(now),
	).SetHealth(string(onchain.CursorHashConflict)).SetLastErrorCode("FINALIZED_HASH_CONFLICT").
		SetLastErrorMessage(message).AddVersion(1).Save(ctx)
	if err != nil {
		return err
	}
	if updated != 1 {
		return onchain.ErrEthereumScanLeaseLost
	}
	return nil
}

func (r *OnchainRepository) resolveEthereumDepositCreate(ctx context.Context, network onchain.Network, block onchain.EthereumBlockRef, deposit onchain.EthereumScanDeposit) (DepositCreate, error) {
	if deposit.Intent.Network != network || deposit.Transfer.BlockNumber != block.Number ||
		!strings.EqualFold(deposit.Transfer.BlockHash, block.Hash) {
		return DepositCreate{}, fmt.Errorf("Ethereum scan deposit does not belong to reconciled block")
	}
	intent, err := r.db(ctx).OnchainPaymentIntent.Query().Where(
		onchainpaymentintent.IDEQ(deposit.Intent.ID), onchainpaymentintent.NetworkEQ(string(network)),
		onchainpaymentintent.DepositAddressEQ(deposit.Transfer.ToAddress),
	).Only(ctx)
	if err != nil {
		return DepositCreate{}, fmt.Errorf("verify Ethereum payment intent binding: %w", err)
	}
	if !strings.EqualFold(intent.TokenContract, deposit.Transfer.ContractAddress) {
		return DepositCreate{}, fmt.Errorf("Ethereum payment intent contract does not match deposit")
	}
	return DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: string(network), ChainID: intent.ChainID,
		TransactionID: deposit.Transfer.TransactionHash, LogIndex: int64(deposit.Transfer.LogIndex),
		TransactionIndex: int64(deposit.TransactionIndex), BlockHeight: int64(block.Number), BlockHash: block.Hash,
		TokenContract: deposit.Transfer.ContractAddress, FromAddress: deposit.Transfer.FromAddress,
		ToAddress: deposit.Transfer.ToAddress, AmountRaw: deposit.Transfer.AmountRaw, TransactionTime: block.Timestamp,
	}, nil
}

type onchainEventKey struct {
	transactionID string
	logIndex      int64
}

func (r *OnchainRepository) resolveTRONDepositCreate(ctx context.Context, network onchain.Network, block onchain.TRONSolidifiedBlock, deposit onchain.TRONScanDeposit) (DepositCreate, error) {
	if deposit.Intent.Network != network || deposit.Transfer.BlockHeight != block.Height || !deposit.Transfer.BlockTimestamp.Equal(block.Timestamp) {
		return DepositCreate{}, fmt.Errorf("TRON scan deposit does not belong to reconciled block")
	}
	intent, err := r.db(ctx).OnchainPaymentIntent.Query().
		Where(
			onchainpaymentintent.IDEQ(deposit.Intent.ID),
			onchainpaymentintent.NetworkEQ(string(network)),
			onchainpaymentintent.DepositAddressEQ(deposit.Transfer.ToAddress),
		).
		Only(ctx)
	if err != nil {
		return DepositCreate{}, fmt.Errorf("verify TRON payment intent binding: %w", err)
	}
	if intent.TokenContract != deposit.Transfer.ContractAddress {
		return DepositCreate{}, fmt.Errorf("TRON payment intent contract does not match deposit")
	}
	return DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: string(network), ChainID: intent.ChainID,
		TransactionID: deposit.Transfer.TransactionID, LogIndex: int64(deposit.Transfer.LogIndex),
		TransactionIndex: int64(deposit.TransactionIndex), BlockHeight: block.Height, BlockHash: block.Hash,
		TokenContract: deposit.Transfer.ContractAddress, FromAddress: deposit.Transfer.FromAddress,
		ToAddress: deposit.Transfer.ToAddress, AmountRaw: deposit.Transfer.AmountRaw,
		TransactionTime: deposit.Transfer.BlockTimestamp,
	}, nil
}

func depositCreateEqual(left, right DepositCreate) bool {
	return left.IntentID == right.IntentID &&
		left.PaymentOrderID == right.PaymentOrderID &&
		left.UserID == right.UserID &&
		left.Network == right.Network &&
		left.ChainID == right.ChainID &&
		left.TransactionID == right.TransactionID &&
		left.LogIndex == right.LogIndex &&
		left.TransactionIndex == right.TransactionIndex &&
		left.BlockHeight == right.BlockHeight &&
		left.BlockHash == right.BlockHash &&
		left.TokenContract == right.TokenContract &&
		left.FromAddress == right.FromAddress &&
		left.ToAddress == right.ToAddress &&
		left.AmountRaw == right.AmountRaw &&
		left.TransactionTime.Equal(right.TransactionTime)
}

type WalletSweepCreate struct {
	IntentID           int64
	Network            string
	ChainID            int64
	SourceAddress      string
	DestinationAddress string
	BalanceSnapshotRaw string
	AmountRaw          string
	IdempotencyKey     string
	Status             onchain.TransferStatus
}

func (r *OnchainRepository) CreateWalletSweep(ctx context.Context, input WalletSweepCreate) (*dbent.WalletSweep, error) {
	create := r.db(ctx).WalletSweep.Create().
		SetIntentID(input.IntentID).
		SetNetwork(input.Network).
		SetChainID(input.ChainID).
		SetSourceAddress(input.SourceAddress).
		SetDestinationAddress(input.DestinationAddress).
		SetBalanceSnapshotRaw(input.BalanceSnapshotRaw).
		SetAmountRaw(input.AmountRaw).
		SetIdempotencyKey(input.IdempotencyKey)
	if input.Status != "" {
		create.SetStatus(string(input.Status))
	}
	entity, err := create.Save(ctx)
	return entity, translateOnchainWriteError(err)
}

func (r *OnchainRepository) ListTRONSweepCandidates(ctx context.Context, network onchain.Network, limit int) ([]onchain.TRONSweepCandidate, error) {
	if (network != onchain.NetworkTronMainnet && network != onchain.NetworkTronNile) || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid TRON sweep candidate query")
	}
	db := r.db(ctx)
	intents, err := db.OnchainPaymentIntent.Query().
		Where(
			onchainpaymentintent.NetworkEQ(string(network)),
			onchainpaymentintent.StatusEQ(string(onchain.IntentSettled)),
			onchainpaymentintent.HasDepositsWith(
				onchaindeposit.FinalizedEQ(true),
				onchaindeposit.ReceiptSuccessEQ(true),
				onchaindeposit.StatusEQ(string(onchain.DepositCredited)),
			),
			onchainpaymentintent.Not(onchainpaymentintent.HasWalletSweepsWith(
				walletsweep.StatusNotIn(string(onchain.TransferFinalized), string(onchain.TransferResourceWait)),
			)),
		).
		Order(dbent.Asc(onchainpaymentintent.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query settled TRON sweep intents: %w", err)
	}
	result := make([]onchain.TRONSweepCandidate, 0, len(intents))
	for _, intent := range intents {
		latestDeposit, depositErr := db.OnchainDeposit.Query().
			Where(
				onchaindeposit.IntentIDEQ(intent.ID),
				onchaindeposit.FinalizedEQ(true),
				onchaindeposit.ReceiptSuccessEQ(true),
				onchaindeposit.StatusEQ(string(onchain.DepositCredited)),
			).
			Order(dbent.Desc(onchaindeposit.FieldID)).
			First(ctx)
		if depositErr != nil {
			return nil, fmt.Errorf("query latest credited deposit for intent %d: %w", intent.ID, depositErr)
		}
		candidate := onchain.TRONSweepCandidate{
			IntentID: intent.ID, Network: network, ChainID: intent.ChainID,
			SourceAddress: intent.DepositAddress, DerivationIndex: intent.DerivationIndex,
			FundingMarker: latestDeposit.ID,
		}
		waiting, waitErr := db.WalletSweep.Query().
			Where(
				walletsweep.IntentIDEQ(intent.ID),
				walletsweep.StatusEQ(string(onchain.TransferResourceWait)),
			).
			Order(dbent.Asc(walletsweep.FieldID)).
			First(ctx)
		if waitErr == nil {
			candidate.WaitingTask = &onchain.TRONSweepTask{
				ID: waiting.ID, IdempotencyKey: waiting.IdempotencyKey, AmountRaw: waiting.AmountRaw,
				Status: onchain.TransferStatus(waiting.Status), Version: waiting.Version,
			}
		} else if !dbent.IsNotFound(waitErr) {
			return nil, fmt.Errorf("query resource-wait TRON sweep for intent %d: %w", intent.ID, waitErr)
		}
		result = append(result, candidate)
	}
	return result, nil
}

func (r *OnchainRepository) EnsureTRONSweep(ctx context.Context, input onchain.TRONSweepCreate) (onchain.TRONSweepTask, bool, error) {
	created, err := r.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID: input.IntentID, Network: string(input.Network), ChainID: input.ChainID,
		SourceAddress: input.SourceAddress, DestinationAddress: input.DestinationAddress,
		BalanceSnapshotRaw: input.BalanceSnapshotRaw, AmountRaw: input.AmountRaw,
		IdempotencyKey: input.IdempotencyKey, Status: input.Status,
	})
	if err == nil {
		return onchain.TRONSweepTask{
			ID: created.ID, IdempotencyKey: created.IdempotencyKey, AmountRaw: created.AmountRaw,
			Status: onchain.TransferStatus(created.Status), Version: created.Version,
		}, true, nil
	}
	if !errors.Is(err, ErrOnchainConflict) {
		return onchain.TRONSweepTask{}, false, err
	}
	existing, queryErr := r.db(ctx).WalletSweep.Query().
		Where(walletsweep.IdempotencyKeyEQ(input.IdempotencyKey)).
		Only(ctx)
	if queryErr != nil {
		return onchain.TRONSweepTask{}, false, fmt.Errorf("load idempotent TRON sweep: %w", queryErr)
	}
	if existing.IntentID != input.IntentID || existing.Network != string(input.Network) || existing.ChainID != input.ChainID ||
		existing.SourceAddress != input.SourceAddress || existing.DestinationAddress != input.DestinationAddress ||
		existing.BalanceSnapshotRaw != input.BalanceSnapshotRaw || existing.AmountRaw != input.AmountRaw {
		return onchain.TRONSweepTask{}, false, fmt.Errorf("%w: TRON sweep idempotency payload mismatch", ErrOnchainConflict)
	}
	return onchain.TRONSweepTask{
		ID: existing.ID, IdempotencyKey: existing.IdempotencyKey, AmountRaw: existing.AmountRaw,
		Status: onchain.TransferStatus(existing.Status), Version: existing.Version,
	}, false, nil
}

func (r *OnchainRepository) MarkTRONSweepResourcesReady(ctx context.Context, taskID int64, version int) error {
	return r.TransitionWalletSweep(ctx, taskID, version, onchain.TransferResourceWait, onchain.TransferPrepared)
}

func (r *OnchainRepository) ListTRONSweepExecutionTasks(ctx context.Context, network onchain.Network, now time.Time, limit int, includeSigning bool) ([]onchain.TRONSweepExecutionTask, error) {
	if (network != onchain.NetworkTronMainnet && network != onchain.NetworkTronNile) || now.IsZero() || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid TRON sweep execution query")
	}
	statuses := []string{
		string(onchain.TransferBroadcast),
		string(onchain.TransferConfirming),
	}
	if includeSigning {
		statuses = append(statuses,
			string(onchain.TransferPrepared),
			string(onchain.TransferSigning),
		)
	}
	sweeps, err := r.db(ctx).WalletSweep.Query().
		Where(
			walletsweep.NetworkEQ(string(network)),
			walletsweep.StatusIn(statuses...),
			walletsweep.Or(walletsweep.NextAttemptAtIsNil(), walletsweep.NextAttemptAtLTE(now)),
		).
		WithIntent().
		Order(dbent.Asc(walletsweep.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query TRON sweep execution tasks: %w", err)
	}
	result := make([]onchain.TRONSweepExecutionTask, 0, len(sweeps))
	for _, sweep := range sweeps {
		intent, edgeErr := sweep.Edges.IntentOrErr()
		if edgeErr != nil {
			return nil, fmt.Errorf("load TRON sweep %d intent: %w", sweep.ID, edgeErr)
		}
		transactionID := ""
		if sweep.TransactionID != nil {
			transactionID = *sweep.TransactionID
		}
		result = append(result, onchain.TRONSweepExecutionTask{
			ID: sweep.ID, IntentID: sweep.IntentID, DerivationIndex: intent.DerivationIndex,
			SourceAddress: sweep.SourceAddress, DestinationAddress: sweep.DestinationAddress,
			BalanceSnapshotRaw: sweep.BalanceSnapshotRaw, AmountRaw: sweep.AmountRaw,
			IdempotencyKey: sweep.IdempotencyKey, TransactionID: transactionID,
			Status: onchain.TransferStatus(sweep.Status), RetryCount: sweep.RetryCount, Version: sweep.Version,
		})
	}
	return result, nil
}

func (r *OnchainRepository) MarkTRONSweepSigning(ctx context.Context, taskID int64, version int, requestDigest string) error {
	requestDigest = strings.TrimSpace(requestDigest)
	if taskID <= 0 || version < 0 || requestDigest == "" || len(requestDigest) > 128 {
		return fmt.Errorf("invalid TRON sweep signing claim")
	}
	updated, err := r.db(ctx).WalletSweep.Update().
		Where(
			walletsweep.IDEQ(taskID),
			walletsweep.VersionEQ(version),
			walletsweep.StatusEQ(string(onchain.TransferPrepared)),
		).
		SetStatus(string(onchain.TransferSigning)).
		SetSignerRequestDigest(requestDigest).
		ClearFailureCode().
		ClearFailureReason().
		ClearNextAttemptAt().
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) RecordTRONSweepBroadcast(ctx context.Context, taskID int64, version int, signerAuditID, transactionID string) error {
	signerAuditID = strings.TrimSpace(signerAuditID)
	transactionID = strings.TrimSpace(transactionID)
	if taskID <= 0 || version < 0 || signerAuditID == "" || len(signerAuditID) > 128 || transactionID == "" || len(transactionID) > 128 {
		return fmt.Errorf("invalid TRON sweep broadcast result")
	}
	updated, err := r.db(ctx).WalletSweep.Update().
		Where(
			walletsweep.IDEQ(taskID),
			walletsweep.VersionEQ(version),
			walletsweep.StatusEQ(string(onchain.TransferSigning)),
		).
		SetStatus(string(onchain.TransferBroadcast)).
		SetSignerAuditID(signerAuditID).
		SetTransactionID(transactionID).
		ClearFailureCode().
		ClearFailureReason().
		ClearNextAttemptAt().
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) RecordTRONSweepRetry(ctx context.Context, taskID int64, version int, status onchain.TransferStatus, failureCode, failureReason string, nextAttempt time.Time, review bool) error {
	failureCode = strings.TrimSpace(failureCode)
	failureReason = strings.TrimSpace(failureReason)
	if taskID <= 0 || version < 0 || failureCode == "" || len(failureCode) > 128 || failureReason == "" || nextAttempt.IsZero() {
		return fmt.Errorf("invalid TRON sweep retry result")
	}
	if status != onchain.TransferPrepared && status != onchain.TransferSigning && status != onchain.TransferBroadcast && status != onchain.TransferConfirming {
		return fmt.Errorf("invalid TRON sweep retry status %q", status)
	}
	if review && !onchain.CanTransitionTransfer(status, onchain.TransferReviewRequired) {
		return invalidTransition(status, onchain.TransferReviewRequired)
	}
	if len(failureReason) > 4096 {
		failureReason = failureReason[:4096]
	}
	update := r.db(ctx).WalletSweep.Update().
		Where(
			walletsweep.IDEQ(taskID),
			walletsweep.VersionEQ(version),
			walletsweep.StatusEQ(string(status)),
		).
		SetFailureCode(failureCode).
		SetFailureReason(failureReason).
		AddRetryCount(1).
		AddVersion(1)
	if review {
		update.SetStatus(string(onchain.TransferReviewRequired)).ClearNextAttemptAt()
	} else {
		update.SetNextAttemptAt(nextAttempt)
	}
	updated, err := update.Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) ScheduleTRONSweepCheck(ctx context.Context, taskID int64, version int, status onchain.TransferStatus, nextAttempt time.Time) error {
	if taskID <= 0 || version < 0 || nextAttempt.IsZero() || (status != onchain.TransferBroadcast && status != onchain.TransferConfirming) {
		return fmt.Errorf("invalid TRON sweep confirmation schedule")
	}
	updated, err := r.db(ctx).WalletSweep.Update().
		Where(
			walletsweep.IDEQ(taskID),
			walletsweep.VersionEQ(version),
			walletsweep.StatusEQ(string(status)),
		).
		SetNextAttemptAt(nextAttempt).
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) MarkTRONSweepConfirming(ctx context.Context, taskID int64, version int) error {
	if taskID <= 0 || version < 0 {
		return fmt.Errorf("invalid TRON sweep confirmation claim")
	}
	updated, err := r.db(ctx).WalletSweep.Update().
		Where(
			walletsweep.IDEQ(taskID),
			walletsweep.VersionEQ(version),
			walletsweep.StatusEQ(string(onchain.TransferBroadcast)),
		).
		SetStatus(string(onchain.TransferConfirming)).
		ClearNextAttemptAt().
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) FinalizeTRONSweep(ctx context.Context, input onchain.TRONSweepFinalization) error {
	fee, ok := new(big.Int).SetString(strings.TrimSpace(input.FeeRaw), 10)
	if input.TaskID <= 0 || input.Version < 0 || input.BlockHeight < 0 || strings.TrimSpace(input.BlockHash) == "" ||
		!ok || fee.Sign() < 0 || fee.BitLen() > 256 || fee.String() != strings.TrimSpace(input.FeeRaw) ||
		input.EnergyUsed < 0 || input.BandwidthUsed < 0 || input.FinalizedAt.IsZero() {
		return fmt.Errorf("invalid TRON sweep finalization")
	}
	updated, err := r.db(ctx).WalletSweep.Update().
		Where(
			walletsweep.IDEQ(input.TaskID),
			walletsweep.VersionEQ(input.Version),
			walletsweep.StatusEQ(string(onchain.TransferConfirming)),
		).
		SetStatus(string(onchain.TransferFinalized)).
		SetFinalizedBlockHeight(input.BlockHeight).
		SetFinalizedBlockHash(strings.TrimSpace(input.BlockHash)).
		SetFeeRaw(fee.String()).
		SetEnergyUsed(input.EnergyUsed).
		SetBandwidthUsed(input.BandwidthUsed).
		SetFinalizedAt(input.FinalizedAt).
		ClearFailureCode().
		ClearFailureReason().
		ClearNextAttemptAt().
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

type EthereumGasFundingCreate struct {
	IntentID                int64
	ChainID                 int64
	SponsorAddress          string
	TargetAddress           string
	DerivationIndex         int64
	AmountWei               string
	IdempotencyKey          string
	Nonce                   int64
	GasLimit                int64
	MaxFeePerGasWei         string
	MaxPriorityFeePerGasWei string
}

func (r *OnchainRepository) CreateEthereumGasFunding(ctx context.Context, input EthereumGasFundingCreate) (*dbent.EthereumGasFunding, error) {
	entity, err := r.db(ctx).EthereumGasFunding.Create().
		SetIntentID(input.IntentID).
		SetChainID(input.ChainID).
		SetSponsorAddress(input.SponsorAddress).
		SetTargetAddress(input.TargetAddress).
		SetDerivationIndex(input.DerivationIndex).
		SetAmountWei(input.AmountWei).
		SetIdempotencyKey(input.IdempotencyKey).
		SetNonce(input.Nonce).
		SetGasLimit(input.GasLimit).
		SetMaxFeePerGasWei(input.MaxFeePerGasWei).
		SetMaxPriorityFeePerGasWei(input.MaxPriorityFeePerGasWei).
		Save(ctx)
	return entity, translateOnchainWriteError(err)
}

type EthereumNonceStateCreate struct {
	ChainID       int64
	SenderAddress string
	NextNonce     int64
}

func (r *OnchainRepository) CreateEthereumNonceState(ctx context.Context, input EthereumNonceStateCreate) (*dbent.EthereumNonceState, error) {
	entity, err := r.db(ctx).EthereumNonceState.Create().
		SetChainID(input.ChainID).
		SetSenderAddress(input.SenderAddress).
		SetNextNonce(input.NextNonce).
		SetObservedPendingNonce(input.NextNonce).
		Save(ctx)
	return entity, translateOnchainWriteError(err)
}

func (r *OnchainRepository) ReserveEthereumNonce(ctx context.Context, input onchain.EthereumNonceReserveInput) (onchain.EthereumNonceReservation, bool, error) {
	if input.ChainID <= 0 || strings.TrimSpace(input.SenderAddress) == "" || strings.TrimSpace(input.Owner) == "" ||
		input.ObservedPending > uint64(^uint64(0)>>1) || input.Now.IsZero() || !input.LeaseUntil.After(input.Now) {
		return onchain.EthereumNonceReservation{}, false, fmt.Errorf("invalid Ethereum nonce reservation input")
	}
	var reservation onchain.EthereumNonceReservation
	var acquired bool
	var conflictMessage string
	err := r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		db := txRepo.db(txCtx)
		state, err := db.EthereumNonceState.Query().
			Where(
				ethereumnoncestate.ChainIDEQ(input.ChainID),
				ethereumnoncestate.SenderAddressEqualFold(input.SenderAddress),
			).
			Only(txCtx)
		if dbent.IsNotFound(err) {
			state, err = db.EthereumNonceState.Create().
				SetChainID(input.ChainID).
				SetSenderAddress(input.SenderAddress).
				SetNextNonce(int64(input.ObservedPending)).
				SetObservedPendingNonce(int64(input.ObservedPending)).
				SetStatus(string(onchain.NonceReady)).
				SetLastReconciledAt(input.Now).
				Save(txCtx)
		}
		if err != nil {
			return fmt.Errorf("load Ethereum nonce state: %w", err)
		}
		status := onchain.NonceStatus(state.Status)
		if status == onchain.NonceConflict || status == onchain.NoncePaused {
			return onchain.ErrEthereumNonceConflict
		}
		if state.LeaseOwner != nil && *state.LeaseOwner != input.Owner && state.LeaseUntil != nil && state.LeaseUntil.After(input.Now) {
			return nil
		}
		if status == onchain.NonceReserved && state.LeaseOwner != nil && *state.LeaseOwner == input.Owner {
			if state.NextNonce <= 0 {
				return fmt.Errorf("reserved Ethereum nonce state is invalid")
			}
			reservation = onchain.EthereumNonceReservation{
				StateID: state.ID, ChainID: state.ChainID, SenderAddress: state.SenderAddress,
				Nonce: uint64(state.NextNonce - 1), Owner: input.Owner, Version: state.Version,
			}
			if state.LeaseUntil != nil {
				reservation.LeaseUntil = *state.LeaseUntil
			}
			acquired = true
			return nil
		}
		if state.NextNonce != int64(input.ObservedPending) {
			conflictMessage = fmt.Sprintf("local next nonce is %d but node pending nonce is %d", state.NextNonce, input.ObservedPending)
			updated, updateErr := db.EthereumNonceState.Update().
				Where(ethereumnoncestate.IDEQ(state.ID), ethereumnoncestate.VersionEQ(state.Version)).
				SetStatus(string(onchain.NonceConflict)).
				SetObservedPendingNonce(int64(input.ObservedPending)).
				SetLastErrorCode("PENDING_NONCE_CONFLICT").
				SetLastErrorMessage(conflictMessage).
				SetLastReconciledAt(input.Now).
				ClearLeaseOwner().ClearLeaseUntil().
				AddVersion(1).
				Save(txCtx)
			return conditionalUpdateResult(updated, updateErr)
		}
		if state.NextNonce == int64(^uint64(0)>>1) {
			return fmt.Errorf("Ethereum nonce state exhausted persistent range")
		}
		updated, updateErr := db.EthereumNonceState.Update().
			Where(ethereumnoncestate.IDEQ(state.ID), ethereumnoncestate.VersionEQ(state.Version)).
			SetStatus(string(onchain.NonceReserved)).
			SetLeaseOwner(input.Owner).
			SetLeaseUntil(input.LeaseUntil).
			SetObservedPendingNonce(int64(input.ObservedPending)).
			SetLastReconciledAt(input.Now).
			ClearLastErrorCode().ClearLastErrorMessage().
			AddNextNonce(1).
			AddVersion(1).
			Save(txCtx)
		if err := conditionalUpdateResult(updated, updateErr); err != nil {
			return err
		}
		reservation = onchain.EthereumNonceReservation{
			StateID: state.ID, ChainID: state.ChainID, SenderAddress: state.SenderAddress,
			Nonce: input.ObservedPending, Owner: input.Owner, Version: state.Version + 1, LeaseUntil: input.LeaseUntil,
		}
		acquired = true
		return nil
	})
	if err != nil {
		return onchain.EthereumNonceReservation{}, false, err
	}
	if conflictMessage != "" {
		return onchain.EthereumNonceReservation{}, false, fmt.Errorf("%w: %s", onchain.ErrEthereumNonceConflict, conflictMessage)
	}
	return reservation, acquired, nil
}

func (r *OnchainRepository) CommitEthereumNonce(ctx context.Context, reservation onchain.EthereumNonceReservation, observedPending uint64, reconciledAt time.Time) error {
	if reservation.StateID <= 0 || reservation.Version < 0 || strings.TrimSpace(reservation.Owner) == "" ||
		observedPending > uint64(^uint64(0)>>1) || reconciledAt.IsZero() {
		return fmt.Errorf("invalid Ethereum nonce commit")
	}
	updated, err := r.db(ctx).EthereumNonceState.Update().
		Where(
			ethereumnoncestate.IDEQ(reservation.StateID),
			ethereumnoncestate.VersionEQ(reservation.Version),
			ethereumnoncestate.StatusEQ(string(onchain.NonceReserved)),
			ethereumnoncestate.LeaseOwnerEQ(reservation.Owner),
		).
		SetStatus(string(onchain.NonceReady)).
		SetObservedPendingNonce(int64(observedPending)).
		SetLastReconciledAt(reconciledAt).
		ClearLeaseOwner().ClearLeaseUntil().
		ClearLastErrorCode().ClearLastErrorMessage().
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) MarkEthereumNonceConflict(ctx context.Context, reservation onchain.EthereumNonceReservation, observedPending uint64, code, message string, reconciledAt time.Time) error {
	code = strings.TrimSpace(code)
	message = strings.TrimSpace(message)
	if reservation.StateID <= 0 || reservation.Version < 0 || strings.TrimSpace(reservation.Owner) == "" ||
		observedPending > uint64(^uint64(0)>>1) || code == "" || len(code) > 128 || message == "" || reconciledAt.IsZero() {
		return fmt.Errorf("invalid Ethereum nonce conflict")
	}
	if len(message) > 4096 {
		message = message[:4096]
	}
	updated, err := r.db(ctx).EthereumNonceState.Update().
		Where(
			ethereumnoncestate.IDEQ(reservation.StateID),
			ethereumnoncestate.VersionEQ(reservation.Version),
			ethereumnoncestate.StatusEQ(string(onchain.NonceReserved)),
			ethereumnoncestate.LeaseOwnerEQ(reservation.Owner),
		).
		SetStatus(string(onchain.NonceConflict)).
		SetObservedPendingNonce(int64(observedPending)).
		SetLastErrorCode(code).
		SetLastErrorMessage(message).
		SetLastReconciledAt(reconciledAt).
		ClearLeaseOwner().ClearLeaseUntil().
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) ListEthereumGasFundingTasks(ctx context.Context, chainID int64, now time.Time, limit int, includeSigning bool) ([]onchain.EthereumGasFundingTask, error) {
	if chainID <= 0 || now.IsZero() || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid Ethereum gas funding task query")
	}
	statuses := []string{string(onchain.TransferBroadcast), string(onchain.TransferConfirming)}
	if includeSigning {
		statuses = append(statuses, string(onchain.TransferPrepared), string(onchain.TransferSigning))
	}
	fundings, err := r.db(ctx).EthereumGasFunding.Query().
		Where(
			ethereumgasfunding.ChainIDEQ(chainID),
			ethereumgasfunding.StatusIn(statuses...),
			ethereumgasfunding.Or(ethereumgasfunding.NextAttemptAtIsNil(), ethereumgasfunding.NextAttemptAtLTE(now)),
		).
		Order(dbent.Asc(ethereumgasfunding.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query Ethereum gas funding tasks: %w", err)
	}
	result := make([]onchain.EthereumGasFundingTask, 0, len(fundings))
	for _, funding := range fundings {
		transactionHash := ""
		if funding.TransactionHash != nil {
			transactionHash = *funding.TransactionHash
		}
		result = append(result, onchain.EthereumGasFundingTask{
			ID: funding.ID, IntentID: funding.IntentID, ChainID: funding.ChainID,
			SponsorAddress: funding.SponsorAddress, TargetAddress: funding.TargetAddress,
			DerivationIndex: funding.DerivationIndex, AmountWei: funding.AmountWei,
			IdempotencyKey: funding.IdempotencyKey, TransactionHash: transactionHash,
			Status: onchain.TransferStatus(funding.Status), Nonce: uint64(funding.Nonce), GasLimit: uint64(funding.GasLimit), RetryCount: funding.RetryCount, Version: funding.Version,
		})
		versions, versionErr := r.db(ctx).EthereumGasFunding.Query().Where(
			ethereumgasfunding.IntentIDEQ(funding.IntentID), ethereumgasfunding.NonceEQ(funding.Nonce), ethereumgasfunding.TransactionHashNotNil(),
		).Order(dbent.Asc(ethereumgasfunding.FieldID)).All(ctx)
		if versionErr != nil {
			return nil, fmt.Errorf("query Ethereum gas funding versions: %w", versionErr)
		}
		for _, item := range versions {
			if item.TransactionHash != nil {
				result[len(result)-1].Versions = append(result[len(result)-1].Versions, onchain.EthereumTransactionVersionReference{ID: item.ID, TransactionHash: *item.TransactionHash, Status: onchain.TransferStatus(item.Status)})
			}
		}
	}
	return result, nil
}

func (r *OnchainRepository) MarkEthereumGasFundingSigning(ctx context.Context, taskID int64, version int, requestDigest string) error {
	requestDigest = strings.TrimSpace(requestDigest)
	if taskID <= 0 || version < 0 || requestDigest == "" || len(requestDigest) > 128 {
		return fmt.Errorf("invalid Ethereum gas funding signing claim")
	}
	updated, err := r.db(ctx).EthereumGasFunding.Update().
		Where(
			ethereumgasfunding.IDEQ(taskID),
			ethereumgasfunding.VersionEQ(version),
			ethereumgasfunding.StatusEQ(string(onchain.TransferPrepared)),
		).
		SetStatus(string(onchain.TransferSigning)).
		SetSignerRequestDigest(requestDigest).
		ClearFailureCode().
		ClearFailureReason().
		ClearNextAttemptAt().
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) RecordEthereumGasFundingBroadcast(ctx context.Context, taskID int64, version int, signerAuditID, transactionHash string) error {
	signerAuditID = strings.TrimSpace(signerAuditID)
	transactionHash = strings.TrimSpace(transactionHash)
	if taskID <= 0 || version < 0 || signerAuditID == "" || len(signerAuditID) > 128 || transactionHash == "" || len(transactionHash) > 128 {
		return fmt.Errorf("invalid Ethereum gas funding broadcast result")
	}
	updated, err := r.db(ctx).EthereumGasFunding.Update().
		Where(
			ethereumgasfunding.IDEQ(taskID),
			ethereumgasfunding.VersionEQ(version),
			ethereumgasfunding.StatusEQ(string(onchain.TransferSigning)),
		).
		SetStatus(string(onchain.TransferBroadcast)).
		SetSignerAuditID(signerAuditID).
		SetTransactionHash(transactionHash).
		ClearFailureCode().
		ClearFailureReason().
		ClearNextAttemptAt().
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) RecordEthereumGasFundingReplacement(ctx context.Context, input onchain.EthereumGasFundingReplacement) error {
	input.SignerAuditID = strings.TrimSpace(input.SignerAuditID)
	input.OriginalTransactionHash = strings.TrimSpace(input.OriginalTransactionHash)
	input.ReplacementTransactionHash = strings.TrimSpace(input.ReplacementTransactionHash)
	fee, feeOK := new(big.Int).SetString(strings.TrimSpace(input.MaxFeePerGasWei), 10)
	tip, tipOK := new(big.Int).SetString(strings.TrimSpace(input.MaxPriorityFeePerGasWei), 10)
	if input.TaskID <= 0 || input.Version < 0 || input.SignerAuditID == "" || len(input.SignerAuditID) > 128 ||
		!common.IsHexHash(input.OriginalTransactionHash) || !common.IsHexHash(input.ReplacementTransactionHash) ||
		strings.EqualFold(input.OriginalTransactionHash, input.ReplacementTransactionHash) || input.Nonce > uint64(^uint64(0)>>1) ||
		input.GasLimit < 21_000 || input.GasLimit > uint64(^uint64(0)>>1) || !feeOK || !tipOK || fee.Sign() <= 0 || tip.Sign() <= 0 || tip.Cmp(fee) > 0 || input.NextAttemptAt.IsZero() {
		return fmt.Errorf("invalid Ethereum gas funding replacement")
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		db := txRepo.db(txCtx)
		current, err := db.EthereumGasFunding.Query().Where(ethereumgasfunding.IDEQ(input.TaskID)).Only(txCtx)
		if err != nil {
			return fmt.Errorf("load Ethereum gas funding replacement source: %w", err)
		}
		if current.Version != input.Version || current.Status != string(onchain.TransferBroadcast) || current.TransactionHash == nil || !strings.EqualFold(*current.TransactionHash, input.OriginalTransactionHash) ||
			current.Nonce != int64(input.Nonce) || current.GasLimit != int64(input.GasLimit) || fee.Cmp(mustRepositoryInteger(current.MaxFeePerGasWei)) <= 0 || tip.Cmp(mustRepositoryInteger(current.MaxPriorityFeePerGasWei)) <= 0 {
			return ErrOnchainConflict
		}
		archiveKey := fmt.Sprintf("ethereum-gas-replaced:%d:%s", current.ID, strings.TrimPrefix(strings.ToLower(input.OriginalTransactionHash), "0x"))
		archived, err := db.EthereumGasFunding.Create().
			SetIntentID(current.IntentID).SetChainID(current.ChainID).SetSponsorAddress(current.SponsorAddress).
			SetTargetAddress(current.TargetAddress).SetDerivationIndex(current.DerivationIndex).SetAmountWei(current.AmountWei).
			SetIdempotencyKey(archiveKey).SetNillableSignerRequestDigest(current.SignerRequestDigest).SetNillableSignerAuditID(current.SignerAuditID).
			SetNonce(current.Nonce).SetGasLimit(current.GasLimit).SetMaxFeePerGasWei(current.MaxFeePerGasWei).
			SetMaxPriorityFeePerGasWei(current.MaxPriorityFeePerGasWei).
			SetNillableReplacementOfID(current.ReplacementOfID).SetStatus(string(onchain.TransferReplaced)).Save(txCtx)
		if err != nil {
			return translateOnchainWriteError(err)
		}
		updated, err := db.EthereumGasFunding.Update().Where(ethereumgasfunding.IDEQ(current.ID), ethereumgasfunding.VersionEQ(current.Version), ethereumgasfunding.StatusEQ(string(onchain.TransferBroadcast))).
			SetSignerAuditID(input.SignerAuditID).SetTransactionHash(input.ReplacementTransactionHash).SetReplacementOfID(archived.ID).
			SetNonce(int64(input.Nonce)).SetGasLimit(int64(input.GasLimit)).SetMaxFeePerGasWei(fee.String()).SetMaxPriorityFeePerGasWei(tip.String()).
			SetNextAttemptAt(input.NextAttemptAt).ClearFailureCode().ClearFailureReason().AddVersion(1).Save(txCtx)
		if err := conditionalUpdateResult(updated, err); err != nil {
			return err
		}
		archivedUpdated, err := db.EthereumGasFunding.Update().Where(ethereumgasfunding.IDEQ(archived.ID), ethereumgasfunding.StatusEQ(string(onchain.TransferReplaced))).SetTransactionHash(input.OriginalTransactionHash).Save(txCtx)
		return conditionalUpdateResult(archivedUpdated, err)
	})
}

func mustRepositoryInteger(raw string) *big.Int {
	value, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	if !ok {
		return new(big.Int)
	}
	return value
}

func (r *OnchainRepository) RecordEthereumGasFundingRetry(ctx context.Context, taskID int64, version int, status onchain.TransferStatus, failureCode, failureReason string, nextAttempt time.Time, review bool) error {
	failureCode = strings.TrimSpace(failureCode)
	failureReason = strings.TrimSpace(failureReason)
	if taskID <= 0 || version < 0 || failureCode == "" || len(failureCode) > 128 || failureReason == "" || nextAttempt.IsZero() {
		return fmt.Errorf("invalid Ethereum gas funding retry result")
	}
	if status != onchain.TransferPrepared && status != onchain.TransferSigning && status != onchain.TransferBroadcast && status != onchain.TransferConfirming {
		return fmt.Errorf("invalid Ethereum gas funding retry status %q", status)
	}
	if review && !onchain.CanTransitionTransfer(status, onchain.TransferReviewRequired) {
		return invalidTransition(status, onchain.TransferReviewRequired)
	}
	if len(failureReason) > 4096 {
		failureReason = failureReason[:4096]
	}
	update := r.db(ctx).EthereumGasFunding.Update().
		Where(
			ethereumgasfunding.IDEQ(taskID),
			ethereumgasfunding.VersionEQ(version),
			ethereumgasfunding.StatusEQ(string(status)),
		).
		SetFailureCode(failureCode).
		SetFailureReason(failureReason).
		AddRetryCount(1).
		AddVersion(1)
	if review {
		update.SetStatus(string(onchain.TransferReviewRequired)).ClearNextAttemptAt()
	} else {
		update.SetNextAttemptAt(nextAttempt)
	}
	updated, err := update.Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) ScheduleEthereumGasFundingCheck(ctx context.Context, taskID int64, version int, status onchain.TransferStatus, nextAttempt time.Time) error {
	if taskID <= 0 || version < 0 || nextAttempt.IsZero() || (status != onchain.TransferBroadcast && status != onchain.TransferConfirming) {
		return fmt.Errorf("invalid Ethereum gas funding confirmation schedule")
	}
	updated, err := r.db(ctx).EthereumGasFunding.Update().
		Where(
			ethereumgasfunding.IDEQ(taskID),
			ethereumgasfunding.VersionEQ(version),
			ethereumgasfunding.StatusEQ(string(status)),
		).
		SetNextAttemptAt(nextAttempt).
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) MarkEthereumGasFundingConfirming(ctx context.Context, taskID int64, version int) error {
	if taskID <= 0 || version < 0 {
		return fmt.Errorf("invalid Ethereum gas funding confirmation claim")
	}
	updated, err := r.db(ctx).EthereumGasFunding.Update().
		Where(
			ethereumgasfunding.IDEQ(taskID),
			ethereumgasfunding.VersionEQ(version),
			ethereumgasfunding.StatusEQ(string(onchain.TransferBroadcast)),
		).
		SetStatus(string(onchain.TransferConfirming)).
		ClearNextAttemptAt().
		AddVersion(1).
		Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) FinalizeEthereumGasFunding(ctx context.Context, input onchain.EthereumGasFundingFinalization) error {
	fee, ok := new(big.Int).SetString(strings.TrimSpace(input.ActualFeeWei), 10)
	if input.TaskID <= 0 || input.Version < 0 || input.BlockHeight < 0 || strings.TrimSpace(input.BlockHash) == "" ||
		!ok || fee.Sign() < 0 || fee.BitLen() > 256 || fee.String() != strings.TrimSpace(input.ActualFeeWei) || input.FinalizedAt.IsZero() {
		return fmt.Errorf("invalid Ethereum gas funding finalization")
	}
	if input.WinnerID <= 0 || !common.IsHexHash(input.WinnerHash) {
		return fmt.Errorf("invalid Ethereum gas funding winner")
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		db := txRepo.db(txCtx)
		current, err := db.EthereumGasFunding.Query().Where(ethereumgasfunding.IDEQ(input.TaskID)).Only(txCtx)
		if err != nil {
			return err
		}
		if current.Version != input.Version || (current.Status != string(onchain.TransferBroadcast) && current.Status != string(onchain.TransferConfirming)) {
			return ErrOnchainConflict
		}
		winner, err := db.EthereumGasFunding.Query().Where(ethereumgasfunding.IDEQ(input.WinnerID), ethereumgasfunding.IntentIDEQ(current.IntentID), ethereumgasfunding.NonceEQ(current.Nonce), ethereumgasfunding.TransactionHashEqualFold(input.WinnerHash)).Only(txCtx)
		if err != nil {
			return fmt.Errorf("load winning Ethereum gas funding version: %w", err)
		}
		_, err = db.EthereumGasFunding.Update().Where(ethereumgasfunding.IntentIDEQ(current.IntentID), ethereumgasfunding.NonceEQ(current.Nonce), ethereumgasfunding.IDNEQ(winner.ID), ethereumgasfunding.StatusIn(string(onchain.TransferBroadcast), string(onchain.TransferConfirming), string(onchain.TransferReplaced))).SetStatus(string(onchain.TransferReplaced)).SetFinalized(false).ClearNextAttemptAt().Save(txCtx)
		if err != nil {
			return err
		}
		updated, err := db.EthereumGasFunding.Update().Where(ethereumgasfunding.IDEQ(winner.ID)).SetStatus(string(onchain.TransferFinalized)).SetFinalized(true).SetFinalizedBlockHeight(input.BlockHeight).SetFinalizedBlockHash(strings.TrimSpace(input.BlockHash)).SetActualFeeWei(fee.String()).SetFinalizedAt(input.FinalizedAt).ClearFailureCode().ClearFailureReason().ClearNextAttemptAt().AddVersion(1).Save(txCtx)
		return conditionalUpdateResult(updated, err)
	})
}

func (r *OnchainRepository) ListEthereumSweepExecutionTasks(ctx context.Context, network onchain.Network, now time.Time, limit int) ([]onchain.EthereumSweepExecutionTask, error) {
	if (network != onchain.NetworkEthereumMainnet && network != onchain.NetworkEthereumSepolia) || now.IsZero() || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid Ethereum sweep execution query")
	}
	sweeps, err := r.db(ctx).WalletSweep.Query().
		Where(
			walletsweep.NetworkEQ(string(network)),
			walletsweep.StatusIn(string(onchain.TransferPrepared), string(onchain.TransferSigning), string(onchain.TransferBroadcast)),
			walletsweep.Or(walletsweep.NextAttemptAtIsNil(), walletsweep.NextAttemptAtLTE(now)),
		).
		WithIntent().
		Order(dbent.Asc(walletsweep.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query Ethereum sweep execution tasks: %w", err)
	}
	result := make([]onchain.EthereumSweepExecutionTask, 0, len(sweeps))
	for _, sweep := range sweeps {
		intent, edgeErr := sweep.Edges.IntentOrErr()
		if edgeErr != nil {
			return nil, fmt.Errorf("load Ethereum sweep %d intent: %w", sweep.ID, edgeErr)
		}
		unfinishedFunding, countErr := r.db(ctx).EthereumGasFunding.Query().
			Where(
				ethereumgasfunding.IntentIDEQ(intent.ID),
				ethereumgasfunding.StatusNotIn(string(onchain.TransferFinalized), string(onchain.TransferReplaced)),
			).
			Count(ctx)
		if countErr != nil {
			return nil, fmt.Errorf("query unfinished gas funding for Ethereum sweep %d: %w", sweep.ID, countErr)
		}
		if unfinishedFunding > 0 {
			continue
		}
		result = append(result, onchain.EthereumSweepExecutionTask{
			ID: sweep.ID, IntentID: sweep.IntentID, ChainID: sweep.ChainID, DerivationIndex: intent.DerivationIndex,
			SourceAddress: sweep.SourceAddress, DestinationAddress: sweep.DestinationAddress,
			BalanceSnapshotRaw: sweep.BalanceSnapshotRaw, AmountRaw: sweep.AmountRaw,
			IdempotencyKey: sweep.IdempotencyKey, Status: onchain.TransferStatus(sweep.Status),
			RetryCount: sweep.RetryCount, Version: sweep.Version,
		})
		if sweep.TransactionID != nil {
			result[len(result)-1].TransactionHash = *sweep.TransactionID
		}
		if sweep.Nonce != nil {
			result[len(result)-1].Nonce = uint64(*sweep.Nonce)
			versions, versionErr := r.db(ctx).WalletSweep.Query().Where(walletsweep.IntentIDEQ(sweep.IntentID), walletsweep.NonceEQ(*sweep.Nonce), walletsweep.TransactionIDNotNil()).Order(dbent.Asc(walletsweep.FieldID)).All(ctx)
			if versionErr != nil {
				return nil, fmt.Errorf("query Ethereum sweep versions: %w", versionErr)
			}
			for _, item := range versions {
				if item.TransactionID != nil {
					result[len(result)-1].Versions = append(result[len(result)-1].Versions, onchain.EthereumTransactionVersionReference{ID: item.ID, TransactionHash: *item.TransactionID, Status: onchain.TransferStatus(item.Status)})
				}
			}
		}
	}
	return result, nil
}

func (r *OnchainRepository) MarkEthereumSweepSigning(ctx context.Context, taskID int64, version int, requestDigest string) error {
	return r.MarkTRONSweepSigning(ctx, taskID, version, requestDigest)
}

func (r *OnchainRepository) RecordEthereumSweepBroadcast(ctx context.Context, taskID int64, version int, signerAuditID, transactionHash string, nonce uint64) error {
	signerAuditID = strings.TrimSpace(signerAuditID)
	transactionHash = strings.TrimSpace(transactionHash)
	if taskID <= 0 || version < 0 || signerAuditID == "" || len(signerAuditID) > 128 || !common.IsHexHash(transactionHash) || nonce > uint64(^uint64(0)>>1) {
		return fmt.Errorf("invalid Ethereum sweep broadcast result")
	}
	updated, err := r.db(ctx).WalletSweep.Update().Where(
		walletsweep.IDEQ(taskID), walletsweep.VersionEQ(version), walletsweep.StatusEQ(string(onchain.TransferSigning)),
	).SetStatus(string(onchain.TransferBroadcast)).SetSignerAuditID(signerAuditID).SetTransactionID(transactionHash).
		SetNonce(int64(nonce)).ClearFailureCode().ClearFailureReason().ClearNextAttemptAt().AddVersion(1).Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) RecordEthereumSweepReplacement(ctx context.Context, input onchain.EthereumSweepReplacement) error {
	input.SignerAuditID = strings.TrimSpace(input.SignerAuditID)
	input.OriginalTransactionHash = strings.TrimSpace(input.OriginalTransactionHash)
	input.ReplacementTransactionHash = strings.TrimSpace(input.ReplacementTransactionHash)
	if input.TaskID <= 0 || input.Version < 0 || input.SignerAuditID == "" || len(input.SignerAuditID) > 128 || !common.IsHexHash(input.OriginalTransactionHash) || !common.IsHexHash(input.ReplacementTransactionHash) || strings.EqualFold(input.OriginalTransactionHash, input.ReplacementTransactionHash) || input.Nonce > uint64(^uint64(0)>>1) || input.NextAttemptAt.IsZero() {
		return fmt.Errorf("invalid Ethereum sweep replacement")
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		db := txRepo.db(txCtx)
		current, err := db.WalletSweep.Query().Where(walletsweep.IDEQ(input.TaskID)).Only(txCtx)
		if err != nil {
			return fmt.Errorf("load Ethereum sweep replacement source: %w", err)
		}
		if current.Version != input.Version || current.Status != string(onchain.TransferBroadcast) || current.TransactionID == nil || !strings.EqualFold(*current.TransactionID, input.OriginalTransactionHash) || current.Nonce == nil || *current.Nonce != int64(input.Nonce) {
			return ErrOnchainConflict
		}
		archiveKey := fmt.Sprintf("ethereum-sweep-replaced:%d:%s", current.ID, strings.TrimPrefix(strings.ToLower(input.OriginalTransactionHash), "0x"))
		archived, err := db.WalletSweep.Create().SetIntentID(current.IntentID).SetNetwork(current.Network).SetChainID(current.ChainID).
			SetSourceAddress(current.SourceAddress).SetDestinationAddress(current.DestinationAddress).SetBalanceSnapshotRaw(current.BalanceSnapshotRaw).
			SetAmountRaw(current.AmountRaw).SetIdempotencyKey(archiveKey).SetNillableSignerRequestDigest(current.SignerRequestDigest).
			SetNillableSignerAuditID(current.SignerAuditID).SetNillableNonce(current.Nonce).
			SetNillableReplacementOfID(current.ReplacementOfID).SetFeeRaw(current.FeeRaw).SetStatus(string(onchain.TransferReplaced)).Save(txCtx)
		if err != nil {
			return translateOnchainWriteError(err)
		}
		updated, err := db.WalletSweep.Update().Where(walletsweep.IDEQ(current.ID), walletsweep.VersionEQ(current.Version), walletsweep.StatusEQ(string(onchain.TransferBroadcast))).
			SetSignerAuditID(input.SignerAuditID).SetTransactionID(input.ReplacementTransactionHash).SetReplacementOfID(archived.ID).
			SetNonce(int64(input.Nonce)).SetNextAttemptAt(input.NextAttemptAt).ClearFailureCode().ClearFailureReason().AddVersion(1).Save(txCtx)
		if err := conditionalUpdateResult(updated, err); err != nil {
			return err
		}
		archivedUpdated, err := db.WalletSweep.Update().Where(walletsweep.IDEQ(archived.ID), walletsweep.StatusEQ(string(onchain.TransferReplaced))).SetTransactionID(input.OriginalTransactionHash).Save(txCtx)
		return conditionalUpdateResult(archivedUpdated, err)
	})
}

func (r *OnchainRepository) ScheduleEthereumSweepCheck(ctx context.Context, taskID int64, version int, nextAttempt time.Time) error {
	if taskID <= 0 || version < 0 || nextAttempt.IsZero() {
		return fmt.Errorf("invalid Ethereum sweep check schedule")
	}
	updated, err := r.db(ctx).WalletSweep.Update().Where(walletsweep.IDEQ(taskID), walletsweep.VersionEQ(version), walletsweep.StatusEQ(string(onchain.TransferBroadcast))).SetNextAttemptAt(nextAttempt).AddVersion(1).Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) RecordEthereumSweepRetry(ctx context.Context, taskID int64, version int, status onchain.TransferStatus, failureCode, failureReason string, nextAttempt time.Time, review bool) error {
	return r.RecordTRONSweepRetry(ctx, taskID, version, status, failureCode, failureReason, nextAttempt, review)
}

func (r *OnchainRepository) FinalizeEthereumSweep(ctx context.Context, input onchain.EthereumSweepFinalization) error {
	fee, ok := new(big.Int).SetString(strings.TrimSpace(input.ActualFeeWei), 10)
	if input.TaskID <= 0 || input.Version < 0 || input.WinnerID <= 0 || !common.IsHexHash(input.WinnerHash) || input.BlockHeight < 0 || !common.IsHexHash(input.BlockHash) || !ok || fee.Sign() < 0 || input.FinalizedAt.IsZero() {
		return fmt.Errorf("invalid Ethereum sweep finalization")
	}
	return r.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		db := txRepo.db(txCtx)
		current, err := db.WalletSweep.Query().Where(walletsweep.IDEQ(input.TaskID)).Only(txCtx)
		if err != nil {
			return err
		}
		if current.Version != input.Version || current.Status != string(onchain.TransferBroadcast) || current.Nonce == nil {
			return ErrOnchainConflict
		}
		winner, err := db.WalletSweep.Query().Where(walletsweep.IDEQ(input.WinnerID), walletsweep.IntentIDEQ(current.IntentID), walletsweep.NonceEQ(*current.Nonce), walletsweep.TransactionIDEqualFold(input.WinnerHash)).Only(txCtx)
		if err != nil {
			return fmt.Errorf("load winning Ethereum sweep version: %w", err)
		}
		_, err = db.WalletSweep.Update().Where(walletsweep.IntentIDEQ(current.IntentID), walletsweep.NonceEQ(*current.Nonce), walletsweep.IDNEQ(winner.ID), walletsweep.StatusIn(string(onchain.TransferBroadcast), string(onchain.TransferReplaced))).SetStatus(string(onchain.TransferReplaced)).ClearNextAttemptAt().Save(txCtx)
		if err != nil {
			return err
		}
		updated, err := db.WalletSweep.Update().Where(walletsweep.IDEQ(winner.ID)).SetStatus(string(onchain.TransferFinalized)).SetFinalizedBlockHeight(input.BlockHeight).SetFinalizedBlockHash(strings.TrimSpace(input.BlockHash)).SetFeeRaw(fee.String()).SetFinalizedAt(input.FinalizedAt).ClearFailureCode().ClearFailureReason().ClearNextAttemptAt().AddVersion(1).Save(txCtx)
		return conditionalUpdateResult(updated, err)
	})
}

func (r *OnchainRepository) TransitionIntent(ctx context.Context, id int64, version int, from, to onchain.IntentStatus) error {
	if !onchain.CanTransitionIntent(from, to) {
		return invalidTransition(from, to)
	}
	updated, err := r.db(ctx).OnchainPaymentIntent.Update().
		Where(onchainpaymentintent.IDEQ(id), onchainpaymentintent.VersionEQ(version), onchainpaymentintent.StatusEQ(string(from))).
		SetStatus(string(to)).AddVersion(1).Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) TransitionDeposit(ctx context.Context, id int64, version int, from, to onchain.DepositStatus) error {
	if !onchain.CanTransitionDeposit(from, to) {
		return invalidTransition(from, to)
	}
	updated, err := r.db(ctx).OnchainDeposit.Update().
		Where(onchaindeposit.IDEQ(id), onchaindeposit.VersionEQ(version), onchaindeposit.StatusEQ(string(from))).
		SetStatus(string(to)).AddVersion(1).Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) TransitionCursorHealth(ctx context.Context, id int64, version int, from, to onchain.CursorHealth) error {
	if !onchain.CanTransitionCursor(from, to) {
		return invalidTransition(from, to)
	}
	updated, err := r.db(ctx).ChainScanCursor.Update().
		Where(chainscancursor.IDEQ(id), chainscancursor.VersionEQ(version), chainscancursor.HealthEQ(string(from))).
		SetHealth(string(to)).AddVersion(1).Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) TransitionWalletSweep(ctx context.Context, id int64, version int, from, to onchain.TransferStatus) error {
	if !onchain.CanTransitionTransfer(from, to) {
		return invalidTransition(from, to)
	}
	updated, err := r.db(ctx).WalletSweep.Update().
		Where(walletsweep.IDEQ(id), walletsweep.VersionEQ(version), walletsweep.StatusEQ(string(from))).
		SetStatus(string(to)).AddVersion(1).Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) TransitionGasFunding(ctx context.Context, id int64, version int, from, to onchain.TransferStatus) error {
	if !onchain.CanTransitionTransfer(from, to) {
		return invalidTransition(from, to)
	}
	updated, err := r.db(ctx).EthereumGasFunding.Update().
		Where(ethereumgasfunding.IDEQ(id), ethereumgasfunding.VersionEQ(version), ethereumgasfunding.StatusEQ(string(from))).
		SetStatus(string(to)).AddVersion(1).Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) TransitionNonceState(ctx context.Context, id int64, version int, from, to onchain.NonceStatus) error {
	if !onchain.CanTransitionNonce(from, to) {
		return invalidTransition(from, to)
	}
	updated, err := r.db(ctx).EthereumNonceState.Update().
		Where(ethereumnoncestate.IDEQ(id), ethereumnoncestate.VersionEQ(version), ethereumnoncestate.StatusEQ(string(from))).
		SetStatus(string(to)).AddVersion(1).Save(ctx)
	return conditionalUpdateResult(updated, err)
}

func (r *OnchainRepository) AcquireCursorLease(ctx context.Context, network, owner string, now, leaseUntil time.Time) (bool, error) {
	if strings.TrimSpace(network) == "" || strings.TrimSpace(owner) == "" || !leaseUntil.After(now) {
		return false, fmt.Errorf("invalid cursor lease interval")
	}
	updated, err := r.db(ctx).ChainScanCursor.Update().
		Where(
			chainscancursor.NetworkEQ(network),
			chainscancursor.Or(
				chainscancursor.LeaseOwnerEQ(owner),
				chainscancursor.LeaseUntilIsNil(),
				chainscancursor.LeaseUntilLTE(now),
			),
		).
		SetLeaseOwner(owner).
		SetLeaseUntil(leaseUntil).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return updated == 1, nil
}

func (r *OnchainRepository) RenewCursorLease(ctx context.Context, network, owner string, now, leaseUntil time.Time) (bool, error) {
	if strings.TrimSpace(network) == "" || strings.TrimSpace(owner) == "" || !leaseUntil.After(now) {
		return false, fmt.Errorf("invalid cursor lease interval")
	}
	updated, err := r.db(ctx).ChainScanCursor.Update().
		Where(
			chainscancursor.NetworkEQ(network),
			chainscancursor.LeaseOwnerEQ(owner),
			chainscancursor.LeaseUntilGT(now),
		).
		SetLeaseUntil(leaseUntil).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return updated == 1, nil
}

func (r *OnchainRepository) ReleaseCursorLease(ctx context.Context, network, owner string) (bool, error) {
	if strings.TrimSpace(network) == "" || strings.TrimSpace(owner) == "" {
		return false, fmt.Errorf("invalid cursor lease owner")
	}
	updated, err := r.db(ctx).ChainScanCursor.Update().
		Where(chainscancursor.NetworkEQ(network), chainscancursor.LeaseOwnerEQ(owner)).
		ClearLeaseOwner().
		ClearLeaseUntil().
		AddVersion(1).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return updated == 1, nil
}

func (r *OnchainRepository) db(ctx context.Context) *dbent.Client {
	return clientFromContext(ctx, r.client)
}

func translateOnchainWriteError(err error) error {
	if err == nil {
		return nil
	}
	if isUniqueConstraintViolation(err) {
		return fmt.Errorf("%w: %v", ErrOnchainConflict, err)
	}
	return err
}

func conditionalUpdateResult(updated int, err error) error {
	if err != nil {
		return translateOnchainWriteError(err)
	}
	if updated != 1 {
		return ErrOnchainConcurrentUpdate
	}
	return nil
}

func invalidTransition[T ~string](from, to T) error {
	return fmt.Errorf("%w: %s -> %s", ErrOnchainInvalidTransition, from, to)
}
