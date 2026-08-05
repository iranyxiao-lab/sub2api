package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/onchaindeposit"
	"github.com/Wei-Shaw/sub2api/ent/onchainpaymentintent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/walletsweep"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

var onchainRepoTestSequence atomic.Uint64

func newOnchainRepoSQLite(t *testing.T) (*OnchainRepository, *dbent.Client) {
	t.Helper()
	dsn := fmt.Sprintf("file:onchain_repo_%d?mode=memory&cache=shared", onchainRepoTestSequence.Add(1))
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })
	return NewOnchainRepository(client), client
}

func createOnchainTestIntent(t *testing.T, ctx context.Context, repo *OnchainRepository, client *dbent.Client, suffix string) *dbent.OnchainPaymentIntent {
	t.Helper()
	user, err := client.User.Create().
		SetEmail("onchain-" + suffix + "@example.com").
		SetPasswordHash("test-password-hash").
		SetUsername("onchain-" + suffix).
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(10).
		SetPayAmount(10).
		SetRechargeCode("recharge-" + suffix).
		SetPaymentType("usdt_trc20").
		SetPaymentTradeNo("trade-" + suffix).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("localhost").
		Save(ctx)
	require.NoError(t, err)

	intent, err := repo.CreatePaymentIntent(ctx, PaymentIntentCreate{
		PaymentOrderID:    order.ID,
		UserID:            user.ID,
		Network:           "tron-mainnet",
		ChainID:           0,
		TokenContract:     "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
		DepositAddress:    "address-" + suffix,
		DerivationIndex:   order.ID,
		ExpectedAmountRaw: "10000000",
		ConfigSnapshot:    map[string]any{"network": "tron-mainnet"},
		ConfigVersion:     "test-v1",
	})
	require.NoError(t, err)
	return intent
}

func TestOnchainRepositoryTranslatesUniqueConflicts(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "unique")

	_, err := repo.CreatePaymentIntent(ctx, PaymentIntentCreate{
		PaymentOrderID:    intent.PaymentOrderID,
		UserID:            intent.UserID,
		Network:           "tron-mainnet",
		ChainID:           0,
		TokenContract:     intent.TokenContract,
		DepositAddress:    "another-address",
		DerivationIndex:   intent.DerivationIndex + 1,
		ExpectedAmountRaw: "10000000",
		ConfigSnapshot:    map[string]any{},
		ConfigVersion:     "test-v1",
	})
	require.ErrorIs(t, err, ErrOnchainConflict)
}

func TestOnchainRepositoryAllocatesUniqueTRONAddressesConcurrently(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	const xpub = "xpub6EFn6HriJB8wve2KZNPPbwEbwHJ5fmRGBrum7xyjaxzHPvtnns3ab3fpecQF8ZWoy3PnazqxAq6JazJDnNYrkp61ZX94fayC3hU6bdhjS52"

	start := make(chan struct{})
	results := make(chan TRONAddressAllocation, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			allocation, err := repo.AllocateTRONAddress(ctx, onchain.NetworkTronMainnet, xpub)
			require.NoError(t, err)
			results <- allocation
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	indexes := make(map[uint32]struct{}, 16)
	addresses := make(map[string]struct{}, 16)
	for allocation := range results {
		require.Equal(t, onchain.NetworkTronMainnet, allocation.Network)
		require.NoError(t, onchain.ValidateAddress(onchain.NetworkTronMainnet, allocation.Address))
		indexes[allocation.Index] = struct{}{}
		addresses[allocation.Address] = struct{}{}
	}
	require.Len(t, indexes, 16)
	require.Len(t, addresses, 16)
	cursor, err := client.WalletDerivationCursor.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(16), cursor.NextIndex)
}

func TestOnchainRepositoryBootstrapsTRONCursorPastExistingIntents(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	existing := createOnchainTestIntent(t, ctx, repo, client, "cursor-bootstrap")
	const xpub = "xpub6EFn6HriJB8wve2KZNPPbwEbwHJ5fmRGBrum7xyjaxzHPvtnns3ab3fpecQF8ZWoy3PnazqxAq6JazJDnNYrkp61ZX94fayC3hU6bdhjS52"

	allocation, err := repo.AllocateTRONAddress(ctx, onchain.NetworkTronMainnet, xpub)
	require.NoError(t, err)
	require.Equal(t, uint32(existing.DerivationIndex+1), allocation.Index)
	require.NoError(t, onchain.ValidateAddress(onchain.NetworkTronMainnet, allocation.Address))
}

func TestOnchainRepositoryAllocatesIndependentEthereumAddressesConcurrently(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	ethereumXPub := ethereumRepositoryTestXPub(t)

	tronAllocation, err := repo.AllocateTRONAddress(ctx, onchain.NetworkTronMainnet, "xpub6EFn6HriJB8wve2KZNPPbwEbwHJ5fmRGBrum7xyjaxzHPvtnns3ab3fpecQF8ZWoy3PnazqxAq6JazJDnNYrkp61ZX94fayC3hU6bdhjS52")
	require.NoError(t, err)
	require.Zero(t, tronAllocation.Index)

	start := make(chan struct{})
	results := make(chan EthereumAddressAllocation, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			allocation, allocateErr := repo.AllocateEthereumAddress(ctx, onchain.NetworkEthereumMainnet, ethereumXPub)
			require.NoError(t, allocateErr)
			results <- allocation
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	indexes := make(map[uint32]struct{}, 8)
	addresses := make(map[string]struct{}, 8)
	for allocation := range results {
		require.Equal(t, onchain.NetworkEthereumMainnet, allocation.Network)
		require.NoError(t, onchain.ValidateAddress(onchain.NetworkEthereumMainnet, allocation.Address))
		indexes[allocation.Index] = struct{}{}
		addresses[allocation.Address] = struct{}{}
	}
	require.Len(t, indexes, 8)
	require.Len(t, addresses, 8)
	_, hasZero := indexes[0]
	require.True(t, hasZero, "Ethereum must start from its own index zero despite a TRON allocation")
	cursors, err := client.WalletDerivationCursor.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, cursors, 2)
	nextByNetwork := make(map[string]int64, 2)
	for _, cursor := range cursors {
		nextByNetwork[cursor.Network] = cursor.NextIndex
	}
	require.Equal(t, int64(1), nextByNetwork[string(onchain.NetworkTronMainnet)])
	require.Equal(t, int64(8), nextByNetwork[string(onchain.NetworkEthereumMainnet)])
}

func TestOnchainRepositoryRejectsCrossChainAllocatorUse(t *testing.T) {
	repo, _ := newOnchainRepoSQLite(t)
	_, err := repo.AllocateEthereumAddress(context.Background(), onchain.NetworkTronMainnet, ethereumRepositoryTestXPub(t))
	require.ErrorContains(t, err, "does not support")
}

func ethereumRepositoryTestXPub(t *testing.T) string {
	t.Helper()
	key, err := hdkeychain.NewMaster([]byte("0123456789abcdef0123456789abcdef"), &chaincfg.MainNetParams)
	require.NoError(t, err)
	defer key.Zero()
	for _, index := range []uint32{
		hdkeychain.HardenedKeyStart + 44,
		hdkeychain.HardenedKeyStart + 60,
		hdkeychain.HardenedKeyStart,
		0,
	} {
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

func TestOnchainRepositoryConcurrentConditionalUpdateHasSingleWinner(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "concurrent")

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- repo.TransitionIntent(ctx, intent.ID, 0, onchain.IntentPending, onchain.IntentPartiallyPaid)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var successes, stale int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrOnchainConcurrentUpdate):
			stale++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, stale)

	loaded, err := client.OnchainPaymentIntent.Query().Where(onchainpaymentintent.IDEQ(intent.ID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentPartiallyPaid), loaded.Status)
	require.Equal(t, 1, loaded.Version)
	require.ErrorIs(t,
		repo.TransitionIntent(ctx, intent.ID, loaded.Version, onchain.IntentPartiallyPaid, onchain.IntentSettled),
		ErrOnchainInvalidTransition,
	)
}

