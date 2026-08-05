package repository

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/onchaindeposit"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOnchainAdminQueriesFilterAndTraceFunds(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	intent := createOnchainTestIntent(t, ctx, repo, client, "admin-query")
	transactionTime := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.EnsureDeposit(ctx, DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: intent.Network, ChainID: intent.ChainID, TransactionID: "tx-admin-query", LogIndex: 7,
		TransactionIndex: 3, BlockHeight: 125, BlockHash: "block-admin-query",
		TokenContract: intent.TokenContract, FromAddress: "sender-admin-query", ToAddress: intent.DepositAddress,
		AmountRaw: "5000000", TransactionTime: transactionTime,
	}))
	deposit, err := client.OnchainDeposit.Query().Where(onchaindeposit.TransactionIDEQ("tx-admin-query")).Only(ctx)
	require.NoError(t, err)
	_, err = deposit.Update().SetStatus("REVIEW_REQUIRED").Save(ctx)
	require.NoError(t, err)

	finalizedSweep, err := repo.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID: intent.ID, Network: intent.Network, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DestinationAddress: "sweep-admin-query",
		BalanceSnapshotRaw: "5000000", AmountRaw: "1000000", IdempotencyKey: "sweep-admin-query-finalized",
	})
	require.NoError(t, err)
	_, err = finalizedSweep.Update().SetStatus(string(onchain.TransferFinalized)).SetRetryCount(1).Save(ctx)
	require.NoError(t, err)
	resourceWaitSweep, err := repo.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID: intent.ID, Network: intent.Network, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DestinationAddress: "sweep-admin-query",
		BalanceSnapshotRaw: "4000000", AmountRaw: "3000000", IdempotencyKey: "sweep-admin-query-resource",
		Status: onchain.TransferResourceWait,
	})
	require.NoError(t, err)
	_, err = resourceWaitSweep.Update().SetRetryCount(2).Save(ctx)
	require.NoError(t, err)
	failedSweep, err := repo.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID: intent.ID, Network: intent.Network, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DestinationAddress: "sweep-admin-query",
		BalanceSnapshotRaw: "4000000", AmountRaw: "1000000", IdempotencyKey: "sweep-admin-query-failed",
		Status: onchain.TransferFailed,
	})
	require.NoError(t, err)
	_, err = failedSweep.Update().SetRetryCount(3).Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(intent.PaymentOrderID, 10)).
		SetAction("ONCHAIN_BALANCE_CREDITED").
		SetDetail(`{"credited_amount_raw":"5000000"}`).
		SetOperator("system:onchain-settlement").
		Save(ctx)
	require.NoError(t, err)

	logIndex := int64(7)
	items, total, err := repo.ListAdminOnchainDeposits(ctx, service.AdminOnchainDepositQuery{
		Page: 1, PageSize: 20, Network: intent.Network, TransactionID: "tx-admin-query",
		Address: intent.DepositAddress, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		LogIndex: &logIndex,
	})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, "REVIEW_REQUIRED", items[0].Status)
	require.Equal(t, "5000000", items[0].AmountRaw)
	require.True(t, items[0].ReceiptSuccess)

	trace, err := repo.GetAdminOnchainOrderTrace(ctx, intent.PaymentOrderID)
	require.NoError(t, err)
	require.NotNil(t, trace)
	require.Equal(t, intent.DepositAddress, trace.Intent.DepositAddress)
	require.Len(t, trace.Deposits, 1)
	require.Len(t, trace.BalanceAudits, 1)
	require.Equal(t, "ONCHAIN_BALANCE_CREDITED", trace.BalanceAudits[0].Action)
	require.Len(t, trace.Sweeps, 3)
	require.Empty(t, trace.GasFundings)

	cursor, err := repo.CreateChainScanCursor(ctx, ChainScanCursorCreate{Network: intent.Network, ChainID: intent.ChainID})
	require.NoError(t, err)
	_, err = intent.Update().SetLastErrorCode(onchain.TRONBalanceReconciliationErrorCode).SetLastErrorMessage("balance mismatch").Save(ctx)
	require.NoError(t, err)
	_, err = cursor.Update().SetHealth("HEALTHY").SetFinalizedHeight(125).SetFinalizedHash("block-admin-query").SetLastSuccessAt(transactionTime).Save(ctx)
	require.NoError(t, err)
	health, err := repo.ListAdminOnchainHealth(ctx)
	require.NoError(t, err)
	require.Len(t, health, 1)
	require.Equal(t, "HEALTHY", health[0].CursorHealth)
	require.Equal(t, 1, health[0].PendingSettlements)
	require.Equal(t, 1, health[0].ReviewRequired)
	require.Equal(t, 1, health[0].ReconciliationMismatches)
	require.Equal(t, "4000000", health[0].UnsweptBalanceRaw)
	require.Equal(t, 1, health[0].ResourceWaitSweeps)
	require.Equal(t, 6, health[0].SweepRetryCount)
	require.Equal(t, 3, health[0].SweepMaxRetryCount)
	require.Equal(t, 1, health[0].SweepFailureCount)
}

