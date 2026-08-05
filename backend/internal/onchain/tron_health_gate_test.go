package onchain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTRONHealthGateFailsClosedBeforeFirstCheck(t *testing.T) {
	gate := NewTRONHealthGate()

	require.False(t, gate.NewOrdersAllowed())
	require.True(t, gate.ExistingAddressProcessingAllowed())

	var gateErr *TRONOrderGateError
	require.ErrorAs(t, gate.RequireNewOrder(), &gateErr)
	require.Equal(t, TRONRechargeUnavailable, gateErr.Code)
}

func TestTRONHealthGateKeepsRecoveryEnabledAcrossHealthChanges(t *testing.T) {
	gate := NewTRONHealthGate()
	gate.Record(TRONHealthReport{Healthy: true, Network: NetworkTronMainnet}, nil)
	require.True(t, gate.NewOrdersAllowed())
	require.NoError(t, gate.RequireNewOrder())

	healthErr := &TRONHealthError{Code: TRONHealthScanLag, Cause: errors.New("node is catching up")}
	gate.Record(TRONHealthReport{Network: NetworkTronMainnet, BlockLag: 50}, healthErr)
	require.False(t, gate.NewOrdersAllowed())
	require.True(t, gate.ExistingAddressProcessingAllowed())
	require.ErrorIs(t, gate.RequireNewOrder(), healthErr)

	gate.Record(TRONHealthReport{Healthy: true, Network: NetworkTronMainnet}, nil)
	require.True(t, gate.NewOrdersAllowed())
	require.True(t, gate.ExistingAddressProcessingAllowed())
}

func TestTRONHealthGateRefreshRecordsClientResult(t *testing.T) {
	server := newJavaTronHealthServer(t, javaTronHealthFixture{
		p2pVersion: TronMainnetP2PVersion,
		latest:     105,
		solid:      100,
		decimals:   USDTDecimals,
	})
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)
	gate := NewTRONHealthGate()

	report, err := gate.Refresh(context.Background(), client, TRONHealthCheckOptions{
		Network:          NetworkTronMainnet,
		USDTContract:     TronMainnetUSDTContract,
		ContractCaller:   TronMainnetUSDTContract,
		ExpectedDecimals: USDTDecimals,
		MaxBlockLag:      10,
	})

	require.NoError(t, err)
	require.True(t, report.Healthy)
	require.True(t, gate.NewOrdersAllowed())
	require.Equal(t, report, gate.Snapshot().Report)
}

func TestTRONHealthGateRefreshClosesOrdersOnNodeFailures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		mutate  func(*JavaTronClientOptions)
		kind    JavaTronErrorKind
	}{
		{
			name: "error response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			},
			mutate: func(options *JavaTronClientOptions) { options.MaxAttempts = 1 },
			kind:   JavaTronErrorHTTP,
		},
		{
			name: "timeout",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(30 * time.Millisecond)
				_, _ = w.Write([]byte(`{}`))
			},
			mutate: func(options *JavaTronClientOptions) {
				options.Timeout = 5 * time.Millisecond
				options.MaxAttempts = 1
			},
			kind: JavaTronErrorTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			t.Cleanup(server.Close)
			client := newJavaTronTestClient(t, server.URL, server.URL, tt.mutate)
			gate := NewTRONHealthGate()

			_, err := gate.Refresh(context.Background(), client, TRONHealthCheckOptions{
				Network:          NetworkTronMainnet,
				USDTContract:     TronMainnetUSDTContract,
				ContractCaller:   TronMainnetUSDTContract,
				ExpectedDecimals: USDTDecimals,
				MaxBlockLag:      10,
			})

			var healthErr *TRONHealthError
			require.ErrorAs(t, err, &healthErr)
			require.Equal(t, TRONHealthNodeUnavailable, healthErr.Code)
			var clientErr *JavaTronError
			require.ErrorAs(t, err, &clientErr)
			require.Equal(t, tt.kind, clientErr.Kind)
			require.False(t, gate.NewOrdersAllowed())
			require.True(t, gate.ExistingAddressProcessingAllowed())
			require.Error(t, gate.RequireNewOrder())
		})
	}
}