func TestOnchainRepositoryEntityTransitions(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "entities")

	deposit, err := repo.CreateDeposit(ctx, DepositCreate{
		IntentID:         intent.ID,
		PaymentOrderID:   intent.PaymentOrderID,
		UserID:           intent.UserID,
		Network:          intent.Network,
		ChainID:          intent.ChainID,
		TransactionID:    "transaction-1",
		LogIndex:         0,
		TransactionIndex: 1,
		BlockHeight:      100,
		BlockHash:        "block-100",
		TokenContract:    intent.TokenContract,
		FromAddress:      "sender",
		ToAddress:        intent.DepositAddress,
		AmountRaw:        "10000000",
		TransactionTime:  time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, repo.TransitionDeposit(ctx, deposit.ID, 0, onchain.DepositConfirmed, onchain.DepositCreditPending))

	sweep, err := repo.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID:           intent.ID,
		Network:            intent.Network,
		ChainID:            intent.ChainID,
		SourceAddress:      intent.DepositAddress,
		DestinationAddress: "treasury",
		BalanceSnapshotRaw: "10000000",
		AmountRaw:          "10000000",
		IdempotencyKey:     "sweep-1",
	})
	require.NoError(t, err)
	require.NoError(t, repo.TransitionWalletSweep(ctx, sweep.ID, 0, onchain.TransferPrepared, onchain.TransferSigning))

	gas, err := repo.CreateEthereumGasFunding(ctx, EthereumGasFundingCreate{
		IntentID:                intent.ID,
		ChainID:                 1,
		SponsorAddress:          "0x1111111111111111111111111111111111111111",
		TargetAddress:           "0x2222222222222222222222222222222222222222",
		DerivationIndex:         intent.DerivationIndex,
		AmountWei:               "1000000000000000",
		IdempotencyKey:          "gas-1",
		Nonce:                   1,
		GasLimit:                21000,
		MaxFeePerGasWei:         "30000000000",
		MaxPriorityFeePerGasWei: "1000000000",
	})
	require.NoError(t, err)
	require.NoError(t, repo.TransitionGasFunding(ctx, gas.ID, 0, onchain.TransferPrepared, onchain.TransferFeeWait))

	nonce, err := repo.CreateEthereumNonceState(ctx, EthereumNonceStateCreate{
		ChainID:       1,
		SenderAddress: "0x1111111111111111111111111111111111111111",
		NextNonce:     1,
	})
	require.NoError(t, err)
	require.NoError(t, repo.TransitionNonceState(ctx, nonce.ID, 0, onchain.NonceReconciling, onchain.NonceReady))
}

func TestOnchainRepositoryPlansSettledTRONSweepsIdempotently(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "sweep-planning")
	deposit, err := repo.CreateDeposit(ctx, DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: intent.Network, ChainID: intent.ChainID, TransactionID: "sweep-funding-transaction",
		LogIndex: 0, TransactionIndex: 0, BlockHeight: 101, BlockHash: "block-101",
		TokenContract: intent.TokenContract, FromAddress: "sender", ToAddress: intent.DepositAddress,
		AmountRaw: "50000000", TransactionTime: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, repo.TransitionDeposit(ctx, deposit.ID, 0, onchain.DepositConfirmed, onchain.DepositCreditPending))
	require.NoError(t, repo.TransitionDeposit(ctx, deposit.ID, 1, onchain.DepositCreditPending, onchain.DepositCredited))
	_, err = client.OnchainPaymentIntent.UpdateOneID(intent.ID).
		SetStatus(string(onchain.IntentSettled)).
		SetSettledAt(time.Now().UTC()).
		Save(ctx)
	require.NoError(t, err)

	candidates, err := repo.ListTRONSweepCandidates(ctx, onchain.NetworkTronMainnet, 10)
	require.NoError(t, err)
	require.Equal(t, []onchain.TRONSweepCandidate{{
		IntentID: intent.ID, Network: onchain.NetworkTronMainnet, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DerivationIndex: intent.DerivationIndex, FundingMarker: deposit.ID,
	}}, candidates)

	input := onchain.TRONSweepCreate{
		IntentID: intent.ID, Network: onchain.NetworkTronMainnet, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DestinationAddress: "treasury",
		BalanceSnapshotRaw: "50000000", AmountRaw: "50000000",
		IdempotencyKey: "tron-sweep-v1:test", Status: onchain.TransferResourceWait,
	}
	first, created, err := repo.EnsureTRONSweep(ctx, input)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, onchain.TransferResourceWait, first.Status)
	second, created, err := repo.EnsureTRONSweep(ctx, input)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first, second)

	candidates, err = repo.ListTRONSweepCandidates(ctx, onchain.NetworkTronMainnet, 10)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.NotNil(t, candidates[0].WaitingTask)
	require.Equal(t, first.ID, candidates[0].WaitingTask.ID)
	require.NoError(t, repo.MarkTRONSweepResourcesReady(ctx, first.ID, first.Version))
	candidates, err = repo.ListTRONSweepCandidates(ctx, onchain.NetworkTronMainnet, 10)
	require.NoError(t, err)
	require.Empty(t, candidates, "an active prepared task must block conflicting sweep creation")

	mismatched := input
	mismatched.AmountRaw = "49999999"
	_, _, err = repo.EnsureTRONSweep(ctx, mismatched)
	require.ErrorIs(t, err, ErrOnchainConflict)
}

func TestOnchainRepositoryTracksEthereumGasFundingThroughFinalized(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "ethereum-gas-funding")
	key, err := onchain.EthereumGasFundingIdempotencyKey(intent.ID, intent.DerivationIndex, 17, "1000000000000000")
	require.NoError(t, err)
	funding, err := repo.CreateEthereumGasFunding(ctx, EthereumGasFundingCreate{
		IntentID: intent.ID, ChainID: 1,
		SponsorAddress:  "0x1111111111111111111111111111111111111111",
		TargetAddress:   "0x2222222222222222222222222222222222222222",
		DerivationIndex: intent.DerivationIndex, AmountWei: "1000000000000000", IdempotencyKey: key,
		Nonce: 4, GasLimit: 21000, MaxFeePerGasWei: "30000000000", MaxPriorityFeePerGasWei: "1000000000",
	})
	require.NoError(t, err)

	tasks, err := repo.ListEthereumGasFundingTasks(ctx, 1, time.Now().UTC(), 10, true)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, onchain.TransferPrepared, tasks[0].Status)
	require.Equal(t, uint64(4), tasks[0].Nonce)
	require.Equal(t, uint64(21000), tasks[0].GasLimit)

	require.NoError(t, repo.MarkEthereumGasFundingSigning(ctx, funding.ID, 0, "sha256:request"))
	txHash := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, repo.RecordEthereumGasFundingBroadcast(ctx, funding.ID, 1, "audit-gas-1", txHash))
	require.NoError(t, repo.MarkEthereumGasFundingConfirming(ctx, funding.ID, 2))
	finalizedAt := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.FinalizeEthereumGasFunding(ctx, onchain.EthereumGasFundingFinalization{
		TaskID: funding.ID, Version: 3, WinnerID: funding.ID, WinnerHash: txHash, BlockHeight: 123,
		BlockHash:    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ActualFeeWei: "630000000000000", FinalizedAt: finalizedAt,
	}))

	persisted, err := client.EthereumGasFunding.Get(ctx, funding.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.TransferFinalized), persisted.Status)
	require.True(t, persisted.Finalized)
	require.Equal(t, int64(123), *persisted.FinalizedBlockHeight)
	require.Equal(t, "630000000000000", *persisted.ActualFeeWei)
	require.Equal(t, "sha256:request", *persisted.SignerRequestDigest)
	require.Equal(t, "audit-gas-1", *persisted.SignerAuditID)
	require.Equal(t, txHash, *persisted.TransactionHash)
	require.Equal(t, finalizedAt, *persisted.FinalizedAt)
}

