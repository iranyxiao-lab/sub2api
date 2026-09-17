package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/walletderivationcursor"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

const tronOrderTestXPub = "xpub6EFn6HriJB8wve2KZNPPbwEbwHJ5fmRGBrum7xyjaxzHPvtnns3ab3fpecQF8ZWoy3PnazqxAq6JazJDnNYrkp61ZX94fayC3hU6bdhjS52"

type tronOrderRepositoryTestDouble struct {
	failIntent      bool
	allocationCalls int
	intentCalls     int
}

func (r *tronOrderRepositoryTestDouble) AllocateTRONAddressForOrder(ctx context.Context, network onchain.Network, xpub string) (TRONAddressAllocation, error) {
	r.allocationCalls++
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return TRONAddressAllocation{}, fmt.Errorf("transaction context is required")
	}
	db := tx.Client()
	cursor, err := db.WalletDerivationCursor.Query().Where(walletderivationcursor.NetworkEQ(string(network))).Only(ctx)
	if dbent.IsNotFound(err) {
		cursor, err = db.WalletDerivationCursor.Create().SetNetwork(string(network)).SetNextIndex(0).Save(ctx)
	}
	if err != nil {
		return TRONAddressAllocation{}, err
	}
	index := cursor.NextIndex
	if _, err := db.WalletDerivationCursor.UpdateOneID(cursor.ID).SetNextIndex(index + 1).Save(ctx); err != nil {
		return TRONAddressAllocation{}, err
	}
	address, err := onchain.DeriveTRONAddress(xpub, uint32(index))
	if err != nil {
		return TRONAddressAllocation{}, err
	}
	return TRONAddressAllocation{Network: network, Index: uint32(index), Address: address}, nil
}

func (r *tronOrderRepositoryTestDouble) CreateOnchainPaymentIntent(ctx context.Context, input OnchainPaymentIntentCreate) error {
	r.intentCalls++
	if r.failIntent {
		return errors.New("injected intent failure")
	}
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return fmt.Errorf("transaction context is required")
	}
	_, err := tx.Client().OnchainPaymentIntent.Create().
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
	return err
}

func TestCreateTRONOrderAtomicallyPersistsOrderIntentAddressAndSnapshot(t *testing.T) {
	ctx := context.Background()
	svc, client, user, repo, selection := newTRONOrderTestService(t, nil)

	resp, err := createTRONOrderForTest(ctx, svc, user, selection, 12.345678, "12.345678")
	require.NoError(t, err)
	require.Positive(t, resp.OrderID)
	require.Equal(t, "TD8icGtfdGTCyxRvsoszeaKbTdrFHFfkbk", resp.OnchainPayment.Address)
	require.Equal(t, "12.345678", resp.OnchainPayment.Amount)
	require.Equal(t, resp.OnchainPayment.Address, resp.OnchainPayment.QRCode)
	require.Equal(t, "0", resp.OnchainPayment.ReceivedAmount)
	require.Equal(t, "12.345678", resp.OnchainPayment.PendingAmount)
	require.Equal(t, 1, repo.allocationCalls)
	require.Equal(t, 1, repo.intentCalls)

	order, err := client.PaymentOrder.Get(ctx, resp.OrderID)
	require.NoError(t, err)
	require.Equal(t, payment.TypeUSDTTRC20, order.PaymentType)
	require.Equal(t, "tron-mainnet", order.ProviderSnapshot["network"])
	require.Equal(t, onchain.TronMainnetUSDTContract, order.ProviderSnapshot["token_contract"])
	require.Equal(t, resp.OnchainPayment.Address, order.ProviderSnapshot["deposit_address"])
	require.Equal(t, "12345678", order.ProviderSnapshot["expected_amount_raw"])
	require.NotContains(t, order.ProviderSnapshot, "xpub")
	require.NotContains(t, order.ProviderSnapshot, "private_key")

	intent, err := client.OnchainPaymentIntent.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, order.ID, intent.PaymentOrderID)
	require.Equal(t, int64(0), intent.DerivationIndex)
	require.Equal(t, resp.OnchainPayment.Address, intent.DepositAddress)
	require.Equal(t, "12345678", intent.ExpectedAmountRaw)
	cursor, err := client.WalletDerivationCursor.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), cursor.NextIndex)
}

func TestCreateTRONOrderRollsBackOrderIntentAndCursorOnFailure(t *testing.T) {
	ctx := context.Background()
	svc, client, user, repo, selection := newTRONOrderTestService(t, errors.New("injected intent failure"))

	_, err := createTRONOrderForTest(ctx, svc, user, selection, 10, "10")
	require.ErrorContains(t, err, "create TRON payment intent")
	require.Equal(t, 1, repo.allocationCalls)
	require.Equal(t, 1, repo.intentCalls)

	orderCount, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, orderCount)
	intentCount, err := client.OnchainPaymentIntent.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, intentCount)
	cursorCount, err := client.WalletDerivationCursor.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, cursorCount)
}

