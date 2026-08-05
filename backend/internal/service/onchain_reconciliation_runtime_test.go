package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
)

type reconciliationRuntimeTestRunner struct {
	calls chan struct{}
}

func (r *reconciliationRuntimeTestRunner) RunOnce(context.Context) (onchain.TRONBalanceReconciliationResult, error) {
	r.calls <- struct{}{}
	return onchain.TRONBalanceReconciliationResult{}, nil
}

func TestRunTRONBalanceReconciliationRunsPeriodicallyAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	runner := &reconciliationRuntimeTestRunner{calls: make(chan struct{}, 3)}
	go func() {
		defer close(done)
		runTRONBalanceReconciliation(ctx, runner, time.Millisecond)
	}()

	for range 2 {
		select {
		case <-runner.calls:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for periodic reconciliation")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconciliation runtime did not stop")
	}
}
