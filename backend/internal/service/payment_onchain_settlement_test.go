//go:build unit

package service

import (
	"context"
	"math/big"
	"strconv"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/onchainpaymentintent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

type onchainSettlementRedeemRepo struct {
	RedeemCodeRepository
	mu       sync.Mutex
	code     *RedeemCode
	useCalls int
}

func (r *onchainSettlementRedeemRepo) Create(_ context.Context, code *RedeemCode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *code
	clone.ID = 1
	r.code = &clone
	return nil
}

func (r *onchainSettlementRedeemRepo) GetByCode(_ context.Context, code string) (*RedeemCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == nil || r.code.Code != code {
		return nil, ErrRedeemCodeNotFound
	}
	clone := *r.code
	return &clone, nil
}

func (r *onchainSettlementRedeemRepo) GetByID(context.Context, int64) (*RedeemCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == nil {
		return nil, ErrRedeemCodeNotFound
	}
	clone := *r.code
	return &clone, nil
}

func (r *onchainSettlementRedeemRepo) Use(_ context.Context, id, userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == nil || r.code.ID != id || r.code.Status != StatusUnused {
		return ErrRedeemCodeUsed
	}
	now := time.Now().UTC()
	r.code.Status = StatusUsed
	r.code.UsedBy = &userID
	r.code.UsedAt = &now
	r.useCalls++
	return nil
}

type onchainSettlementUserRepo struct {
	UserRepository
	mu       sync.Mutex
	user     *User
	credited float64
}

func (r *onchainSettlementUserRepo) GetByID(context.Context, int64) (*User, error) {
	return r.user, nil
}

func (r *onchainSettlementUserRepo) UpdateBalance(_ context.Context, _ int64, amount float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.credited += amount
	return nil
}

func (r *onchainSettlementUserRepo) creditedAmount() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.credited
}

func TestConfirmOnchainDepositReusesBalanceFulfillmentIdempotently(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	user, order, intent, deposit := createOnchainSettlementFixture(t, ctx, client, "10000000", "10000000")

	redeemRepo := &onchainSettlementRedeemRepo{}
	userRepo := &onchainSettlementUserRepo{user: &User{ID: user.ID, Email: user.Email}}
	redeemService := &RedeemService{
		redeemRepo: redeemRepo, userRepo: userRepo, entClient: client,
	}
	svc := &PaymentService{entClient: client, redeemService: redeemService, userRepo: userRepo}

	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	require.Equal(t, 10.0, userRepo.creditedAmount())
	redeemRepo.mu.Lock()
	require.Equal(t, 1, redeemRepo.useCalls)
	redeemRepo.mu.Unlock()

	loadedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, loadedOrder.Status)
	require.Equal(t, onchainSettlementIdempotencyKey(intent.ID), loadedOrder.PaymentTradeNo)
	loadedIntent, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentSettled), loadedIntent.Status)
	require.Equal(t, "10000000", loadedIntent.CreditedAmountRaw)
	require.NotNil(t, loadedIntent.SettlementIdempotencyKey)
	loadedDeposit, err := client.OnchainDeposit.Get(ctx, deposit.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.DepositCredited), loadedDeposit.Status)
	require.NotNil(t, loadedDeposit.CreditAuditRef)

	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	require.Equal(t, 10.0, userRepo.creditedAmount(), "settled intent must not credit balance twice")
	redeemRepo.mu.Lock()
	require.Equal(t, 1, redeemRepo.useCalls)
	redeemRepo.mu.Unlock()
}

func TestConfirmOnchainDepositCreditsActualOverpayment(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	user, order, intent, _ := createOnchainSettlementFixture(t, ctx, client, "10000000", "15000000")

	redeemRepo := &onchainSettlementRedeemRepo{}
	userRepo := &onchainSettlementUserRepo{user: &User{ID: user.ID, Email: user.Email}}
	svc := &PaymentService{
		entClient: client,
		redeemService: &RedeemService{
			redeemRepo: redeemRepo, userRepo: userRepo, entClient: client,
		},
		userRepo: userRepo,
	}

	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	require.Equal(t, 15.0, userRepo.creditedAmount())
	loadedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, 15.0, loadedOrder.Amount)
	require.Equal(t, 15.0, loadedOrder.PayAmount)
	loadedIntent, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, "15000000", loadedIntent.ReceivedAmountRaw)
	require.Equal(t, "5000000", loadedIntent.OverpaidAmountRaw)
	require.Equal(t, "15000000", loadedIntent.CreditedAmountRaw)
}