func TestOnchainRepositoryPersistsEthereumGasFundingReplacementChain(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "ethereum-gas-replacement")
	key, err := onchain.EthereumGasFundingIdempotencyKey(intent.ID, intent.DerivationIndex, 17, "1000000000000000")
	require.NoError(t, err)
	funding, err := repo.CreateEthereumGasFunding(ctx, EthereumGasFundingCreate{IntentID: intent.ID, ChainID: 1, SponsorAddress: "0x1111111111111111111111111111111111111111", TargetAddress: "0x2222222222222222222222222222222222222222", DerivationIndex: intent.DerivationIndex, AmountWei: "1000000000000000", IdempotencyKey: key, Nonce: 4, GasLimit: 21000, MaxFeePerGasWei: "30000000000", MaxPriorityFeePerGasWei: "1000000000"})
	require.NoError(t, err)
	require.NoError(t, repo.MarkEthereumGasFundingSigning(ctx, funding.ID, 0, "sha256:request"))
	original := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	replacement := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	require.NoError(t, repo.RecordEthereumGasFundingBroadcast(ctx, funding.ID, 1, "audit-original", original))
	require.NoError(t, repo.RecordEthereumGasFundingReplacement(ctx, onchain.EthereumGasFundingReplacement{TaskID: funding.ID, Version: 2, SignerAuditID: "audit-replacement", OriginalTransactionHash: original, ReplacementTransactionHash: replacement, Nonce: 4, GasLimit: 21000, MaxFeePerGasWei: "34500000000", MaxPriorityFeePerGasWei: "1150000000", NextAttemptAt: time.Now().UTC().Add(time.Minute)}))
	current, err := client.EthereumGasFunding.Get(ctx, funding.ID)
	require.NoError(t, err)
	require.Equal(t, replacement, *current.TransactionHash)
	require.NotNil(t, current.ReplacementOfID)
	require.Equal(t, int64(4), current.Nonce)
	archived, err := client.EthereumGasFunding.Get(ctx, *current.ReplacementOfID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.TransferReplaced), archived.Status)
	require.Equal(t, original, *archived.TransactionHash)
	require.Equal(t, current.IntentID, archived.IntentID)
	require.Equal(t, current.AmountWei, archived.AmountWei)
	require.Equal(t, "30000000000", archived.MaxFeePerGasWei)
	finalizedAt := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.FinalizeEthereumGasFunding(ctx, onchain.EthereumGasFundingFinalization{TaskID: current.ID, Version: current.Version, WinnerID: archived.ID, WinnerHash: original, BlockHeight: 200, BlockHash: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ActualFeeWei: "630000000000000", FinalizedAt: finalizedAt}))
	current, err = client.EthereumGasFunding.Get(ctx, current.ID)
	require.NoError(t, err)
	archived, err = client.EthereumGasFunding.Get(ctx, archived.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.TransferReplaced), current.Status)
	require.Equal(t, string(onchain.TransferFinalized), archived.Status)
	require.True(t, archived.Finalized)
}

func TestOnchainRepositoryPersistsEthereumSweepReplacementChain(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "ethereum-sweep-replacement")
	sweep, err := repo.CreateWalletSweep(ctx, WalletSweepCreate{IntentID: intent.ID, Network: string(onchain.NetworkEthereumMainnet), ChainID: 1, SourceAddress: "0x2222222222222222222222222222222222222222", DestinationAddress: "0x3333333333333333333333333333333333333333", BalanceSnapshotRaw: "250000000", AmountRaw: "250000000", IdempotencyKey: "ethereum-sweep-v1:replacement"})
	require.NoError(t, err)
	require.NoError(t, repo.MarkEthereumSweepSigning(ctx, sweep.ID, 0, "sha256:request"))
	original := "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	replacement := "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	require.NoError(t, repo.RecordEthereumSweepBroadcast(ctx, sweep.ID, 1, "audit-original", original, 9))
	current, err := client.WalletSweep.Get(ctx, sweep.ID)
	require.NoError(t, err)
	require.NoError(t, repo.RecordEthereumSweepReplacement(ctx, onchain.EthereumSweepReplacement{TaskID: sweep.ID, Version: current.Version, SignerAuditID: "audit-replacement", OriginalTransactionHash: original, ReplacementTransactionHash: replacement, Nonce: 9, NextAttemptAt: time.Now().UTC().Add(time.Minute)}))
	current, err = client.WalletSweep.Get(ctx, sweep.ID)
	require.NoError(t, err)
	require.Equal(t, replacement, *current.TransactionID)
	require.NotNil(t, current.ReplacementOfID)
	archived, err := client.WalletSweep.Get(ctx, *current.ReplacementOfID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.TransferReplaced), archived.Status)
	require.Equal(t, original, *archived.TransactionID)
	require.Equal(t, current.AmountRaw, archived.AmountRaw)
	require.Equal(t, current.SourceAddress, archived.SourceAddress)
	finalizedAt := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.FinalizeEthereumSweep(ctx, onchain.EthereumSweepFinalization{TaskID: current.ID, Version: current.Version, WinnerID: archived.ID, WinnerHash: original, BlockHeight: 201, BlockHash: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ActualFeeWei: "1950000000000000", FinalizedAt: finalizedAt}))
	current, err = client.WalletSweep.Get(ctx, current.ID)
	require.NoError(t, err)
	archived, err = client.WalletSweep.Get(ctx, archived.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.TransferReplaced), current.Status)
	require.Equal(t, string(onchain.TransferFinalized), archived.Status)
	require.Equal(t, "1950000000000000", archived.FeeRaw)
}

func TestOnchainRepositorySerializesAndReconcilesEthereumNonces(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	now := time.Now().UTC().Truncate(time.Second)
	input := onchain.EthereumNonceReserveInput{
		ChainID: 1, SenderAddress: "0x1111111111111111111111111111111111111111",
		Owner: "worker-1", ObservedPending: 7, Now: now, LeaseUntil: now.Add(30 * time.Second),
	}

	first, acquired, err := repo.ReserveEthereumNonce(ctx, input)
	require.NoError(t, err)
	require.True(t, acquired)
	require.Equal(t, uint64(7), first.Nonce)

	contender := input
	contender.Owner = "worker-2"
	_, acquired, err = repo.ReserveEthereumNonce(ctx, contender)
	require.NoError(t, err)
	require.False(t, acquired, "an active lease must serialize allocations for the sender")

	require.NoError(t, repo.CommitEthereumNonce(ctx, first, 8, now.Add(time.Second)))
	contender.ObservedPending = 8
	contender.Now = now.Add(2 * time.Second)
	contender.LeaseUntil = contender.Now.Add(30 * time.Second)
	second, acquired, err := repo.ReserveEthereumNonce(ctx, contender)
	require.NoError(t, err)
	require.True(t, acquired)
	require.Equal(t, uint64(8), second.Nonce)

	afterCrash := contender
	afterCrash.Owner = "worker-3"
	afterCrash.Now = contender.LeaseUntil.Add(time.Second)
	afterCrash.LeaseUntil = afterCrash.Now.Add(30 * time.Second)
	afterCrash.ObservedPending = 8
	_, _, err = repo.ReserveEthereumNonce(ctx, afterCrash)
	require.ErrorIs(t, err, onchain.ErrEthereumNonceConflict)

	state, err := client.EthereumNonceState.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, string(onchain.NonceConflict), state.Status)
	require.Equal(t, int64(9), state.NextNonce)
	require.Equal(t, int64(8), state.ObservedPendingNonce)
	require.Equal(t, "PENDING_NONCE_CONFLICT", *state.LastErrorCode)
	require.Nil(t, state.LeaseOwner)
}

