package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
)

const defaultTRONSweepPollInterval = 30 * time.Second

type OnchainSweepRuntime struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func ProvideOnchainSweepRuntime(plannerStore onchain.TRONSweepStore, executionStore onchain.TRONSweepExecutionStore, cfg *config.Config) (*OnchainSweepRuntime, error) {
	if executionStore == nil || cfg == nil {
		return nil, fmt.Errorf("onchain sweep runtime dependencies are required")
	}
	tron := cfg.Onchain.TRON
	if !tron.Enabled {
		return &OnchainSweepRuntime{}, nil
	}
	client, err := onchain.NewJavaTronClient(onchain.JavaTronClientOptions{
		FullNodeURL: tron.FullNodeURL, SolidityNodeURL: tron.SolidityNodeURL,
		Timeout:          time.Duration(tron.RequestTimeoutSeconds) * time.Second,
		ResponseMaxBytes: tron.ResponseMaxBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("create TRON sweep node client: %w", err)
	}
	executorOptions := onchain.TRONSweepExecutorOptions{
		Network: onchain.Network(tron.Network), ContractAddress: tron.USDTContract,
		DestinationAddress: tron.SweepAddress, BatchSize: tron.SweepBatchSize,
		MaxConsecutiveFailures: tron.SweepMaxFailures,
		RetryBackoff:           time.Duration(tron.SweepRetrySeconds) * time.Second,
		ConfirmationPoll:       time.Duration(tron.SweepConfirmSeconds) * time.Second,
	}
	if !tron.SweeperEnabled {
		tracker, err := onchain.NewTRONSweepTracker(executionStore, client, executorOptions)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		runtime := &OnchainSweepRuntime{cancel: cancel, done: make(chan struct{})}
		go func() {
			defer close(runtime.done)
			runTRONSweepTracking(ctx, tracker, defaultTRONSweepPollInterval)
		}()
		return runtime, nil
	}
	if plannerStore == nil {
		return nil, fmt.Errorf("onchain sweep planner store is required when new sweeps are enabled")
	}
	planner, err := onchain.NewTRONSweepPlanner(plannerStore, client, onchain.TRONSweepPlannerOptions{
		Network: onchain.Network(tron.Network), ContractAddress: tron.USDTContract,
		DestinationAddress: tron.SweepAddress, MinimumAmountRaw: tron.SweepMinimumAmountRaw,
		RequiredEnergy: tron.SweepRequiredEnergy, RequiredBandwidth: tron.SweepRequiredBandwidth,
		MinimumTRXBalanceSun: tron.SweepMinimumTRXSun, BatchSize: tron.SweepBatchSize,
	})
	if err != nil {
		return nil, err
	}
	hotWalletMonitor, err := onchain.NewTRONHotWalletMonitor(
		client, onchain.Network(tron.Network), tron.USDTContract, tron.SweepAddress,
		tron.HotWalletWarningRaw, tron.HotWalletApprovalRaw,
	)
	if err != nil {
		return nil, err
	}
	signer, err := signerv1.NewClient(signerv1.ClientConfig{
		BaseURL: tron.SignerURL, ClientCertFile: tron.SignerClientCertFile,
		ClientKeyFile: tron.SignerClientKeyFile, ServerCAFile: tron.SignerServerCAFile,
		ServerIdentityURI: tron.SignerServerIdentityURI,
		Timeout:           time.Duration(tron.SignerTimeoutSeconds) * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create TRON sweep signer client: %w", err)
	}
	executor, err := onchain.NewTRONSweepExecutor(executionStore, client, signer, executorOptions)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &OnchainSweepRuntime{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(runtime.done)
		runTRONSweeps(ctx, planner, executor, hotWalletMonitor, defaultTRONSweepPollInterval)
	}()
	return runtime, nil
}

type tronSweepSolidificationTracker interface {
	TrackSolidificationOnce(context.Context) (onchain.TRONSweepExecutionResult, error)
}

func runTRONSweepTracking(ctx context.Context, tracker tronSweepSolidificationTracker, interval time.Duration) {
	for ctx.Err() == nil {
		if _, err := tracker.TrackSolidificationOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Error("TRON sweep solidification tracking failed", "error", err)
		}
		if !waitTRONSweepRuntime(ctx, interval) {
			return
		}
	}
}

func runTRONSweeps(ctx context.Context, planner *onchain.TRONSweepPlanner, executor *onchain.TRONSweepExecutor, hotWalletMonitor *onchain.TRONHotWalletMonitor, interval time.Duration) {
	for ctx.Err() == nil {
		hotWallet, monitorErr := hotWalletMonitor.Check(ctx)
		signingEnabled := monitorErr == nil && !hotWallet.AutomaticSweepsPaused
		if monitorErr != nil && ctx.Err() == nil {
			slog.Error("TRON automatic sweeps paused because hot wallet balance is unavailable", "error", monitorErr)
		} else if hotWallet.ColdTransferApprovalRequired {
			slog.Error("TRON automatic sweeps paused pending independent cold-wallet transfer approval", "balance_raw", hotWallet.BalanceRaw, "threshold_raw", hotWallet.ColdApprovalThresholdRaw)
		} else {
			if hotWallet.Warning {
				slog.Warn("TRON hot wallet balance warning", "balance_raw", hotWallet.BalanceRaw, "threshold_raw", hotWallet.WarningThresholdRaw)
			}
			if _, err := planner.PlanBatch(ctx); err != nil && ctx.Err() == nil {
				slog.Error("TRON sweep planning failed", "error", err)
			}
		}
		var executionErr error
		if signingEnabled {
			_, executionErr = executor.RunOnce(ctx)
		} else {
			_, executionErr = executor.TrackSolidificationOnce(ctx)
		}
		if executionErr != nil && ctx.Err() == nil {
			slog.Error("TRON sweep execution failed", "error", executionErr)
		}
		if !waitTRONSweepRuntime(ctx, interval) {
			return
		}
	}
}

func waitTRONSweepRuntime(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (r *OnchainSweepRuntime) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.once.Do(func() {
		r.cancel()
		<-r.done
	})
}