func TestOnchainAdminOrderTraceReturnsNilForNonOnchainOrder(t *testing.T) {
	ctx := context.Background()
	repo, _ := newOnchainRepoSQLite(t)
	trace, err := repo.GetAdminOnchainOrderTrace(ctx, 999999)
	require.NoError(t, err)
	require.Nil(t, trace)
}

func TestOnchainAdminHealthAggregatesEthereumOperations(t *testing.T) {
	ctx := context.Background()
	repo, client := newOnchainRepoSQLite(t)
	tronIntent := createOnchainTestIntent(t, ctx, repo, client, "admin-ethereum-metrics")
	require.NoError(t, client.OnchainPaymentIntent.DeleteOneID(tronIntent.ID).Exec(ctx))
	intent, err := repo.CreatePaymentIntent(ctx, PaymentIntentCreate{
		PaymentOrderID: tronIntent.PaymentOrderID, UserID: tronIntent.UserID,
		Network: string(onchain.NetworkEthereumMainnet), ChainID: 1,
		TokenContract:   onchain.EthereumMainnetUSDTContract,
		DepositAddress:  "0x2222222222222222222222222222222222222222",
		DerivationIndex: tronIntent.DerivationIndex, ExpectedAmountRaw: "250000000",
		ConfigSnapshot: map[string]any{"network": string(onchain.NetworkEthereumMainnet)}, ConfigVersion: "test-v1",
	})
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	_, err = repo.CreateChainScanCursor(ctx, ChainScanCursorCreate{Network: string(onchain.NetworkEthereumMainnet), ChainID: 1})
	require.NoError(t, err)
	require.NoError(t, repo.EnsureDeposit(ctx, DepositCreate{
		IntentID: intent.ID, PaymentOrderID: intent.PaymentOrderID, UserID: intent.UserID,
		Network: intent.Network, ChainID: intent.ChainID, TransactionID: "0xdeposit", LogIndex: 0,
		BlockHeight: 100, BlockHash: "0xblock", TokenContract: intent.TokenContract,
		FromAddress: "0x3333333333333333333333333333333333333333", ToAddress: intent.DepositAddress,
		AmountRaw: "250000000", TransactionTime: now,
	}))

	finalizedSweep, err := repo.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID: intent.ID, Network: intent.Network, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DestinationAddress: "0x4444444444444444444444444444444444444444",
		BalanceSnapshotRaw: "250000000", AmountRaw: "50000000", IdempotencyKey: "admin-ethereum-finalized-sweep",
	})
	require.NoError(t, err)
	_, err = finalizedSweep.Update().SetStatus(string(onchain.TransferFinalized)).SetFeeRaw("100").Save(ctx)
	require.NoError(t, err)
	pendingSweep, err := repo.CreateWalletSweep(ctx, WalletSweepCreate{
		IntentID: intent.ID, Network: intent.Network, ChainID: intent.ChainID,
		SourceAddress: intent.DepositAddress, DestinationAddress: "0x4444444444444444444444444444444444444444",
		BalanceSnapshotRaw: "200000000", AmountRaw: "200000000", IdempotencyKey: "admin-ethereum-pending-sweep",
		Status: onchain.TransferBroadcast,
	})
	require.NoError(t, err)
	_, err = pendingSweep.Update().SetReplacementOfID(finalizedSweep.ID).SetFailureCode("RPC_TIMEOUT").SetUpdatedAt(now.Add(-20 * time.Minute)).Save(ctx)
	require.NoError(t, err)

	finalizedFunding, err := repo.CreateEthereumGasFunding(ctx, EthereumGasFundingCreate{
		IntentID: intent.ID, ChainID: 1, SponsorAddress: "0x1111111111111111111111111111111111111111",
		TargetAddress: intent.DepositAddress, DerivationIndex: intent.DerivationIndex,
		AmountWei: "1000", IdempotencyKey: "admin-ethereum-finalized-funding", Nonce: 7,
		GasLimit: 21000, MaxFeePerGasWei: "10", MaxPriorityFeePerGasWei: "1",
	})
	require.NoError(t, err)
	_, err = finalizedFunding.Update().SetStatus(string(onchain.TransferFinalized)).SetFinalized(true).SetActualFeeWei("200").Save(ctx)
	require.NoError(t, err)
	pendingFunding, err := repo.CreateEthereumGasFunding(ctx, EthereumGasFundingCreate{
		IntentID: intent.ID, ChainID: 1, SponsorAddress: "0x1111111111111111111111111111111111111111",
		TargetAddress: intent.DepositAddress, DerivationIndex: intent.DerivationIndex,
		AmountWei: "1000", IdempotencyKey: "admin-ethereum-pending-funding", Nonce: 8,
		GasLimit: 21000, MaxFeePerGasWei: "10", MaxPriorityFeePerGasWei: "1",
	})
	require.NoError(t, err)
	_, err = pendingFunding.Update().SetStatus(string(onchain.TransferConfirming)).SetReplacementOfID(finalizedFunding.ID).SetUpdatedAt(now.Add(-10 * time.Minute)).Save(ctx)
	require.NoError(t, err)
	_, err = client.EthereumNonceState.Create().SetChainID(1).
		SetSenderAddress("0x1111111111111111111111111111111111111111").
		SetNextNonce(9).SetObservedPendingNonce(7).SetStatus(string(onchain.NonceConflict)).Save(ctx)
	require.NoError(t, err)

	health, err := repo.ListAdminOnchainHealth(ctx)
	require.NoError(t, err)
	require.Len(t, health, 1)
	item := health[0]
	require.Equal(t, "200000000", item.UnsweptBalanceRaw)
	require.Equal(t, "300", item.GasCostWei)
	require.Equal(t, 1, item.RPCErrorCount)
	require.Equal(t, 2, item.PendingNonceCount)
	require.Equal(t, 1, item.NonceConflictCount)
	require.Equal(t, 2, item.PendingTransactionCount)
	require.GreaterOrEqual(t, item.StuckTransactionAgeSeconds, int64(19*60))
	require.Equal(t, 2, item.ReplacementTransactionCount)
	require.Equal(t, "0x1111111111111111111111111111111111111111", item.GasSponsorAddress)

	trace, err := repo.GetAdminOnchainOrderTrace(ctx, intent.PaymentOrderID)
	require.NoError(t, err)
	require.NotNil(t, trace)
	require.Len(t, trace.Deposits, 1)
	require.True(t, trace.Deposits[0].ReceiptSuccess)
	require.Len(t, trace.Sweeps, 2)
	require.Len(t, trace.GasFundings, 2)
	require.Len(t, trace.NonceStates, 1)
	require.Equal(t, int64(9), trace.NonceStates[0].NextNonce)
	require.Equal(t, int64(7), trace.NonceStates[0].ObservedPendingNonce)
	require.Equal(t, string(onchain.NonceConflict), trace.NonceStates[0].Status)
}
