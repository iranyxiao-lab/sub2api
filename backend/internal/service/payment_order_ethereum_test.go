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
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/stretchr/testify/require"
)

type ethereumOrderRepositoryTestDouble struct {
	failIntent      bool
	allocationCalls int
	intentCalls     int
}

func (r *ethereumOrderRepositoryTestDouble) AllocateEthereumAddressForOrder(ctx context.Context, network onchain.Network, xpub string) (EthereumAddressAllocation, error) {
	r.allocationCalls++
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return EthereumAddressAllocation{}, fmt.Errorf("transaction context is required")
	}
	db := tx.Client()
	cursor, err := db.WalletDerivationCursor.Query().Where(walletderivationcursor.NetworkEQ(string(network))).Only(ctx)
	if dbent.IsNotFound(err) {
		cursor, err = db.WalletDerivationCursor.Create().SetNetwork(string(network)).SetNextIndex(0).Save(ctx)
	}
	if err != nil {
		return EthereumAddressAllocation{}, err
	}
	index := cursor.NextIndex
	if _, err := db.WalletDerivationCursor.UpdateOneID(cursor.ID).SetNextIndex(index + 1).Save(ctx); err != nil {
		return EthereumAddressAllocation{}, err
	}
	address, err := onchain.DeriveEthereumAddress(xpub, uint32(index))
	if err != nil {
		return EthereumAddressAllocation{}, err
	}
	return EthereumAddressAllocation{Network: network, Index: uint32(index), Address: address}, nil
}

func (r *ethereumOrderRepositoryTestDouble) CreateOnchainPaymentIntent(ctx context.Context, input OnchainPaymentIntentCreate) error {
	r.intentCalls++
	if r.failIntent {
		return errors.New("injected intent failure")
	}
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return fmt.Errorf("transaction context is required")
	}
	_, err := tx.Client().OnchainPaymentIntent.Create().
		SetPaymentOrderID(input.PaymentOrderID).SetUserID(input.UserID).
		SetNetwork(input.Network).SetChainID(input.ChainID).SetTokenContract(input.TokenContract).
		SetDepositAddress(input.DepositAddress).SetDerivationIndex(input.DerivationIndex).
		SetExpectedAmountRaw(input.ExpectedAmountRaw).SetConfigSnapshot(input.ConfigSnapshot).
		SetConfigVersion(input.ConfigVersion).Save(ctx)
	return err
}

func TestCreateEthereumOrderAtomicallyPersistsOrderIntentAddressAndSnapshot(t *testing.T) {
	ctx := context.Background()
	svc, client, user, repo, selection := newEthereumOrderTestService(t, false)
	resp, err := createEthereumOrderForTest(ctx, svc, user, selection, 12.345678, "12.345678")
	require.NoError(t, err)
	require.Equal(t, "0xcc0a50F5A968c28A4eBa361863a1068CEfC84680", resp.OnchainPayment.Address)
	require.Equal(t, "12.345678", resp.OnchainPayment.Amount)
	require.Equal(t, uint64(1), resp.OnchainPayment.ChainID)
	require.Equal(t, 1, repo.allocationCalls)
	require.Equal(t, 1, repo.intentCalls)

	order, err := client.PaymentOrder.Get(ctx, resp.OrderID)
	require.NoError(t, err)
	require.Equal(t, payment.TypeUSDTERC20, order.PaymentType)
	require.Equal(t, string(onchain.NetworkEthereumMainnet), order.ProviderSnapshot["network"])
	require.EqualValues(t, 1, order.ProviderSnapshot["chain_id"])
	require.Equal(t, onchain.EthereumMainnetUSDTContract, order.ProviderSnapshot["token_contract"])
	require.Equal(t, resp.OnchainPayment.Address, order.ProviderSnapshot["deposit_address"])
	require.Equal(t, "12345678", order.ProviderSnapshot["expected_amount_raw"])
	require.NotContains(t, order.ProviderSnapshot, "xpub")
	require.NotContains(t, order.ProviderSnapshot, "private_key")

	intent, err := client.OnchainPaymentIntent.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), intent.ChainID)
	require.Equal(t, int64(0), intent.DerivationIndex)
	require.Equal(t, resp.OnchainPayment.Address, intent.DepositAddress)
	require.Equal(t, "12345678", intent.ExpectedAmountRaw)
}