func TestConfirmERC20DepositCreditsActualOverpaymentIdempotently(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	user, order, intent, deposit := createOnchainSettlementFixtureForPaymentType(
		t, ctx, client, payment.TypeUSDTERC20, "10000000", "15000000",
	)
	redeemRepo := &onchainSettlementRedeemRepo{}
	userRepo := &onchainSettlementUserRepo{user: &User{ID: user.ID, Email: user.Email}}
	svc := &PaymentService{
		entClient: client,
		redeemService: &RedeemService{
			redeemRepo: redeemRepo, userRepo: userRepo, entClient: client,
		},
		userRepo: userRepo,
	}

	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	require.Equal(t, 15.0, userRepo.creditedAmount())
	redeemRepo.mu.Lock()
	require.Equal(t, 1, redeemRepo.useCalls)
	redeemRepo.mu.Unlock()

	loadedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.TypeUSDTERC20, loadedOrder.PaymentType)
	require.Equal(t, OrderStatusCompleted, loadedOrder.Status)
	require.Equal(t, 15.0, loadedOrder.Amount)
	loadedIntent, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentSettled), loadedIntent.Status)
	require.Equal(t, "5000000", loadedIntent.OverpaidAmountRaw)
	loadedDeposit, err := client.OnchainDeposit.Get(ctx, deposit.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.DepositCredited), loadedDeposit.Status)
}

func TestCalculateOnchainCreditedBalancePreservesFeeAndMultiplierRatio(t *testing.T) {
	// Requested 10, 10% fee => expected payment 11; a 2x multiplier makes
	// the expected credited balance 20. Paying 16.5 is 150% of expected.
	credited, err := calculateOnchainCreditedBalance(20, big.NewInt(16_500_000), big.NewInt(11_000_000))
	require.NoError(t, err)
	require.Equal(t, 30.0, credited)
}

func TestConfirmOnchainDepositRejectsIntegerUnderpayment(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	_, order, intent, _ := createOnchainSettlementFixture(t, ctx, client, "10000000", "9999999")
	svc := &PaymentService{entClient: client}

	err := svc.ConfirmOnchainDeposit(ctx, intent.ID)
	require.Error(t, err)
	loadedOrder, loadErr := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, loadErr)
	require.Equal(t, OrderStatusPartiallyPaid, loadedOrder.Status)
	loadedIntent, loadErr := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, loadErr)
	require.Equal(t, string(onchain.IntentSettlementDue), loadedIntent.Status)
	require.Nil(t, loadedIntent.SettlementIdempotencyKey)
}

func TestConfirmOnchainDepositRejectsNonOnchainPaymentType(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	_, order, intent, _ := createOnchainSettlementFixture(t, ctx, client, "10000000", "10000000")
	_, err := client.PaymentOrder.UpdateOneID(order.ID).SetPaymentType(payment.TypeAlipay).Save(ctx)
	require.NoError(t, err)

	err = (&PaymentService{entClient: client}).ConfirmOnchainDeposit(ctx, intent.ID)
	requireOnchainErrorReason(t, err, "ONCHAIN_ORDER_MISMATCH")
	loadedIntent, loadErr := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, loadErr)
	require.Equal(t, string(onchain.IntentSettlementDue), loadedIntent.Status)
}

func TestConfirmOnchainDepositRecoversExpiredFullyPaidOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	user, order, intent, _ := createOnchainSettlementFixture(t, ctx, client, "10000000", "10000000")
	_, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusExpired).Save(ctx)
	require.NoError(t, err)

	redeemRepo := &onchainSettlementRedeemRepo{}
	userRepo := &onchainSettlementUserRepo{user: &User{ID: user.ID, Email: user.Email}}
	svc := &PaymentService{
		entClient: client,
		redeemService: &RedeemService{
			redeemRepo: redeemRepo, userRepo: userRepo, entClient: client,
		},
		userRepo: userRepo,
	}
	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	loadedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, loadedOrder.Status)
	require.Equal(t, 10.0, userRepo.creditedAmount())
	lateAudits, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
		paymentauditlog.ActionEQ("ONCHAIN_LATE_PAYMENT_RECOVERED"),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, lateAudits)
}

