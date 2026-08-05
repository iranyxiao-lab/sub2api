package service

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/stretchr/testify/require"
)

type onchainAdminHealthTestRepository struct {
	health []AdminOnchainHealth
}

func (r *onchainAdminHealthTestRepository) ListAdminOnchainHealth(context.Context) ([]AdminOnchainHealth, error) {
	return append([]AdminOnchainHealth(nil), r.health...), nil
}

func (*onchainAdminHealthTestRepository) ListAdminOnchainDeposits(context.Context, AdminOnchainDepositQuery) ([]AdminOnchainDeposit, int, error) {
	return nil, 0, nil
}

func (*onchainAdminHealthTestRepository) GetAdminOnchainOrderTrace(context.Context, int64) (*AdminOnchainOrderTrace, error) {
	return nil, nil
}

func TestGetAdminOnchainHealthCombinesNodeScanResourceAndSignerMetrics(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	repo := &onchainAdminHealthTestRepository{health: []AdminOnchainHealth{{
		Network: string(onchain.NetworkTronMainnet), CursorHealth: "HEALTHY",
		FinalizedHeight: 140, LastSuccessAt: timePointer(now.Add(-2 * time.Minute)),
		UnsweptBalanceRaw: "42000000", ResourceWaitSweeps: 2, SweepRetryCount: 4, SweepFailureCount: 1,
	}}}
	svc := &PaymentService{
		onchainAdminRepo: repo,
		tronConfig: &config.SelfHostedTRONConfig{
			Network: string(onchain.NetworkTronMainnet), SweepAddress: "TSweep",
		},
		tronOrderHealthCheck: func(context.Context) (onchain.TRONHealthReport, error) {
			return onchain.TRONHealthReport{
				Healthy: true, FullNodeHeight: 150, SolidHeight: 145, BlockLag: 5, CheckedAt: now,
			}, nil
		},
		tronResourceCheck: func(context.Context) (onchain.TRONAccountState, error) {
			return onchain.TRONAccountState{
				TRXBalanceSun: 3_000_000, EnergyLimit: 100_000, EnergyUsed: 25_000,
				FreeNetLimit: 1_000, FreeNetUsed: 200, NetLimit: 500, NetUsed: 100,
			}, nil
		},
		tronSignerHealthCheck: func(context.Context) error { return nil },
	}

	items, err := svc.GetAdminOnchainHealth(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	item := items[0]
	require.True(t, item.NodeHealthy)
	require.Equal(t, int64(150), item.FullNodeHeight)
	require.Equal(t, int64(145), item.SolidHeight)
	require.Equal(t, int64(5), item.NodeBlockLag)
	require.Equal(t, int64(5), item.ScanLagBlocks)
	require.InDelta(t, 120, item.ScanLagSeconds, 2)
	require.Equal(t, int64(3_000_000), item.TRXBalanceSun)
	require.Equal(t, int64(75_000), item.AvailableEnergy)
	require.Equal(t, int64(1_200), item.AvailableBandwidth)
	require.True(t, item.SignerHealthy)
	require.Equal(t, "42000000", item.UnsweptBalanceRaw)
}

func TestEvaluateTRONAlertsCoversConfiguredOperationalFailures(t *testing.T) {
	errorCode := "SOLIDIFIED_HASH_CONFLICT"
	alerts := evaluateTRONAlerts(AdminOnchainHealth{
		NodeHealthy: false, NodeBlockLag: 25, CursorHealth: string(onchain.CursorHashConflict),
		LastSuccessAt: timePointer(time.Now().Add(-3 * time.Minute)), LastErrorCode: &errorCode,
		ScanLagSeconds: 180, PendingSettlements: 100, ResourceWaitSweeps: 1,
		SweepMaxRetryCount: 3, ReconciliationMismatches: 1, SignerHealthy: false, HotWalletWarning: true,
		HotWalletBalanceRaw: "50000000000",
	}, config.SelfHostedTRONConfig{
		Enabled: true, MaxBlockLag: 20, ScanStallAlertSeconds: 120,
		SettlementBacklogAlert: 100, ResourceWaitAlert: 1,
		SweepRequiredEnergy: 130000, SweepRequiredBandwidth: 400, SweepMinimumTRXSun: 100000000,
		SweepMaxFailures: 3, HotWalletWarningRaw: "50000000000", HotWalletApprovalRaw: "100000000000",
	})

	codes := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		codes = append(codes, alert.Code)
	}
	require.ElementsMatch(t, []string{
		"NODE_UNSYNCED", "HASH_CONFLICT", "SCAN_STALLED", "SETTLEMENT_BACKLOG",
		"RESOURCE_INSUFFICIENT", "SWEEP_CONSECUTIVE_FAILURES", "SIGNER_UNAVAILABLE", "HOT_WALLET_LIMIT",
		"RECONCILIATION_MISMATCH",
	}, codes)
}

