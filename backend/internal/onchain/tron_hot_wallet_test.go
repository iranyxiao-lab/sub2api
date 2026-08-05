package onchain

import (
	"context"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

type staticTRONHotWalletBalanceSource struct {
	balance *big.Int
	err     error
}

func (s staticTRONHotWalletBalanceSource) TRC20Balance(context.Context, string, string) (*big.Int, error) {
	if s.balance == nil {
		return nil, s.err
	}
	return new(big.Int).Set(s.balance), s.err
}

func TestTRONHotWalletMonitorThresholds(t *testing.T) {
	tests := []struct {
		name             string
		balance          int64
		warning          bool
		approvalRequired bool
		paused           bool
	}{
		{name: "below warning", balance: 49_999_999_999},
		{name: "warning", balance: 50_000_000_000, warning: true},
		{name: "cold approval", balance: 100_000_000_000, warning: true, approvalRequired: true, paused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor, err := NewTRONHotWalletMonitor(
				staticTRONHotWalletBalanceSource{balance: big.NewInt(tt.balance)},
				NetworkTronMainnet, TronMainnetUSDTContract, javaTronAccountTestAddress,
				"50000000000", "100000000000",
			)
			require.NoError(t, err)
			status, err := monitor.Check(context.Background())
			require.NoError(t, err)
			require.Equal(t, tt.warning, status.Warning)
			require.Equal(t, tt.approvalRequired, status.ColdTransferApprovalRequired)
			require.Equal(t, tt.paused, status.AutomaticSweepsPaused)
			require.Equal(t, javaTronAccountTestAddress, status.Address)
		})
	}
}

func TestTRONHotWalletMonitorRejectsInvalidPolicy(t *testing.T) {
	source := staticTRONHotWalletBalanceSource{balance: big.NewInt(1)}
	_, err := NewTRONHotWalletMonitor(source, NetworkTronMainnet, TronMainnetUSDTContract, javaTronAccountTestAddress, "100", "100")
	require.ErrorContains(t, err, "below")
	_, err = NewTRONHotWalletMonitor(source, NetworkTronMainnet, TronMainnetUSDTContract, javaTronAccountTestAddress, "050", "100")
	require.ErrorContains(t, err, "canonical")
}