func TestExpireTimedOutOrdersExpiresPartiallyPaidOnchainOrder(t *testing.T) {
	for _, paymentType := range []payment.PaymentType{payment.TypeUSDTTRC20, payment.TypeUSDTERC20} {
		t.Run(string(paymentType), func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			_, order, _, _ := createOnchainSettlementFixtureForPaymentType(t, ctx, client, paymentType, "10000000", "4000000")
			_, err := client.PaymentOrder.UpdateOneID(order.ID).
				SetExpiresAt(time.Now().Add(-time.Minute)).
				Save(ctx)
			require.NoError(t, err)
			svc := &PaymentService{entClient: client}

			expired, err := svc.ExpireTimedOutOrders(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, expired)
			loadedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusExpired, loadedOrder.Status)
		})
	}
}

func TestRetryOnchainSettlementSchedulesImmediateRetry(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	_, order, intent, _ := createOnchainSettlementFixture(t, ctx, client, "10000000", "10000000")
	key := onchainSettlementIdempotencyKey(intent.ID)
	_, err := client.OnchainPaymentIntent.UpdateOneID(intent.ID).
		SetStatus(string(onchain.IntentSettling)).
		SetSettlementIdempotencyKey(key).
		SetNextSettlementAt(time.Now().Add(time.Hour)).
		SetLastErrorCode("BALANCE_UNAVAILABLE").
		SetLastErrorMessage("temporary failure").
		Save(ctx)
	require.NoError(t, err)
	svc := &PaymentService{entClient: client}
	before := time.Now().UTC()

	require.NoError(t, svc.RetryOnchainSettlement(ctx, intent.ID, "admin:7"))
	loaded, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentSettlementDue), loaded.Status)
	require.NotNil(t, loaded.NextSettlementAt)
	require.False(t, loaded.NextSettlementAt.Before(before))
	require.Nil(t, loaded.LastErrorCode)
	audits, err := client.PaymentAuditLog.Query().Where(
		paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
		paymentauditlog.ActionEQ("ONCHAIN_SETTLEMENT_RETRY_REQUESTED"),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, audits)
}

func TestConfirmOnchainDepositRecoversAfterFulfillmentCommit(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	_, order, intent, deposit := createOnchainSettlementFixture(t, ctx, client, "10000000", "10000000")
	key := onchainSettlementIdempotencyKey(intent.ID)
	_, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetStatus(OrderStatusCompleted).
		SetPaymentTradeNo(key).
		SetCompletedAt(time.Now().UTC()).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.OnchainPaymentIntent.UpdateOneID(intent.ID).
		SetStatus(string(onchain.IntentSettling)).
		SetSettlementIdempotencyKey(key).
		Save(ctx)
	require.NoError(t, err)
	redeemRepo := &onchainSettlementRedeemRepo{code: &RedeemCode{
		ID: 1, Code: order.RechargeCode, Type: RedeemTypeBalance, Value: order.Amount, Status: StatusUsed,
	}}
	svc := &PaymentService{entClient: client, redeemService: &RedeemService{redeemRepo: redeemRepo}}

	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	loadedIntent, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentSettled), loadedIntent.Status)
	loadedDeposit, err := client.OnchainDeposit.Get(ctx, deposit.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.DepositCredited), loadedDeposit.Status)
	redeemRepo.mu.Lock()
	require.Zero(t, redeemRepo.useCalls, "recovery must not repeat an already committed fulfillment")
	redeemRepo.mu.Unlock()
}

func TestConfirmERC20DepositRecoversAfterFulfillmentCommit(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	_, order, intent, deposit := createOnchainSettlementFixtureForPaymentType(
		t, ctx, client, payment.TypeUSDTERC20, "10000000", "10000000",
	)
	key := onchainSettlementIdempotencyKey(intent.ID)
	_, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetStatus(OrderStatusCompleted).
		SetPaymentTradeNo(key).
		SetCompletedAt(time.Now().UTC()).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.OnchainPaymentIntent.UpdateOneID(intent.ID).
		SetStatus(string(onchain.IntentSettling)).
		SetSettlementIdempotencyKey(key).
		Save(ctx)
	require.NoError(t, err)
	redeemRepo := &onchainSettlementRedeemRepo{code: &RedeemCode{
		ID: 1, Code: order.RechargeCode, Type: RedeemTypeBalance, Value: order.Amount, Status: StatusUsed,
	}}
	svc := &PaymentService{entClient: client, redeemService: &RedeemService{redeemRepo: redeemRepo}}

	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	loadedIntent, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentSettled), loadedIntent.Status)
	loadedDeposit, err := client.OnchainDeposit.Get(ctx, deposit.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.DepositCredited), loadedDeposit.Status)
	redeemRepo.mu.Lock()
	require.Zero(t, redeemRepo.useCalls, "ERC20 recovery must not repeat an already committed fulfillment")
	redeemRepo.mu.Unlock()
}