func TestOnchainRepositoryBlocksEthereumSweepUntilGasFundingFinalized(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "ethereum-sweep-gas-gate")
	source := "0x2222222222222222222222222222222222222222"
	err := client.OnchainPaymentIntent.DeleteOneID(intent.ID).Exec(ctx)
	require.NoError(t, err)
	intent, err = repo.CreatePaymentIntent(ctx, PaymentIntentCreate{
		PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: string(onchain.NetworkEthereumMainnet), ChainID: 1,
		TokenContract: onchain.EthereumMainnetUSDTContract, DepositAddress: source,
		DerivationIndex: intent.DerivationIndex, ExpectedAmountRaw: "250000000",
		ConfigSnapshot: map[string]any{"network": string(onchain.NetworkEthereumMainnet)}, ConfigVersion: "test-v1",
	})
	require.NoError(t, err)
	sweepKey, err := onchain.EthereumSweepIdempotencyKey(intent.ID, intent.DerivationIndex, 17, "250000000")
	require.NoError(t, err)
	_, err = repo.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID: intent.ID, Network: string(onchain.NetworkEthereumMainnet), ChainID: 1,
		SourceAddress: source, DestinationAddress: "0x3333333333333333333333333333333333333333",
		BalanceSnapshotRaw: "250000000", AmountRaw: "250000000", IdempotencyKey: sweepKey,
	})
	require.NoError(t, err)
	fundingKey, err := onchain.EthereumGasFundingIdempotencyKey(intent.ID, intent.DerivationIndex, 17, "5000000000000000")
	require.NoError(t, err)
	funding, err := repo.CreateEthereumGasFunding(ctx, EthereumGasFundingCreate{
		IntentID: intent.ID, ChainID: 1, SponsorAddress: "0x1111111111111111111111111111111111111111",
		TargetAddress: source, DerivationIndex: intent.DerivationIndex, AmountWei: "5000000000000000",
		IdempotencyKey: fundingKey, Nonce: 1, GasLimit: 21000,
		MaxFeePerGasWei: "30000000000", MaxPriorityFeePerGasWei: "1000000000",
	})
	require.NoError(t, err)

	tasks, err := repo.ListEthereumSweepExecutionTasks(ctx, onchain.NetworkEthereumMainnet, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Empty(t, tasks)
	_, err = client.EthereumGasFunding.UpdateOneID(funding.ID).
		SetStatus(string(onchain.TransferFinalized)).
		SetFinalized(true).
		Save(ctx)
	require.NoError(t, err)
	tasks, err = repo.ListEthereumSweepExecutionTasks(ctx, onchain.NetworkEthereumMainnet, time.Now().UTC(), 10)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, source, tasks[0].SourceAddress)
}

func createPreparedTRONSweepExecutionTask(t *testing.T, ctx context.Context, repo *OnchainRepository, client *dbent.Client, suffix string) onchain.TRONSweepTask {
	t.Helper()
	intent := createOnchainTestIntent(t, ctx, repo, client, suffix)
	task, created, err := repo.EnsureTRONSweep(ctx, onchain.TRONSweepCreate{
		IntentID: intent.ID, Network: onchain.NetworkTronMainnet, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DestinationAddress: "treasury-" + suffix,
		BalanceSnapshotRaw: "50000000", AmountRaw: "50000000",
		IdempotencyKey: "tron-sweep-v1:" + suffix, Status: onchain.TransferPrepared,
	})
	require.NoError(t, err)
	require.True(t, created)
	return task
}

func TestOnchainRepositoryClaimsTRONSweepSigningConditionally(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	task := createPreparedTRONSweepExecutionTask(t, ctx, repo, client, "execution-claim")
	now := time.Now().UTC()

	tasks, err := repo.ListTRONSweepExecutionTasks(ctx, onchain.NetworkTronMainnet, now, 10, true)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, task.ID, tasks[0].ID)
	require.Positive(t, tasks[0].DerivationIndex)
	require.Equal(t, onchain.TransferPrepared, tasks[0].Status)

	require.NoError(t, repo.MarkTRONSweepSigning(ctx, task.ID, task.Version, "sha256:request"))
	require.ErrorIs(t, repo.MarkTRONSweepSigning(ctx, task.ID, task.Version, "sha256:request"), ErrOnchainConcurrentUpdate)
	stored, err := client.WalletSweep.Get(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.TransferSigning), stored.Status)
	require.Equal(t, "sha256:request", *stored.SignerRequestDigest)
	require.Equal(t, 1, stored.Version)
}

func TestOnchainRepositoryTrackingModeCannotBeStarvedByPreparedSweeps(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	prepared := createPreparedTRONSweepExecutionTask(t, ctx, repo, client, "tracking-prepared")
	broadcast := createPreparedTRONSweepExecutionTask(t, ctx, repo, client, "tracking-broadcast")
	require.Less(t, prepared.ID, broadcast.ID)
	require.NoError(t, repo.MarkTRONSweepSigning(ctx, broadcast.ID, broadcast.Version, "sha256:tracking"))
	require.NoError(t, repo.RecordTRONSweepBroadcast(ctx, broadcast.ID, broadcast.Version+1, "audit-tracking", fmt.Sprintf("%064x", 777)))

	tasks, err := repo.ListTRONSweepExecutionTasks(ctx, onchain.NetworkTronMainnet, time.Now().UTC(), 1, false)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, broadcast.ID, tasks[0].ID)
	require.Equal(t, onchain.TransferBroadcast, tasks[0].Status)
}

func TestOnchainRepositoryRecoversTRONSweepBroadcastResponseLoss(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	task := createPreparedTRONSweepExecutionTask(t, ctx, repo, client, "execution-response-loss")
	now := time.Now().UTC()
	require.NoError(t, repo.MarkTRONSweepSigning(ctx, task.ID, task.Version, "sha256:request"))
	require.NoError(t, repo.RecordTRONSweepRetry(
		ctx, task.ID, 1, onchain.TransferSigning, "SIGNER_CALL_FAILED", "response lost", now.Add(time.Minute), false,
	))

	tasks, err := repo.ListTRONSweepExecutionTasks(ctx, onchain.NetworkTronMainnet, now, 10, true)
	require.NoError(t, err)
	require.Empty(t, tasks, "retry must not run before its persistent backoff expires")
	tasks, err = repo.ListTRONSweepExecutionTasks(ctx, onchain.NetworkTronMainnet, now.Add(2*time.Minute), 10, true)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, onchain.TransferSigning, tasks[0].Status)
	require.Equal(t, 1, tasks[0].RetryCount)
	require.Equal(t, 2, tasks[0].Version)

	txID := fmt.Sprintf("%064x", 501)
	require.NoError(t, repo.RecordTRONSweepBroadcast(ctx, task.ID, tasks[0].Version, "audit-recovered", txID))
	require.ErrorIs(t, repo.RecordTRONSweepBroadcast(ctx, task.ID, tasks[0].Version, "audit-recovered", txID), ErrOnchainConcurrentUpdate)
	stored, err := client.WalletSweep.Get(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.TransferBroadcast), stored.Status)
	require.Equal(t, txID, *stored.TransactionID)
	require.Equal(t, "audit-recovered", *stored.SignerAuditID)
	require.Nil(t, stored.NextAttemptAt)
}

func TestOnchainRepositoryPausesTRONSweepForManualReview(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	task := createPreparedTRONSweepExecutionTask(t, ctx, repo, client, "execution-review")
	now := time.Now().UTC()
	require.NoError(t, repo.MarkTRONSweepSigning(ctx, task.ID, task.Version, "sha256:request"))
	require.NoError(t, repo.RecordTRONSweepRetry(ctx, task.ID, 1, onchain.TransferSigning, "SIGNER_FAILED", "attempt 1", now.Add(time.Minute), false))
	require.NoError(t, repo.RecordTRONSweepRetry(ctx, task.ID, 2, onchain.TransferSigning, "SIGNER_FAILED", "attempt 2", now.Add(time.Minute), false))
	require.NoError(t, repo.RecordTRONSweepRetry(ctx, task.ID, 3, onchain.TransferSigning, "SIGNER_FAILED", "attempt 3", now.Add(time.Minute), true))

	stored, err := client.WalletSweep.Query().Where(walletsweep.IDEQ(task.ID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, string(onchain.TransferReviewRequired), stored.Status)
	require.Equal(t, 3, stored.RetryCount)
	require.Equal(t, "SIGNER_FAILED", *stored.FailureCode)
	require.Nil(t, stored.NextAttemptAt)
	tasks, err := repo.ListTRONSweepExecutionTasks(ctx, onchain.NetworkTronMainnet, now.Add(24*time.Hour), 10, true)
	require.NoError(t, err)
	require.Empty(t, tasks)
}

func TestOnchainRepositoryFinalizesTRONSweepResourceAccounting(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	task := createPreparedTRONSweepExecutionTask(t, ctx, repo, client, "execution-finalize")
	txID := fmt.Sprintf("%064x", 502)
	require.NoError(t, repo.MarkTRONSweepSigning(ctx, task.ID, task.Version, "sha256:request"))
	require.NoError(t, repo.RecordTRONSweepBroadcast(ctx, task.ID, 1, "audit-final", txID))
	require.NoError(t, repo.MarkTRONSweepConfirming(ctx, task.ID, 2))
	finalizedAt := time.Now().UTC()
	require.NoError(t, repo.FinalizeTRONSweep(ctx, onchain.TRONSweepFinalization{
		TaskID: task.ID, Version: 3, BlockHeight: 12345, BlockHash: fmt.Sprintf("%064x", 12345),
		FeeRaw: "1000", EnergyUsed: 64000, BandwidthUsed: 345, FinalizedAt: finalizedAt,
	}))
	require.ErrorIs(t, repo.FinalizeTRONSweep(ctx, onchain.TRONSweepFinalization{
		TaskID: task.ID, Version: 3, BlockHeight: 12345, BlockHash: fmt.Sprintf("%064x", 12345),
		FeeRaw: "1000", EnergyUsed: 64000, BandwidthUsed: 345, FinalizedAt: finalizedAt,
	}), ErrOnchainConcurrentUpdate)

	stored, err := client.WalletSweep.Get(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.TransferFinalized), stored.Status)
	require.Equal(t, int64(12345), *stored.FinalizedBlockHeight)
	require.Equal(t, fmt.Sprintf("%064x", 12345), *stored.FinalizedBlockHash)
	require.Equal(t, "1000", stored.FeeRaw)
	require.Equal(t, int64(64000), stored.EnergyUsed)
	require.Equal(t, int64(345), stored.BandwidthUsed)
	require.WithinDuration(t, finalizedAt, *stored.FinalizedAt, time.Millisecond)
}