func TestCreateEthereumOrderRollsBackOrderIntentAndCursorOnFailure(t *testing.T) {
	ctx := context.Background()
	svc, client, user, repo, selection := newEthereumOrderTestService(t, true)
	_, err := createEthereumOrderForTest(ctx, svc, user, selection, 10, "10")
	require.ErrorContains(t, err, "create Ethereum payment intent")
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

func TestCreateOrderRejectsEthereumSubscriptionBeforeLoadingConfigOrAllocatingAddress(t *testing.T) {
	svc := &PaymentService{}
	_, err := svc.CreateOrder(context.Background(), CreateOrderRequest{
		PaymentType: payment.TypeUSDTERC20, OrderType: payment.OrderTypeSubscription, PlanID: 1,
	})
	requireOnchainErrorReason(t, err, "ONCHAIN_BALANCE_RECHARGE_ONLY")
}

func TestCreateEthereumOrderRejectsExcessPrecisionBeforeAllocatingAddress(t *testing.T) {
	ctx := context.Background()
	svc, client, user, repo, selection := newEthereumOrderTestService(t, false)

	_, err := createEthereumOrderForTest(ctx, svc, user, selection, 10, "10.0000001")
	requireOnchainErrorReason(t, err, "INVALID_AMOUNT")
	require.Zero(t, repo.allocationCalls)
	require.Zero(t, repo.intentCalls)

	orderCount, countErr := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, countErr)
	require.Zero(t, orderCount)
	cursorCount, countErr := client.WalletDerivationCursor.Query().Count(ctx)
	require.NoError(t, countErr)
	require.Zero(t, cursorCount)
}