func TestConfirmOnchainDepositConcurrentWorkersCreditOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	user, order, intent, _ := createOnchainSettlementFixture(t, ctx, client, "10000000", "10000000")
	redeemRepo := &onchainSettlementRedeemRepo{}
	userRepo := &onchainSettlementUserRepo{user: &User{ID: user.ID, Email: user.Email}}
	svc := &PaymentService{
		entClient: client,
		redeemService: &RedeemService{
			redeemRepo: redeemRepo, userRepo: userRepo, entClient: client,
		},
		userRepo: userRepo,
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- svc.ConfirmOnchainDeposit(ctx, intent.ID)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for range results {
		// A competing worker may observe the fulfillment lease conflict. The
		// persisted state is retried below and must converge without a second credit.
	}
	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	require.Equal(t, 10.0, userRepo.creditedAmount())
	redeemRepo.mu.Lock()
	require.Equal(t, 1, redeemRepo.useCalls)
	redeemRepo.mu.Unlock()
	loadedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, loadedOrder.Status)
}

func TestConfirmERC20DepositConcurrentWorkersCreditOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	user, order, intent, _ := createOnchainSettlementFixtureForPaymentType(
		t, ctx, client, payment.TypeUSDTERC20, "10000000", "10000000",
	)
	redeemRepo := &onchainSettlementRedeemRepo{}
	userRepo := &onchainSettlementUserRepo{user: &User{ID: user.ID, Email: user.Email}}
	svc := &PaymentService{
		entClient: client,
		redeemService: &RedeemService{
			redeemRepo: redeemRepo, userRepo: userRepo, entClient: client,
		},
		userRepo: userRepo,
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- svc.ConfirmOnchainDeposit(ctx, intent.ID)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for range results {
	}
	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
	require.Equal(t, 10.0, userRepo.creditedAmount())
	redeemRepo.mu.Lock()
	require.Equal(t, 1, redeemRepo.useCalls)
	redeemRepo.mu.Unlock()
	loadedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, loadedOrder.Status)
}

func TestTRONDailyLimitBypassAllowsRepeatedCompletedRecharges(t *testing.T) {
	ctx := context.Background()
	svc, client, user, _, selection := newTRONOrderTestService(t, nil)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	redeemRepo := &onchainSettlementRedeemRepo{}
	userRepo := &onchainSettlementUserRepo{user: user}
	svc.redeemService = &RedeemService{redeemRepo: redeemRepo, userRepo: userRepo, entClient: client}
	svc.userRepo = userRepo

	first, err := createTRONOrderForTest(ctx, svc, user, selection, 100, "100")
	require.NoError(t, err)
	confirmTRONOrderForTest(t, ctx, svc, client, first.OrderID, "daily-first", "100000000")
	second, err := createTRONOrderForTest(ctx, svc, user, selection, 100, "100")
	require.NoError(t, err)
	confirmTRONOrderForTest(t, ctx, svc, client, second.OrderID, "daily-second", "100000000")

	third, err := createTRONOrderForTest(ctx, svc, user, selection, 100, "100")
	require.NoError(t, err)
	require.NotEqual(t, first.OnchainPayment.Address, second.OnchainPayment.Address)
	require.NotEqual(t, second.OnchainPayment.Address, third.OnchainPayment.Address)
	require.Equal(t, 200.0, userRepo.creditedAmount())
	completed, err := client.PaymentOrder.Query().Where(paymentorder.StatusEQ(OrderStatusCompleted)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, completed)
}