func TestOnchainRepositoryPreparesUnderpaymentAndLaterSettlement(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "settlement")

	createSettlementDeposit(t, ctx, repo, intent, "settlement-a", 0, "3000000")
	createSettlementDeposit(t, ctx, repo, intent, "settlement-b", 1, "4000000")
	candidates, err := repo.ListSettlementCandidates(ctx, onchain.NetworkTronMainnet, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{intent.ID}, candidates)

	prepared, err := repo.PrepareIntentSettlement(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, onchain.IntentPartiallyPaid, prepared.Status)
	require.Equal(t, "7000000", prepared.ReceivedAmountRaw)
	require.Equal(t, "3000000", prepared.PendingAmountRaw)
	require.False(t, prepared.Ready)

	loadedIntent, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, "7000000", loadedIntent.ReceivedAmountRaw)
	require.Equal(t, string(onchain.IntentPartiallyPaid), loadedIntent.Status)
	order, err := client.PaymentOrder.Get(ctx, intent.PaymentOrderID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusPartiallyPaid, order.Status)
	pendingDeposits, err := client.OnchainDeposit.Query().Where(
		onchaindeposit.IntentIDEQ(intent.ID),
		onchaindeposit.StatusEQ(string(onchain.DepositCreditPending)),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, pendingDeposits)

	createSettlementDeposit(t, ctx, repo, intent, "settlement-c", 2, "3000000")
	prepared, err = repo.PrepareIntentSettlement(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, onchain.IntentSettlementDue, prepared.Status)
	require.Equal(t, "10000000", prepared.ReceivedAmountRaw)
	require.Equal(t, "0", prepared.PendingAmountRaw)
	require.True(t, prepared.Ready)

	loadedIntent, err = client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, "10000000", loadedIntent.ReceivedAmountRaw)
	require.Equal(t, string(onchain.IntentSettlementDue), loadedIntent.Status)
	candidates, err = repo.ListSettlementCandidates(ctx, onchain.NetworkTronMainnet, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{intent.ID}, candidates, "settlement-due intents remain eligible for fulfillment")

	retry, err := repo.PrepareIntentSettlement(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, intent.ID, retry.IntentID)
	require.True(t, retry.Ready)
	order, err = client.PaymentOrder.Query().Where(paymentorder.IDEQ(intent.PaymentOrderID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusPartiallyPaid, order.Status, "fulfillment owns the paid/completed transition")
}

func TestOnchainRepositoryRoutesExpiredUnderpaymentToReview(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "expired-underpayment")
	createSettlementDeposit(t, ctx, repo, intent, "expired-underpayment-a", 0, "4000000")
	prepared, err := repo.PrepareIntentSettlement(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, onchain.IntentPartiallyPaid, prepared.Status)

	_, err = client.PaymentOrder.UpdateOneID(intent.PaymentOrderID).SetStatus(payment.OrderStatusExpired).Save(ctx)
	require.NoError(t, err)
	candidates, err := repo.ListSettlementCandidates(ctx, onchain.NetworkTronMainnet, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{intent.ID}, candidates, "expiry must wake an intent even without a new deposit")

	prepared, err = repo.PrepareIntentSettlement(ctx, intent.ID)
	require.NoError(t, err)
	require.True(t, prepared.ReviewRequired)
	require.Equal(t, onchain.IntentReviewRequired, prepared.Status)
	require.Equal(t, "6000000", prepared.PendingAmountRaw)
	loadedIntent, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentReviewRequired), loadedIntent.Status)
	order, err := client.PaymentOrder.Get(ctx, intent.PaymentOrderID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusReviewRequired, order.Status)
	reviewDeposits, err := client.OnchainDeposit.Query().Where(
		onchaindeposit.IntentIDEQ(intent.ID),
		onchaindeposit.StatusEQ(string(onchain.DepositReviewRequired)),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, reviewDeposits)
}

func TestOnchainRepositoryRoutesCompletedAddressReuseToReview(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "completed-reuse")
	original, err := repo.CreateDeposit(ctx, DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: intent.Network, ChainID: intent.ChainID, TransactionID: "completed-original",
		LogIndex: 0, BlockHeight: 100, BlockHash: "block-100", TokenContract: intent.TokenContract,
		FromAddress: "sender", ToAddress: intent.DepositAddress, AmountRaw: "10000000",
		TransactionTime: time.Now().UTC(),
	})
	require.NoError(t, err)
	_, err = client.OnchainDeposit.UpdateOneID(original.ID).SetStatus(string(onchain.DepositCredited)).Save(ctx)
	require.NoError(t, err)
	_, err = client.OnchainPaymentIntent.UpdateOneID(intent.ID).
		SetStatus(string(onchain.IntentSettled)).
		SetReceivedAmountRaw("10000000").
		SetCreditedAmountRaw("10000000").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentOrder.UpdateOneID(intent.PaymentOrderID).SetStatus(payment.OrderStatusCompleted).Save(ctx)
	require.NoError(t, err)
	createSettlementDeposit(t, ctx, repo, intent, "completed-reuse-late", 1, "2000000")

	candidates, err := repo.ListSettlementCandidates(ctx, onchain.NetworkTronMainnet, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{intent.ID}, candidates)
	prepared, err := repo.PrepareIntentSettlement(ctx, intent.ID)
	require.NoError(t, err)
	require.True(t, prepared.ReviewRequired)
	require.Equal(t, "12000000", prepared.ReceivedAmountRaw)
	loadedOriginal, err := client.OnchainDeposit.Get(ctx, original.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.DepositCredited), loadedOriginal.Status)
	reviewCount, err := client.OnchainDeposit.Query().Where(
		onchaindeposit.IntentIDEQ(intent.ID),
		onchaindeposit.StatusEQ(string(onchain.DepositReviewRequired)),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, reviewCount)
	order, err := client.PaymentOrder.Get(ctx, intent.PaymentOrderID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusCompleted, order.Status, "address reuse must not reopen the completed order")
}

