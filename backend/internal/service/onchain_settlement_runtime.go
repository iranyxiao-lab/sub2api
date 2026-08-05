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

type OnchainSettlementRuntime struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func ProvideOnchainSettlementRuntime(store onchain.SettlementStore, paymentService *PaymentService, cfg *config.Config) (*OnchainSettlementRuntime, error) {
	if store == nil || paymentService == nil || cfg == nil {
		return nil, fmt.Errorf("onchain settlement runtime dependencies are required")
	}
	type networkSettlement struct {
		enabled   bool
		network   onchain.Network
		batchSize int
	}
	networks := []networkSettlement{
		{enabled: cfg.Onchain.TRON.Enabled && cfg.Onchain.TRON.SettlementEnabled, network: onchain.Network(cfg.Onchain.TRON.Network), batchSize: cfg.Onchain.TRON.ScanBatchSize},
		{enabled: cfg.Onchain.Ethereum.Enabled && cfg.Onchain.Ethereum.SettlementEnabled, network: onchain.Network(cfg.Onchain.Ethereum.Network), batchSize: int(cfg.Onchain.Ethereum.ScanBatchSize)},
	}
	workers := make([]*onchain.SettlementWorker, 0, len(networks))
	for _, item := range networks {
		if !item.enabled {
			continue
		}
		if item.batchSize <= 0 {
			item.batchSize = 100
		}
		worker, err := onchain.NewSettlementWorker(store, paymentService, onchain.SettlementWorkerOptions{
			Network: item.network, BatchSize: item.batchSize, PollInterval: 2 * time.Second,
			MinRetryBackoff: time.Second, MaxRetryBackoff: time.Minute, ClassifyError: ClassifyOnchainSettlementError,
		})
		if err != nil {
			return nil, err
		}
		workers = append(workers, worker)
	}
	if len(workers) == 0 {
		return &OnchainSettlementRuntime{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &OnchainSettlementRuntime{cancel: cancel, done: make(chan struct{})}
	var workersDone sync.WaitGroup
	workersDone.Add(len(workers))
	for _, worker := range workers {
		worker := worker
		go func() {
			defer workersDone.Done()
			if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
				slog.Error("onchain settlement worker stopped", "error", err)
			}
		}()
	}
	go func() {
		workersDone.Wait()
		defer close(runtime.done)
	}()
	return runtime, nil
}

func (r *OnchainSettlementRuntime) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.once.Do(func() {
		r.cancel()
		<-r.done
	})
}