func confirmTRONOrderForTest(t *testing.T, ctx context.Context, svc *PaymentService, client *dbent.Client, orderID int64, transactionID, amountRaw string) {
	t.Helper()
	intent, err := client.OnchainPaymentIntent.Query().Where(onchainpaymentintent.PaymentOrderIDEQ(orderID)).Only(ctx)
	require.NoError(t, err)
	_, err = client.OnchainDeposit.Create().
		SetIntentID(intent.ID).
		SetPaymentOrderID(orderID).
		SetUserID(intent.UserID).
		SetNetwork(intent.Network).
		SetChainID(intent.ChainID).
		SetTransactionID(transactionID).
		SetLogIndex(0).
		SetTransactionIndex(0).
		SetBlockHeight(100).
		SetBlockHash("block-100-" + transactionID).
		SetTokenContract(intent.TokenContract).
		SetFromAddress("sender").
		SetToAddress(intent.DepositAddress).
		SetAmountRaw(amountRaw).
		SetTransactionTime(time.Now().UTC()).
		SetReceiptSuccess(true).
		SetFinalized(true).
		SetStatus(string(onchain.DepositCreditPending)).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.OnchainPaymentIntent.UpdateOneID(intent.ID).
		SetReceivedAmountRaw(amountRaw).
		SetStatus(string(onchain.IntentSettlementDue)).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmOnchainDeposit(ctx, intent.ID))
}

func createOnchainSettlementFixture(t *testing.T, ctx context.Context, client *dbent.Client, expectedRaw, depositRaw string) (*dbent.User, *dbent.PaymentOrder, *dbent.OnchainPaymentIntent, *dbent.OnchainDeposit) {
	return createOnchainSettlementFixtureForPaymentType(t, ctx, client, payment.TypeUSDTTRC20, expectedRaw, depositRaw)
}

func createOnchainSettlementFixtureForPaymentType(t *testing.T, ctx context.Context, client *dbent.Client, paymentType payment.PaymentType, expectedRaw, depositRaw string) (*dbent.User, *dbent.PaymentOrder, *dbent.OnchainPaymentIntent, *dbent.OnchainDeposit) {
	t.Helper()
	network := onchain.NetworkTronMainnet
	chainID := int64(0)
	contract := onchain.TronMainnetUSDTContract
	depositAddress := "TSettlement" + depositRaw
	if paymentType == payment.TypeUSDTERC20 {
		network = onchain.NetworkEthereumMainnet
		chainID = int64(onchain.EthereumMainnetChainID)
		contract = onchain.EthereumMainnetUSDTContract
		depositAddress = "0x0000000000000000000000000000000000000001"
	}
	user, err := client.User.Create().
		SetEmail("onchain-settlement-" + depositRaw + "@example.com").
		SetPasswordHash("test-password-hash").
		SetUsername("onchain-settlement").
		Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(10).
		SetPayAmount(10).
		SetRechargeCode("ONCHAIN-SETTLEMENT-" + depositRaw).
		SetPaymentType(paymentType).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(OrderStatusPartiallyPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("localhost").
		Save(ctx)
	require.NoError(t, err)
	intent, err := client.OnchainPaymentIntent.Create().
		SetPaymentOrderID(order.ID).
		SetUserID(user.ID).
		SetNetwork(string(network)).
		SetChainID(chainID).
		SetTokenContract(contract).
		SetDepositAddress(depositAddress).
		SetDerivationIndex(order.ID).
		SetExpectedAmountRaw(expectedRaw).
		SetReceivedAmountRaw(depositRaw).
		SetConfigSnapshot(map[string]any{"network": string(network)}).
		SetConfigVersion("test-v1").
		SetStatus(string(onchain.IntentSettlementDue)).
		Save(ctx)
	require.NoError(t, err)
	deposit, err := client.OnchainDeposit.Create().
		SetIntentID(intent.ID).
		SetPaymentOrderID(order.ID).
		SetUserID(user.ID).
		SetNetwork(intent.Network).
		SetChainID(intent.ChainID).
		SetTransactionID("tx-" + depositRaw).
		SetLogIndex(0).
		SetTransactionIndex(0).
		SetBlockHeight(100).
		SetBlockHash("block-100").
		SetTokenContract(intent.TokenContract).
		SetFromAddress("sender").
		SetToAddress(intent.DepositAddress).
		SetAmountRaw(depositRaw).
		SetTransactionTime(time.Now().UTC()).
		SetReceiptSuccess(true).
		SetFinalized(true).
		SetStatus(string(onchain.DepositCreditPending)).
		Save(ctx)
	require.NoError(t, err)
	return user, order, intent, deposit
}