func TestOnchainRepositoryPersistsSettlementRetryAndPermanentFailure(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "retry")
	createSettlementDeposit(t, ctx, repo, intent, "retry-deposit", 0, "10000000")
	prepared, err := repo.PrepareIntentSettlement(ctx, intent.ID)
	require.NoError(t, err)
	require.True(t, prepared.Ready)

	nextRetry := time.Now().UTC().Add(time.Hour)
	require.NoError(t, repo.RecordSettlementFailure(ctx, onchain.SettlementFailure{
		IntentID: intent.ID, Code: "BALANCE_UNAVAILABLE", Message: "temporary failure",
		Retryable: true, NextRetryAt: nextRetry,
	}))
	loaded, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentSettlementDue), loaded.Status)
	require.NotNil(t, loaded.NextSettlementAt)
	require.WithinDuration(t, nextRetry, *loaded.NextSettlementAt, time.Millisecond)
	require.NotNil(t, loaded.LastErrorCode)
	candidates, err := repo.ListSettlementCandidates(ctx, onchain.NetworkTronMainnet, 10)
	require.NoError(t, err)
	require.Empty(t, candidates, "future retry must not be selected early")

	require.NoError(t, repo.RecordSettlementFailure(ctx, onchain.SettlementFailure{
		IntentID: intent.ID, Code: "INVALID_BINDING", Message: "permanent failure", Retryable: false,
	}))
	loaded, err = client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentReviewRequired), loaded.Status)
	order, err := client.PaymentOrder.Get(ctx, intent.PaymentOrderID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusReviewRequired, order.Status)
	reviewCount, err := client.OnchainDeposit.Query().Where(
		onchaindeposit.IntentIDEQ(intent.ID),
		onchaindeposit.StatusEQ(string(onchain.DepositReviewRequired)),
	).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, reviewCount)
}

func createSettlementDeposit(t *testing.T, ctx context.Context, repo *OnchainRepository, intent *dbent.OnchainPaymentIntent, transactionID string, logIndex int64, amountRaw string) {
	t.Helper()
	_, err := repo.CreateDeposit(ctx, DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: intent.Network, ChainID: intent.ChainID, TransactionID: transactionID,
		LogIndex: logIndex, TransactionIndex: logIndex, BlockHeight: 100 + logIndex,
		BlockHash: fmt.Sprintf("block-%d", 100+logIndex), TokenContract: intent.TokenContract,
		FromAddress: "sender", ToAddress: intent.DepositAddress, AmountRaw: amountRaw,
		TransactionTime: time.Now().UTC(),
	})
	require.NoError(t, err)
}

func TestOnchainRepositoryTransactionRollback(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	wantErr := errors.New("abort")

	err := repo.WithTx(ctx, func(txCtx context.Context, txRepo *OnchainRepository) error {
		_, err := txRepo.CreateChainScanCursor(txCtx, ChainScanCursorCreate{Network: "tron-mainnet", ChainID: 0})
		require.NoError(t, err)
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)
	count, err := client.ChainScanCursor.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestOnchainRepositoryInitializesTRONCursorOnce(t *testing.T) {
	ctx := context.Background()
	repo, _ := newOnchainRepoSQLite(t)
	initializedAt := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, repo.InitializeTRONScanCursor(ctx, onchain.TRONScanCursorInitialization{
		Network: onchain.NetworkTronMainnet, ChainID: 0, FinalizedHeight: 42,
		FinalizedHash: fmt.Sprintf("%064x", 42), InitializedAt: initializedAt,
	}))
	require.NoError(t, repo.InitializeTRONScanCursor(ctx, onchain.TRONScanCursorInitialization{
		Network: onchain.NetworkTronMainnet, ChainID: 0, FinalizedHeight: 99,
		FinalizedHash: fmt.Sprintf("%064x", 99), InitializedAt: initializedAt.Add(time.Minute),
	}))

	cursor, err := repo.LoadTRONScanCursor(ctx, onchain.NetworkTronMainnet)
	require.NoError(t, err)
	require.Equal(t, int64(42), cursor.FinalizedHeight)
	require.Equal(t, fmt.Sprintf("%064x", 42), cursor.FinalizedHash)
	require.Equal(t, onchain.CursorHealthy, cursor.Health)
}

func TestOnchainRepositoryCursorLeaseAcquisitionAndTakeover(t *testing.T) {
	ctx := context.Background()
	repo, _ := newOnchainRepoSQLite(t)
	_, err := repo.CreateChainScanCursor(ctx, ChainScanCursorCreate{Network: "tron-mainnet", ChainID: 0})
	require.NoError(t, err)

	now := time.Now().UTC()
	start := make(chan struct{})
	results := make(chan bool, 2)
	var wg sync.WaitGroup
	for _, owner := range []string{"scanner-a", "scanner-b"} {
		owner := owner
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			acquired, acquireErr := repo.AcquireCursorLease(ctx, "tron-mainnet", owner, now, now.Add(time.Minute))
			require.NoError(t, acquireErr)
			results <- acquired
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	winners := 0
	for acquired := range results {
		if acquired {
			winners++
		}
	}
	require.Equal(t, 1, winners)

	acquired, err := repo.AcquireCursorLease(ctx, "tron-mainnet", "scanner-c", now.Add(2*time.Minute), now.Add(3*time.Minute))
	require.NoError(t, err)
	require.True(t, acquired, "expired lease should be takeable")
	err = repo.CommitTRONScanBlock(ctx, onchain.TRONScanBlockCommit{
		Network: onchain.NetworkTronMainnet, LeaseOwner: "scanner-a", Now: now.Add(2 * time.Minute),
		PreviousHeight: 0,
		Block: onchain.TRONSolidifiedBlock{
			Height: 1, Hash: fmt.Sprintf("%064x", 1), Timestamp: now,
		},
	})
	require.ErrorIs(t, err, onchain.ErrTRONScanLeaseLost, "previous owner cannot advance after lease takeover")
	renewed, err := repo.RenewCursorLease(ctx, "tron-mainnet", "scanner-b", now.Add(2*time.Minute), now.Add(4*time.Minute))
	require.NoError(t, err)
	require.False(t, renewed, "non-owner must not renew the active lease")
	released, err := repo.ReleaseCursorLease(ctx, "tron-mainnet", "scanner-b")
	require.NoError(t, err)
	require.False(t, released, "non-owner must not release the active lease")
	renewed, err = repo.RenewCursorLease(ctx, "tron-mainnet", "scanner-c", now.Add(2*time.Minute), now.Add(4*time.Minute))
	require.NoError(t, err)
	require.True(t, renewed)
	released, err = repo.ReleaseCursorLease(ctx, "tron-mainnet", "scanner-c")
	require.NoError(t, err)
	require.True(t, released)

	_, err = repo.AcquireCursorLease(ctx, "", "scanner", now, now.Add(time.Minute))
	require.Error(t, err)
	_, err = repo.RenewCursorLease(ctx, "tron-mainnet", " ", now, now.Add(time.Minute))
	require.Error(t, err)
	_, err = repo.ReleaseCursorLease(ctx, "tron-mainnet", "")
	require.Error(t, err)
}

func TestOnchainRepositoryEthereumCursorLeaseExpiresAndIsTakenOver(t *testing.T) {
	ctx := context.Background()
	repo, _ := newOnchainRepoSQLite(t)
	_, err := repo.CreateChainScanCursor(ctx, ChainScanCursorCreate{Network: string(onchain.NetworkEthereumMainnet), ChainID: 1})
	require.NoError(t, err)
	now := time.Now().UTC()
	acquired, err := repo.AcquireCursorLease(ctx, string(onchain.NetworkEthereumMainnet), "ethereum-scanner-a", now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = repo.AcquireCursorLease(ctx, string(onchain.NetworkEthereumMainnet), "ethereum-scanner-b", now.Add(2*time.Minute), now.Add(3*time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)
	renewed, err := repo.RenewCursorLease(ctx, string(onchain.NetworkEthereumMainnet), "ethereum-scanner-a", now.Add(2*time.Minute), now.Add(4*time.Minute))
	require.NoError(t, err)
	require.False(t, renewed, "the expired owner cannot renew after takeover")
}

func TestOnchainRepositoryCommitsTRONBlockAtomically(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "scan-commit")
	_, err := repo.CreateChainScanCursor(ctx, ChainScanCursorCreate{Network: intent.Network, ChainID: intent.ChainID})
	require.NoError(t, err)
	now := time.Now().UTC()
	acquired, err := repo.AcquireCursorLease(ctx, intent.Network, "scanner-a", now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)

	blockTime := now.Add(-time.Minute)
	block := onchain.TRONSolidifiedBlock{
		Height: 1, Hash: fmt.Sprintf("%064x", 1), ParentHash: fmt.Sprintf("%064x", 0), Timestamp: blockTime,
	}
	reference := onchain.TRONPaymentIntentReference{
		ID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: onchain.Network(intent.Network), ChainID: intent.ChainID,
		TokenContract: intent.TokenContract, DepositAddress: intent.DepositAddress,
	}
	deposit := onchain.TRONScanDeposit{
		Intent: reference,
		Transfer: onchain.TRC20Transfer{
			TransactionID: fmt.Sprintf("%064x", 10), LogIndex: 0, BlockHeight: 1,
			BlockTimestamp: blockTime, ContractAddress: intent.TokenContract,
			FromAddress: "sender", ToAddress: intent.DepositAddress, AmountRaw: "10000000",
		},
	}
	secondLog := deposit
	secondLog.Transfer.LogIndex = 1
	require.NoError(t, repo.CommitTRONScanBlock(ctx, onchain.TRONScanBlockCommit{
		Network: onchain.Network(intent.Network), LeaseOwner: "scanner-a", Now: now,
		PreviousHeight: 0, Block: block,
		Deposits: []onchain.TRONScanDeposit{deposit, deposit, secondLog},
	}))

	cursor, err := client.ChainScanCursor.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), cursor.FinalizedHeight)
	require.Equal(t, block.Hash, cursor.FinalizedHash)
	require.Equal(t, string(onchain.CursorHealthy), cursor.Health)
	count, err := client.OnchainDeposit.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, count, "exact duplicates are ignored while distinct log indexes are preserved")
	require.NoError(t, repo.ReconcileTRONScanBlock(ctx, onchain.TRONScanBlockReconcile{
		Network: onchain.Network(intent.Network), LeaseOwner: "scanner-a", Now: now,
		Block: block, Deposits: []onchain.TRONScanDeposit{deposit, secondLog},
	}))
	require.ErrorIs(t, repo.ReconcileTRONScanBlock(ctx, onchain.TRONScanBlockReconcile{
		Network: onchain.Network(intent.Network), LeaseOwner: "scanner-a", Now: now,
		Block: block, Deposits: nil,
	}), onchain.ErrTRONScanHashConflict)

	badDeposit := deposit
	badDeposit.Intent.ID = intent.ID + 1000
	badDeposit.Transfer.TransactionID = fmt.Sprintf("%064x", 12)
	secondDeposit := deposit
	secondDeposit.Transfer.TransactionID = fmt.Sprintf("%064x", 11)
	block.Height = 2
	block.ParentHash = block.Hash
	block.Hash = fmt.Sprintf("%064x", 2)
	block.Timestamp = blockTime.Add(3 * time.Second)
	secondDeposit.Transfer.BlockHeight = 2
	secondDeposit.Transfer.BlockTimestamp = block.Timestamp
	badDeposit.Transfer.BlockHeight = 2
	badDeposit.Transfer.BlockTimestamp = block.Timestamp
	err = repo.CommitTRONScanBlock(ctx, onchain.TRONScanBlockCommit{
		Network: onchain.Network(intent.Network), LeaseOwner: "scanner-a", Now: now,
		PreviousHeight: 1, Block: block, Deposits: []onchain.TRONScanDeposit{secondDeposit, badDeposit},
	})
	require.Error(t, err)

	cursor, err = client.ChainScanCursor.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), cursor.FinalizedHeight)
	count, err = client.OnchainDeposit.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, count, "failed block transaction must roll back earlier deposits")

	require.NoError(t, repo.CommitTRONScanBlock(ctx, onchain.TRONScanBlockCommit{
		Network: onchain.Network(intent.Network), LeaseOwner: "scanner-a", Now: now,
		PreviousHeight: 1, Block: block, Deposits: []onchain.TRONScanDeposit{secondDeposit},
	}))
	cursor, err = client.ChainScanCursor.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), cursor.FinalizedHeight)
	count, err = client.OnchainDeposit.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, count, "retry after rollback must persist the block exactly once")
}