func TestCreateTRONOrderRejectsUnavailableConfigAndExcessPrecisionBeforeAllocation(t *testing.T) {
	ctx := context.Background()
	t.Run("health unavailable", func(t *testing.T) {
		svc, client, user, repo, selection := newTRONOrderTestService(t, errors.New("node unavailable"))
		_, err := createTRONOrderForTest(ctx, svc, user, selection, 10, "10")
		requireOnchainErrorReason(t, err, onchain.TRONRechargeUnavailable)
		require.Zero(t, repo.allocationCalls)
		count, countErr := client.PaymentOrder.Query().Count(ctx)
		require.NoError(t, countErr)
		require.Zero(t, count)
	})

	t.Run("excess precision", func(t *testing.T) {
		svc, _, user, repo, selection := newTRONOrderTestService(t, nil)
		_, err := createTRONOrderForTest(ctx, svc, user, selection, 10, "10.0000001")
		requireOnchainErrorReason(t, err, "INVALID_AMOUNT")
		require.Zero(t, repo.allocationCalls)
	})

	t.Run("non TRON network", func(t *testing.T) {
		svc, _, user, repo, selection := newTRONOrderTestService(t, nil)
		selection.Config["network"] = string(onchain.NetworkEthereumMainnet)
		_, err := createTRONOrderForTest(ctx, svc, user, selection, 10, "10")
		requireOnchainErrorReason(t, err, "PAYMENT_PROVIDER_MISCONFIGURED")
		require.Zero(t, repo.allocationCalls)
	})
}

func TestCreateOrderRejectsTRONSubscriptionBeforeLoadingConfigOrAllocatingAddress(t *testing.T) {
	svc := &PaymentService{}
	_, err := svc.CreateOrder(context.Background(), CreateOrderRequest{
		PaymentType: payment.TypeUSDTTRC20,
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      1,
	})
	requireOnchainErrorReason(t, err, "ONCHAIN_BALANCE_RECHARGE_ONLY")
}

func TestCreateTRONOrderBypassesGlobalDailyLimitButKeepsPendingLimit(t *testing.T) {
	ctx := context.Background()
	svc, client, user, _, selection := newTRONOrderTestService(t, nil)

	first, err := createTRONOrderForTest(ctx, svc, user, selection, 100, "100")
	require.NoError(t, err)
	_, err = client.PaymentOrder.UpdateOneID(first.OrderID).
		SetStatus(OrderStatusPaid).
		SetPaidAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	second, err := createTRONOrderForTest(ctx, svc, user, selection, 100, "100")
	require.NoError(t, err)
	require.NotEqual(t, first.OrderID, second.OrderID)
	require.NotEqual(t, first.OnchainPayment.Address, second.OnchainPayment.Address)

	third, err := createTRONOrderForTest(ctx, svc, user, selection, 100, "100")
	require.NoError(t, err)
	_, err = createTRONOrderForTest(ctx, svc, user, selection, 100, "100")
	requireOnchainErrorReason(t, err, "TOO_MANY_PENDING")
	require.NotZero(t, third.OrderID)
}

func newTRONOrderTestService(t *testing.T, injectedError error) (*PaymentService, *dbent.Client, *User, *tronOrderRepositoryTestDouble, *payment.InstanceSelection) {
	t.Helper()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	entUser, err := client.User.Create().
		SetEmail("tron-order@example.com").
		SetPasswordHash("hash").
		SetUsername("tron-order-user").
		Save(ctx)
	require.NoError(t, err)

	failIntent := injectedError != nil && injectedError.Error() == "injected intent failure"
	healthError := injectedError
	if failIntent {
		healthError = nil
	}
	repo := &tronOrderRepositoryTestDouble{failIntent: failIntent}
	healthCheck := TRONOrderHealthCheck(func(context.Context) (onchain.TRONHealthReport, error) {
		return onchain.TRONHealthReport{Healthy: healthError == nil}, healthError
	})
	svc := &PaymentService{entClient: client}
	svc.SetTRONOrderDependencies(repo, validTRONOrderTestDeployment(), healthCheck)
	return svc, client, &User{ID: entUser.ID, Email: entUser.Email, Username: entUser.Username}, repo, validTRONOrderTestSelection()
}

func createTRONOrderForTest(ctx context.Context, svc *PaymentService, user *User, selection *payment.InstanceSelection, amount float64, payAmount string) (*CreateOrderResponse, error) {
	return svc.createTRONOrder(ctx, CreateOrderRequest{
		UserID: user.ID, Amount: amount, PaymentType: payment.TypeUSDTTRC20,
		OrderType: payment.OrderTypeBalance, ClientIP: "127.0.0.1", SrcHost: "app.example.com",
	}, user, &PaymentConfig{
		MaxPendingOrders: 2, OrderTimeoutMin: 30, DailyLimit: 1,
	}, amount, amount, 0, payAmount, amount, selection)
}

func validTRONOrderTestDeployment() config.SelfHostedTRONConfig {
	return config.SelfHostedTRONConfig{
		Enabled: true, OrderCreationEnabled: true, Network: string(onchain.NetworkTronMainnet),
		USDTContract: onchain.TronMainnetUSDTContract, USDTDecimals: onchain.USDTDecimals,
		XPub: tronOrderTestXPub, ConfigVersion: config.DefaultTRONConfigVersion,
	}
}

func validTRONOrderTestSelection() *payment.InstanceSelection {
	return &payment.InstanceSelection{
		InstanceID: "tron-1", ProviderKey: payment.TypeUSDTTRC20,
		SupportedTypes: payment.TypeUSDTTRC20, PaymentMode: "qrcode",
		Config: map[string]string{
			"network": string(onchain.NetworkTronMainnet), "usdtContract": onchain.TronMainnetUSDTContract,
			"usdtDecimals": "6", "configVersion": config.DefaultTRONConfigVersion,
		},
	}
}

func requireOnchainErrorReason(t *testing.T, err error, reason string) {
	t.Helper()
	require.Error(t, err)
	appErr := new(infraerrors.ApplicationError)
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, reason, appErr.Reason)
}
