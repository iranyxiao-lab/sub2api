package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/stretchr/testify/require"
)

type sweepTrackingRuntimeTestRunner struct {
	calls chan struct{}
}

type sweepTrackingRuntimeTestStore struct {
	calls chan bool
}

func (s *sweepTrackingRuntimeTestStore) ListTRONSweepExecutionTasks(_ context.Context, _ onchain.Network, _ time.Time, _ int, includeSigning bool) ([]onchain.TRONSweepExecutionTask, error) {
	select {
	case s.calls <- includeSigning:
	default:
	}
	return nil, nil
}

func (*sweepTrackingRuntimeTestStore) MarkTRONSweepSigning(context.Context, int64, int, string) error {
	return nil
}

func (*sweepTrackingRuntimeTestStore) RecordTRONSweepBroadcast(context.Context, int64, int, string, string) error {
	return nil
}

func (*sweepTrackingRuntimeTestStore) RecordTRONSweepRetry(context.Context, int64, int, onchain.TransferStatus, string, string, time.Time, bool) error {
	return nil
}

func (*sweepTrackingRuntimeTestStore) ScheduleTRONSweepCheck(context.Context, int64, int, onchain.TransferStatus, time.Time) error {
	return nil
}

func (*sweepTrackingRuntimeTestStore) MarkTRONSweepConfirming(context.Context, int64, int) error {
	return nil
}

func (*sweepTrackingRuntimeTestStore) FinalizeTRONSweep(context.Context, onchain.TRONSweepFinalization) error {
	return nil
}

func (r *sweepTrackingRuntimeTestRunner) TrackSolidificationOnce(context.Context) (onchain.TRONSweepExecutionResult, error) {
	r.calls <- struct{}{}
	return onchain.TRONSweepExecutionResult{}, nil
}

func TestRunTRONSweepTrackingRunsPeriodicallyAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	runner := &sweepTrackingRuntimeTestRunner{calls: make(chan struct{}, 3)}
	go func() {
		defer close(done)
		runTRONSweepTracking(ctx, runner, time.Millisecond)
	}()

	for range 2 {
		select {
		case <-runner.calls:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for sweep solidification tracking")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sweep tracking runtime did not stop")
	}
}

func TestDisabledOnchainSettlementRuntimeStopsSafely(t *testing.T) {
	runtime := &OnchainSettlementRuntime{}
	runtime.Stop()
	runtime.Stop()
}

func TestProvideOnchainSweepRuntimeTracksWithoutPlannerOrSignerWhenSweeperDisabled(t *testing.T) {
	node := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(node.Close)
	store := &sweepTrackingRuntimeTestStore{calls: make(chan bool, 1)}
	cfg := &config.Config{Onchain: config.OnchainConfig{TRON: config.SelfHostedTRONConfig{
		Enabled: true, SweeperEnabled: false, Network: string(onchain.NetworkTronNile),
		FullNodeURL: node.URL, SolidityNodeURL: node.URL,
		USDTContract: onchain.TronMainnetUSDTContract, SweepAddress: onchain.TronMainnetUSDTContract,
		RequestTimeoutSeconds: 1, ResponseMaxBytes: 1024,
		SweepBatchSize: 10, SweepMaxFailures: 3, SweepRetrySeconds: 1, SweepConfirmSeconds: 1,
	}}}

	runtime, err := ProvideOnchainSweepRuntime(nil, store, cfg)
	require.NoError(t, err)
	t.Cleanup(runtime.Stop)
	select {
	case includeSigning := <-store.calls:
		require.False(t, includeSigning)
	case <-time.After(time.Second):
		t.Fatal("tracking-only sweep runtime did not query confirmation tasks")
	}
}