func TestOnchainRepositoryCommitsEthereumBlockAtomically(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	user, err := client.User.Create().SetEmail("ethereum-scan@example.com").SetPasswordHash("hash").SetUsername("ethereum-scan").Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
		SetAmount(10).SetPayAmount(10).SetRechargeCode("ethereum-scan").SetPaymentType(payment.TypeUSDTERC20).
		SetPaymentTradeNo("ethereum-scan-trade").SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
	require.NoError(t, err)
	depositAddress := "0x0000000000000000000000000000000000000003"
	intent, err := repo.CreatePaymentIntent(ctx, PaymentIntentCreate{
		PaymentOrderID: order.ID, UserID: user.ID, Network: string(onchain.NetworkEthereumMainnet), ChainID: 1,
		TokenContract: onchain.EthereumMainnetUSDTContract, DepositAddress: depositAddress,
		DerivationIndex: 0, ExpectedAmountRaw: "10000000", ConfigSnapshot: map[string]any{}, ConfigVersion: "test-v1",
	})
	require.NoError(t, err)
	_, err = repo.CreateChainScanCursor(ctx, ChainScanCursorCreate{Network: string(onchain.NetworkEthereumMainnet), ChainID: 1})
	require.NoError(t, err)
	now := time.Now().UTC()
	acquired, err := repo.AcquireCursorLease(ctx, string(onchain.NetworkEthereumMainnet), "ethereum-scanner-a", now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)
	block := onchain.EthereumBlockRef{Number: 1, Hash: "0x" + fmt.Sprintf("%064x", 1), Timestamp: now.Add(-time.Minute)}
	reference := onchain.EthereumPaymentIntentReference{
		ID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: onchain.NetworkEthereumMainnet, ChainID: 1,
		TokenContract: intent.TokenContract, DepositAddress: intent.DepositAddress,
	}
	deposit := onchain.EthereumScanDeposit{
		Intent: reference, TransactionIndex: 2,
		Transfer: onchain.ERC20Transfer{
			TransactionHash: "0x" + fmt.Sprintf("%064x", 10), LogIndex: 0,
			BlockNumber: 1, BlockHash: block.Hash, ContractAddress: intent.TokenContract,
			FromAddress: "0x0000000000000000000000000000000000000002",
			ToAddress:   intent.DepositAddress, AmountRaw: "10000000",
		},
	}
	secondLog := deposit
	secondLog.Transfer.LogIndex = 1
	require.NoError(t, repo.CommitEthereumScanBlock(ctx, onchain.EthereumScanBlockCommit{
		Network: onchain.NetworkEthereumMainnet, LeaseOwner: "ethereum-scanner-a", Now: now,
		PreviousHeight: 0, Block: block, Deposits: []onchain.EthereumScanDeposit{deposit, deposit, secondLog},
	}))
	require.NoError(t, repo.ReconcileEthereumScanBlock(ctx, onchain.EthereumScanBlockReconcile{
		Network: onchain.NetworkEthereumMainnet, LeaseOwner: "ethereum-scanner-a", Now: now,
		Block: block, Deposits: []onchain.EthereumScanDeposit{deposit, secondLog},
	}))
	require.ErrorIs(t, repo.ReconcileEthereumScanBlock(ctx, onchain.EthereumScanBlockReconcile{
		Network: onchain.NetworkEthereumMainnet, LeaseOwner: "ethereum-scanner-a", Now: now,
		Block: block, Deposits: nil,
	}), onchain.ErrEthereumScanHashConflict)

	bad := deposit
	bad.Intent.ID += 1000
	bad.Transfer.TransactionHash = "0x" + fmt.Sprintf("%064x", 12)
	valid := deposit
	valid.Transfer.TransactionHash = "0x" + fmt.Sprintf("%064x", 11)
	block.Number = 2
	block.Hash = "0x" + fmt.Sprintf("%064x", 2)
	block.Timestamp = block.Timestamp.Add(12 * time.Second)
	bad.Transfer.BlockNumber, valid.Transfer.BlockNumber = 2, 2
	bad.Transfer.BlockHash, valid.Transfer.BlockHash = block.Hash, block.Hash
	err = repo.CommitEthereumScanBlock(ctx, onchain.EthereumScanBlockCommit{
		Network: onchain.NetworkEthereumMainnet, LeaseOwner: "ethereum-scanner-a", Now: now,
		PreviousHeight: 1, Block: block, Deposits: []onchain.EthereumScanDeposit{valid, bad},
	})
	require.Error(t, err)

	cursor, err := client.ChainScanCursor.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), cursor.FinalizedHeight)
	count, err := client.OnchainDeposit.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, count, "failed Ethereum block transaction must roll back deposits and cursor")

	require.NoError(t, repo.MarkEthereumScanHashConflict(ctx, onchain.NetworkEthereumMainnet, "ethereum-scanner-a", now, errors.New("saved hash changed")))
	_, err = repo.ListSettlementCandidates(ctx, onchain.NetworkEthereumMainnet, 10)
	require.ErrorIs(t, err, onchain.ErrEthereumScanHashConflict)
}

