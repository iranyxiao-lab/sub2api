package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
)

const (
	defaultTRONScanPollInterval = 2 * time.Second
	defaultTRONScanMinBackoff   = time.Second
	defaultTRONScanMaxBackoff   = time.Minute
)

type OnchainScannerRuntime struct {
	cancel context.CancelFunc
	done   chan struct{}
	close  []func()
	once   sync.Once
}

func ProvideOnchainScannerRuntime(store onchain.TRONScanStore, leases onchain.TRONScanLeaseStore, cfg *config.Config) (*OnchainScannerRuntime, error) {
	if store == nil || leases == nil || cfg == nil {
		return nil, fmt.Errorf("onchain scanner runtime dependencies are required")
	}
	type workerRun func(context.Context) error
	workers := make([]workerRun, 0, 2)
	closers := make([]func(), 0, 2)
	closeCreated := func() {
		for _, closeClient := range closers {
			closeClient()
		}
	}

	tron := cfg.Onchain.TRON
	if tron.Enabled && tron.ScannerEnabled {
		client, err := onchain.NewJavaTronClient(onchain.JavaTronClientOptions{
			FullNodeURL: tron.FullNodeURL, SolidityNodeURL: tron.SolidityNodeURL,
			Timeout: time.Duration(tron.RequestTimeoutSeconds) * time.Second, ResponseMaxBytes: tron.ResponseMaxBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("create TRON scanner node client: %w", err)
		}
		if err := initializeTRONScanCursor(store, client, tron); err != nil {
			return nil, err
		}
		scanner, err := onchain.NewTRONScanner(client, store, onchain.TRONScannerOptions{
			Network: onchain.Network(tron.Network), ContractAddress: tron.USDTContract,
			BatchSize: tron.ScanBatchSize, SafetyWindow: tron.ScanSafetyWindow,
		})
		if err != nil {
			return nil, err
		}
		leaseDuration := time.Duration(tron.ScanLeaseSeconds) * time.Second
		worker, err := onchain.NewTRONScanWorker(scanner, leases, onchain.TRONScanWorkerOptions{
			Network: onchain.Network(tron.Network), Owner: tronScannerLeaseOwner(),
			LeaseDuration: leaseDuration, RenewInterval: leaseDuration / 3,
			PollInterval: defaultTRONScanPollInterval, MinBackoff: defaultTRONScanMinBackoff, MaxBackoff: defaultTRONScanMaxBackoff,
		})
		if err != nil {
			return nil, err
		}
		workers = append(workers, worker.Run)
	}

	ethereum := cfg.Onchain.Ethereum
	if ethereum.Enabled && ethereum.ScannerEnabled {
		ethereumStore, ok := store.(onchain.EthereumScanStore)
		if !ok {
			return nil, fmt.Errorf("onchain scanner store does not support Ethereum")
		}
		ethereumLeases, ok := leases.(onchain.EthereumScanLeaseStore)
		if !ok {
			return nil, fmt.Errorf("onchain scanner lease store does not support Ethereum")
		}
		timeout := time.Duration(ethereum.RequestTimeoutSeconds) * time.Second
		clientOptions := func(endpoint string) onchain.EthereumRPCClientOptions {
			return onchain.EthereumRPCClientOptions{
				Endpoint: endpoint, Timeout: timeout, ResponseMaxBytes: ethereum.ResponseMaxBytes,
				BatchLimit: ethereum.RPCBatchLimit, MaxRetries: 2, RetryBackoff: 100 * time.Millisecond,
			}
		}
		primary, err := onchain.NewEthereumRPCClient(context.Background(), clientOptions(ethereum.PrimaryRPCURL))
		if err != nil {
			return nil, fmt.Errorf("create primary Ethereum scanner client: %w", err)
		}
		closers = append(closers, primary.Close)
		backup, err := onchain.NewEthereumRPCClient(context.Background(), clientOptions(ethereum.BackupRPCURL))
		if err != nil {
			closeCreated()
			return nil, fmt.Errorf("create backup Ethereum scanner client: %w", err)
		}
		closers = append(closers, backup.Close)
		source, err := onchain.NewEthereumFailoverScanSource(primary, backup)
		if err != nil {
			closeCreated()
			return nil, err
		}
		if err := initializeEthereumScanCursor(ethereumStore, source, ethereum); err != nil {
			closeCreated()
			return nil, err
		}
		scanner, err := onchain.NewEthereumScanner(source, ethereumStore, onchain.EthereumScannerOptions{
			Network: onchain.Network(ethereum.Network), ContractAddress: ethereum.USDTContract,
			BatchSize: ethereum.ScanBatchSize, MaxLogRange: ethereum.RPCMaxLogRange,
			SafetyWindow: ethereum.ScanSafetyWindow,
		})
		if err != nil {
			closeCreated()
			return nil, err
		}
		leaseDuration := time.Duration(ethereum.ScanLeaseSeconds) * time.Second
		worker, err := onchain.NewEthereumScanWorker(scanner, ethereumLeases, onchain.EthereumScanWorkerOptions{
			Network: onchain.Network(ethereum.Network), Owner: ethereumScannerLeaseOwner(),
			LeaseDuration: leaseDuration, RenewInterval: leaseDuration / 3,
			PollInterval: defaultTRONScanPollInterval, MinBackoff: defaultTRONScanMinBackoff, MaxBackoff: defaultTRONScanMaxBackoff,
		})
		if err != nil {
			closeCreated()
			return nil, err
		}
		workers = append(workers, worker.Run)
	}
	if len(workers) == 0 {
		return &OnchainScannerRuntime{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &OnchainScannerRuntime{cancel: cancel, done: make(chan struct{}), close: closers}
	var workersDone sync.WaitGroup
	workersDone.Add(len(workers))
	for _, run := range workers {
		run := run
		go func() {
			defer workersDone.Done()
			if err := run(ctx); err != nil && ctx.Err() == nil {
				slog.Error("onchain scanner worker stopped", "error", err)
			}
		}()
	}
	go func() {
		workersDone.Wait()
		defer close(runtime.done)
	}()
	return runtime, nil
}

func initializeTRONScanCursor(store onchain.TRONScanStore, client onchain.TRONBlockSource, cfg config.SelfHostedTRONConfig) error {
	if _, err := store.LoadTRONScanCursor(context.Background(), onchain.Network(cfg.Network)); err == nil {
		return nil
	} else if !errors.Is(err, onchain.ErrTRONScanCursorNotFound) {
		return fmt.Errorf("load TRON scan cursor before initialization: %w", err)
	}
	timeout := 2 * time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	height, err := client.LatestSolidifiedHeight(ctx)
	if err != nil {
		return fmt.Errorf("read initial TRON solidified height: %w", err)
	}
	block, err := client.SolidifiedBlockByHeight(ctx, height)
	if err != nil {
		return fmt.Errorf("read initial TRON solidified block %d: %w", height, err)
	}
	definition, ok := onchain.NetworkDefinition(onchain.Network(cfg.Network))
	if !ok {
		return fmt.Errorf("unknown TRON network %q", cfg.Network)
	}
	if err := store.InitializeTRONScanCursor(ctx, onchain.TRONScanCursorInitialization{
		Network: onchain.Network(cfg.Network), ChainID: int64(definition.ChainID),
		FinalizedHeight: block.Height, FinalizedHash: block.Hash, InitializedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("initialize TRON scan cursor: %w", err)
	}
	return nil
}

func initializeEthereumScanCursor(store onchain.EthereumScanStore, source *onchain.EthereumFailoverScanSource, cfg config.SelfHostedEthereumConfig) error {
	if _, err := store.LoadEthereumScanCursor(context.Background(), onchain.Network(cfg.Network)); err == nil {
		return nil
	} else if !errors.Is(err, onchain.ErrEthereumScanCursorNotFound) {
		return fmt.Errorf("load Ethereum scan cursor before initialization: %w", err)
	}
	timeout := 4 * time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	finalized, err := source.CommonFinalizedBlock(ctx)
	if err != nil {
		return fmt.Errorf("read initial common Ethereum finalized block: %w", err)
	}
	if cfg.ChainID > uint64(1<<63-1) {
		return fmt.Errorf("Ethereum chain ID exceeds persistent cursor range")
	}
	if err := store.InitializeEthereumScanCursor(ctx, onchain.EthereumScanCursorInitialization{
		Network: onchain.Network(cfg.Network), ChainID: int64(cfg.ChainID),
		FinalizedHeight: finalized.Number, FinalizedHash: finalized.Hash, InitializedAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("initialize Ethereum scan cursor: %w", err)
	}
	return nil
}

func tronScannerLeaseOwner() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "unknown-host"
	}
	owner := fmt.Sprintf("sub2api-tron-scanner:%s:%d", strings.TrimSpace(hostname), os.Getpid())
	if len(owner) > 128 {
		owner = owner[:128]
	}
	return owner
}

func ethereumScannerLeaseOwner() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "unknown-host"
	}
	owner := fmt.Sprintf("sub2api-ethereum-scanner:%s:%d", strings.TrimSpace(hostname), os.Getpid())
	if len(owner) > 128 {
		owner = owner[:128]
	}
	return owner
}

func (r *OnchainScannerRuntime) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.once.Do(func() {
		r.cancel()
		<-r.done
		for _, closeClient := range r.close {
			closeClient()
		}
	})
}