func TestCreateEthereumOrderBypassesDailyLimitButKeepsExplicitProtections(t *testing.T) {
	ctx := context.Background()
	svc, client, user, _, selection := newEthereumOrderTestService(t, false)

	first, err := createEthereumOrderForTest(ctx, svc, user, selection, 100, "100")
	require.NoError(t, err)
	_, err = client.PaymentOrder.UpdateOneID(first.OrderID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	second, err := createEthereumOrderForTest(ctx, svc, user, selection, 100, "100")
	require.NoError(t, err, "global daily limit is 1 but ERC20 must remain unlimited")
	require.NotEqual(t, first.OnchainPayment.Address, second.OnchainPayment.Address)
	_, err = createEthereumOrderForTest(ctx, svc, user, selection, 100, "100")
	require.NoError(t, err)
	_, err = createEthereumOrderForTest(ctx, svc, user, selection, 100, "100")
	requireOnchainErrorReason(t, err, "TOO_MANY_PENDING")

	_, err = createEthereumOrderForTest(ctx, svc, user, selection, 9, "9")
	requireOnchainErrorReason(t, err, "INVALID_AMOUNT")
	svc.ethereumOrderHealthCheck = func(context.Context) (onchain.EthereumStartupReport, error) {
		return onchain.EthereumStartupReport{}, errors.New("finalized unavailable")
	}
	_, err = createEthereumOrderForTest(ctx, svc, user, selection, 100, "100")
	requireOnchainErrorReason(t, err, onchain.EthereumRechargeUnavailable)
}

type ethereumCursorHealthReaderStub struct {
	cursor onchain.EthereumScanCursor
	err    error
}

func (s ethereumCursorHealthReaderStub) LoadEthereumScanCursor(context.Context, onchain.Network) (onchain.EthereumScanCursor, error) {
	return s.cursor, s.err
}

func TestDeploymentEthereumOrderHealthCheckRejectsPersistentHashConflict(t *testing.T) {
	cfg := validEthereumOrderTestDeployment(t)
	cfg.ScannerEnabled = true
	cfg.PrimaryRPCURL = "https://geth.internal.example"
	cfg.BackupRPCURL = "https://nethermind.internal.example"
	cfg.RequestTimeoutSeconds = 1
	cfg.ResponseMaxBytes = 4096
	cfg.RPCBatchLimit = 10
	check := newDeploymentEthereumOrderHealthCheck(cfg, ethereumCursorHealthReaderStub{
		cursor: onchain.EthereumScanCursor{Health: onchain.CursorHashConflict},
	})

	_, err := check(context.Background())
	require.ErrorIs(t, err, onchain.ErrEthereumScanHashConflict)
}

func newEthereumOrderTestService(t *testing.T, failIntent bool) (*PaymentService, *dbent.Client, *User, *ethereumOrderRepositoryTestDouble, *payment.InstanceSelection) {
	t.Helper()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	entUser, err := client.User.Create().SetEmail("ethereum-order@example.com").SetPasswordHash("hash").SetUsername("ethereum-order-user").Save(ctx)
	require.NoError(t, err)
	repo := &ethereumOrderRepositoryTestDouble{failIntent: failIntent}
	svc := &PaymentService{entClient: client}
	svc.SetEthereumOrderDependencies(repo, validEthereumOrderTestDeployment(t), func(context.Context) (onchain.EthereumStartupReport, error) {
		return onchain.EthereumStartupReport{}, nil
	})
	return svc, client, &User{ID: entUser.ID, Email: entUser.Email, Username: entUser.Username}, repo, validEthereumOrderTestSelection()
}

func createEthereumOrderForTest(ctx context.Context, svc *PaymentService, user *User, selection *payment.InstanceSelection, amount float64, payAmount string) (*CreateOrderResponse, error) {
	return svc.createEthereumOrder(ctx, CreateOrderRequest{
		UserID: user.ID, Amount: amount, PaymentType: payment.TypeUSDTERC20,
		OrderType: payment.OrderTypeBalance, ClientIP: "127.0.0.1", SrcHost: "app.example.com",
	}, user, &PaymentConfig{MaxPendingOrders: 2, OrderTimeoutMin: 30, DailyLimit: 1}, amount, amount, 0, payAmount, amount, selection)
}

func validEthereumOrderTestDeployment(t *testing.T) config.SelfHostedEthereumConfig {
	t.Helper()
	return config.SelfHostedEthereumConfig{
		Enabled: true, OrderCreationEnabled: true, Network: string(onchain.NetworkEthereumMainnet),
		ChainID: onchain.EthereumMainnetChainID, USDTContract: onchain.EthereumMainnetUSDTContract,
		USDTDecimals: onchain.USDTDecimals, XPub: ethereumOrderTestXPub(t), ConfigVersion: config.DefaultEthereumConfigVersion,
	}
}

func validEthereumOrderTestSelection() *payment.InstanceSelection {
	return &payment.InstanceSelection{
		InstanceID: "ethereum-1", ProviderKey: payment.TypeUSDTERC20,
		SupportedTypes: payment.TypeUSDTERC20, PaymentMode: "qrcode",
		Config: map[string]string{
			"network": string(onchain.NetworkEthereumMainnet), "chainId": "1",
			"usdtContract": onchain.EthereumMainnetUSDTContract, "usdtDecimals": "6",
			"configVersion": config.DefaultEthereumConfigVersion,
		},
	}
}

func ethereumOrderTestXPub(t *testing.T) string {
	t.Helper()
	key, err := hdkeychain.NewMaster([]byte("0123456789abcdef0123456789abcdef"), &chaincfg.MainNetParams)
	require.NoError(t, err)
	defer key.Zero()
	for _, index := range []uint32{hdkeychain.HardenedKeyStart + 44, hdkeychain.HardenedKeyStart + 60, hdkeychain.HardenedKeyStart, 0} {
		child, deriveErr := key.Derive(index)
		require.NoError(t, deriveErr)
		key.Zero()
		key = child
	}
	publicKey, err := key.Neuter()
	require.NoError(t, err)
	defer publicKey.Zero()
	return publicKey.String()
}
