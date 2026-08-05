package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
)

type OnchainReconciliationRuntime struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func ProvideOnchainReconciliationRuntime(store onchain.TRONBalanceReconciliationStore, cfg *config.Config) (*OnchainReconciliationRuntime, error) {
	if store == nil || cfg == nil {
		return nil, fmt.Errorf("onchain reconciliation runtime dependencies are required")
	}
	tron := cfg.Onchain.TRON
	if !tron.Enabled {
		return &OnchainReconciliationRuntime{}, nil
	}
	client, err := onchain.NewJavaTronClient(onchain.JavaTronClientOptions{
		FullNodeURL: tron.FullNodeURL, SolidityNodeURL: tron.SolidityNodeURL,
		Timeout:          time.Duration(tron.RequestTimeoutSeconds) * time.Second,
		ResponseMaxBytes: tron.ResponseMaxBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("create TRON reconciliation node client: %w", err)
	}
	reconciler, err := onchain.NewTRONBalanceReconciler(store, client, onchain.TRONBalanceReconcilerOptions{
		Network: onchain.Network(tron.Network), ContractAddress: tron.USDTContract,
		BatchSize: tron.ScanBatchSize,
	})
	if err != nil {
		return nil, err
	}
	interval := time.Duration(tron.ReconciliationIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Duration(config.DefaultTRONReconciliationSeconds) * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &OnchainReconciliationRuntime{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(runtime.done)
		runTRONBalanceReconciliation(ctx, reconciler, interval)
	}()
	return runtime, nil
}

type tronBalanceReconciliationRunner interface {
	RunOnce(context.Context) (onchain.TRONBalanceReconciliationResult, error)
}

func runTRONBalanceReconciliation(ctx context.Context, reconciler tronBalanceReconciliationRunner, interval time.Duration) {
	for ctx.Err() == nil {
		result, err := reconciler.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("TRON balance reconciliation failed", "targets", result.Targets, "failed", result.Failed, "error", err)
		}
		if result.Mismatches > 0 {
			slog.Error("TRON balance reconciliation found unexplained differences", "mismatches", result.Mismatches, "reviewed", result.Reviewed)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (r *OnchainReconciliationRuntime) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.once.Do(func() {
		r.cancel()
		<-r.done
	})
}