func TestEvaluateTRONAlertsReturnsEmptyForHealthyMetrics(t *testing.T) {
	alerts := evaluateTRONAlerts(AdminOnchainHealth{
		NodeHealthy: true, NodeBlockLag: 5, CursorHealth: string(onchain.CursorHealthy),
		LastSuccessAt: timePointer(time.Now()), ScanLagSeconds: 5, PendingSettlements: 2,
		TRXBalanceSun: 100000000, AvailableEnergy: 130000, AvailableBandwidth: 400,
		SignerHealthy: true,
	}, config.SelfHostedTRONConfig{
		Enabled: true, MaxBlockLag: 20, ScanStallAlertSeconds: 120,
		SettlementBacklogAlert: 100, ResourceWaitAlert: 1,
		SweepRequiredEnergy: 130000, SweepRequiredBandwidth: 400, SweepMinimumTRXSun: 100000000,
		SweepMaxFailures: 3,
	})
	require.Empty(t, alerts)
}

func TestGetAdminOnchainHealthCombinesEthereumNodeAndWalletMetrics(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	repo := &onchainAdminHealthTestRepository{health: []AdminOnchainHealth{{
		Network: string(onchain.NetworkEthereumMainnet), CursorHealth: string(onchain.CursorHealthy),
		FinalizedHeight: 175, LastSuccessAt: timePointer(now.Add(-time.Minute)),
		GasSponsorAddress: "0x1111111111111111111111111111111111111111",
		PendingNonceCount: 1, PendingTransactionCount: 2, ReplacementTransactionCount: 1,
		UnsweptBalanceRaw: "250000000", GasCostWei: "90000000000000000",
	}}}
	svc := &PaymentService{
		onchainAdminRepo: repo,
		ethereumConfig: &config.SelfHostedEthereumConfig{
			Enabled: true, Network: string(onchain.NetworkEthereumMainnet), ChainID: 1,
			MaxFinalizedLag: 64, ScanStallAlertSeconds: 180, SettlementBacklogAlert: 100,
			PendingNonceAlert: 2, StuckAlertSeconds: 900, ReplacementAlert: 2,
			UnsweptBalanceAlertRaw: "300000000", GasCostAlertWei: "100000000000000000",
			GasSponsorMinWei: "10000000000000000",
		},
		ethereumOrderHealthCheck: func(context.Context) (onchain.EthereumStartupReport, error) {
			return onchain.EthereumStartupReport{
				Primary:         onchain.EthereumEndpointHealth{Latest: 200, Finalized: onchain.EthereumBlockRef{Number: 180}},
				Backup:          onchain.EthereumEndpointHealth{Latest: 199, Finalized: onchain.EthereumBlockRef{Number: 180}},
				CommonFinalized: onchain.EthereumBlockRef{Number: 180},
			}, nil
		},
		ethereumBalanceCheck: func(context.Context, string) (*big.Int, error) {
			return new(big.Int).SetUint64(20_000_000_000_000_000), nil
		},
	}

	items, err := svc.GetAdminOnchainHealth(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	item := items[0]
	require.True(t, item.NodeHealthy)
	require.True(t, item.NodeConsistent)
	require.Equal(t, int64(200), item.PrimaryLatestHeight)
	require.Equal(t, int64(199), item.BackupLatestHeight)
	require.Equal(t, int64(180), item.CommonFinalizedHeight)
	require.Equal(t, int64(20), item.FinalizedLagBlocks)
	require.Equal(t, int64(5), item.ScanLagBlocks)
	require.Equal(t, "20000000000000000", item.GasSponsorBalanceWei)
	require.Empty(t, item.Alerts)
}

func TestEvaluateEthereumAlertsCoversOperationalFailures(t *testing.T) {
	errorCode := "FINALIZED_HASH_CONFLICT"
	alerts := evaluateEthereumAlerts(AdminOnchainHealth{
		NodeHealthy: false, NodeConsistent: false, FinalizedLagBlocks: 20,
		CursorHealth: string(onchain.CursorHashConflict), LastErrorCode: &errorCode,
		LastSuccessAt: timePointer(time.Now()), ScanLagSeconds: 60, PendingSettlements: 2,
		GasSponsorBalanceWei: "5", PendingNonceCount: 1, NonceConflictCount: 1,
		PendingTransactionCount: 1, StuckTransactionAgeSeconds: 60,
		ReplacementTransactionCount: 1, UnsweptBalanceRaw: "200", GasCostWei: "100",
	}, config.SelfHostedEthereumConfig{
		Enabled: true, MaxFinalizedLag: 10, ScanStallAlertSeconds: 30,
		SettlementBacklogAlert: 2, GasSponsorMinWei: "10", PendingNonceAlert: 1,
		StuckAlertSeconds: 60, ReplacementAlert: 1, UnsweptBalanceAlertRaw: "200", GasCostAlertWei: "100",
	})

	codes := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		codes = append(codes, alert.Code)
	}
	require.ElementsMatch(t, []string{
		"NODE_DIVERGENCE", "FINALIZED_DELAY", "HASH_CONFLICT", "SCAN_STALLED",
		"SETTLEMENT_BACKLOG", "ETH_LOW", "PENDING_NONCE", "NONCE_CONFLICT",
		"TRANSACTION_STUCK", "TRANSACTION_REPLACEMENTS", "UNSWEPT_USDT", "GAS_COST_HIGH",
	}, codes)
}

func timePointer(value time.Time) *time.Time { return &value }