func TestOnchainRepositoryRejectsMismatchedDuplicateDeposit(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "deposit-mismatch")
	input := DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: intent.Network, ChainID: intent.ChainID,
		TransactionID: fmt.Sprintf("%064x", 50), LogIndex: 0, TransactionIndex: 0,
		BlockHeight: 5, BlockHash: fmt.Sprintf("%064x", 5), TokenContract: intent.TokenContract,
		FromAddress: "sender", ToAddress: intent.DepositAddress, AmountRaw: "10000000",
		TransactionTime: time.Now().UTC(),
	}
	require.NoError(t, repo.EnsureDeposit(ctx, input))

	input.AmountRaw = "20000000"
	require.ErrorIs(t, repo.EnsureDeposit(ctx, input), ErrOnchainConflict)
	stored, err := client.OnchainDeposit.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "10000000", stored.AmountRaw)
}

func TestOnchainRepositoryPersistsTRONHashConflictFailClosed(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	_, err := repo.CreateChainScanCursor(ctx, ChainScanCursorCreate{Network: "tron-mainnet", ChainID: 0})
	require.NoError(t, err)
	now := time.Now().UTC()
	acquired, err := repo.AcquireCursorLease(ctx, "tron-mainnet", "scanner-a", now, now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)

	wantErr := errors.New("saved hash changed")
	require.NoError(t, repo.MarkTRONScanHashConflict(ctx, onchain.NetworkTronMainnet, "scanner-a", now, wantErr))
	cursor, err := client.ChainScanCursor.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, string(onchain.CursorHashConflict), cursor.Health)
	require.Equal(t, "SOLIDIFIED_HASH_CONFLICT", *cursor.LastErrorCode)
	require.Equal(t, wantErr.Error(), *cursor.LastErrorMessage)

	err = repo.CommitTRONScanBlock(ctx, onchain.TRONScanBlockCommit{
		Network: onchain.NetworkTronMainnet, LeaseOwner: "scanner-a", Now: now,
		PreviousHeight: 0,
		Block: onchain.TRONSolidifiedBlock{
			Height: 1, Hash: fmt.Sprintf("%064x", 1), Timestamp: now,
		},
	})
	require.ErrorIs(t, err, onchain.ErrTRONScanHashConflict)
}

func TestOnchainRepositoryListsTRONBalanceReconciliationLedger(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "reconciliation-list")
	_, err := intent.Update().
		SetStatus(string(onchain.IntentSettled)).
		SetReceivedAmountRaw("100000000").
		SetCreditedAmountRaw("100000000").
		Save(ctx)
	require.NoError(t, err)
	deposit, err := repo.CreateDeposit(ctx, DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: intent.Network, ChainID: intent.ChainID, TransactionID: "reconciliation-deposit",
		LogIndex: 0, BlockHeight: 10, BlockHash: "reconciliation-block",
		TokenContract: intent.TokenContract, FromAddress: "sender", ToAddress: intent.DepositAddress,
		AmountRaw: "100000000", TransactionTime: time.Now().UTC(),
	})
	require.NoError(t, err)
	_, err = deposit.Update().SetStatus(string(onchain.DepositCredited)).Save(ctx)
	require.NoError(t, err)
	finalized, err := repo.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID: intent.ID, Network: intent.Network, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DestinationAddress: "hot-wallet",
		BalanceSnapshotRaw: "100000000", AmountRaw: "60000000", IdempotencyKey: "reconciliation-finalized",
	})
	require.NoError(t, err)
	_, err = finalized.Update().SetStatus(string(onchain.TransferFinalized)).Save(ctx)
	require.NoError(t, err)
	_, err = repo.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID: intent.ID, Network: intent.Network, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DestinationAddress: "hot-wallet",
		BalanceSnapshotRaw: "40000000", AmountRaw: "40000000", IdempotencyKey: "reconciliation-in-flight",
		Status: onchain.TransferBroadcast,
	})
	require.NoError(t, err)

	targets, err := repo.ListTRONBalanceReconciliationTargets(ctx, onchain.NetworkTronMainnet, 0, 100)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, intent.ID, targets[0].IntentID)
	require.Equal(t, "100000000", targets[0].RecordedDepositsRaw)
	require.Equal(t, "100000000", targets[0].CreditedAmountRaw)
	require.Equal(t, "60000000", targets[0].FinalizedSweptRaw)
	require.True(t, targets[0].HasInFlightSweep)

	after, err := repo.ListTRONBalanceReconciliationTargets(ctx, onchain.NetworkTronMainnet, intent.ID, 100)
	require.NoError(t, err)
	require.Empty(t, after)
}

func TestOnchainRepositoryMarksTRONBalanceMismatchForReview(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "reconciliation-review")
	_, err := intent.Update().
		SetStatus(string(onchain.IntentSettled)).
		SetReceivedAmountRaw("100000000").
		SetCreditedAmountRaw("100000000").
		Save(ctx)
	require.NoError(t, err)
	deposit, err := repo.CreateDeposit(ctx, DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: intent.Network, ChainID: intent.ChainID, TransactionID: "reconciliation-review-deposit",
		LogIndex: 0, BlockHeight: 10, BlockHash: "reconciliation-review-block",
		TokenContract: intent.TokenContract, FromAddress: "sender", ToAddress: intent.DepositAddress,
		AmountRaw: "100000000", TransactionTime: time.Now().UTC(),
	})
	require.NoError(t, err)
	_, err = deposit.Update().SetStatus(string(onchain.DepositCredited)).SetCreditAuditRef("audit-ref").Save(ctx)
	require.NoError(t, err)
	mismatch := onchain.TRONBalanceReconciliationMismatch{
		IntentID: intent.ID, ReasonCode: "ONCHAIN_BALANCE_MISMATCH",
		Reason:              "ONCHAIN_BALANCE_MISMATCH: expected 100000000, got 90000000",
		RecordedDepositsRaw: "100000000", CreditedAmountRaw: "100000000",
		FinalizedSweptRaw: "0", ExpectedBalanceRaw: "100000000", ActualBalanceRaw: "90000000",
	}
	require.NoError(t, repo.MarkTRONBalanceReconciliationReview(ctx, mismatch))

	loadedIntent, err := client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.IntentReviewRequired), loadedIntent.Status)
	require.Equal(t, onchain.TRONBalanceReconciliationErrorCode, *loadedIntent.LastErrorCode)
	require.Contains(t, *loadedIntent.LastErrorMessage, "expected 100000000")
	loadedDeposit, err := client.OnchainDeposit.Get(ctx, deposit.ID)
	require.NoError(t, err)
	require.Equal(t, string(onchain.DepositReviewRequired), loadedDeposit.Status)
	require.Equal(t, "audit-ref", *loadedDeposit.CreditAuditRef)
	require.Contains(t, *loadedDeposit.ValidationError, "expected 100000000")
	order, err := client.PaymentOrder.Get(ctx, intent.PaymentOrderID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusReviewRequired, order.Status)

	version := loadedIntent.Version
	require.NoError(t, repo.MarkTRONBalanceReconciliationReview(ctx, mismatch))
	loadedIntent, err = client.OnchainPaymentIntent.Get(ctx, intent.ID)
	require.NoError(t, err)
	require.Equal(t, version, loadedIntent.Version, "identical reconciliation findings must be idempotent")
}
