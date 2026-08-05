package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
)

func validateEthereumOnchainStartup(ctx context.Context, cfg config.SelfHostedEthereumConfig) error {
	if !cfg.Enabled {
		return nil
	}
	timeout := time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	startupCtx, cancel := context.WithTimeout(ctx, 12*timeout)
	defer cancel()
	clientOptions := func(endpoint string) onchain.EthereumRPCClientOptions {
		return onchain.EthereumRPCClientOptions{
			Endpoint: endpoint, Timeout: timeout, ResponseMaxBytes: cfg.ResponseMaxBytes,
			BatchLimit: cfg.RPCBatchLimit, MaxRetries: 2, RetryBackoff: 100 * time.Millisecond,
		}
	}
	primary, err := onchain.NewEthereumRPCClient(startupCtx, clientOptions(cfg.PrimaryRPCURL))
	if err != nil {
		return fmt.Errorf("create primary client: %w", err)
	}
	defer primary.Close()
	backup, err := onchain.NewEthereumRPCClient(startupCtx, clientOptions(cfg.BackupRPCURL))
	if err != nil {
		return fmt.Errorf("create backup client: %w", err)
	}
	defer backup.Close()
	_, err = onchain.ValidateEthereumStartup(startupCtx, primary, backup, onchain.EthereumStartupOptions{
		Network: onchain.Network(cfg.Network), ChainID: cfg.ChainID,
		USDTContract: cfg.USDTContract, USDTDecimals: cfg.USDTDecimals,
		MaxFinalizedLag: cfg.MaxFinalizedLag,
	})
	if err != nil {
		return fmt.Errorf("validate primary and backup nodes: %w", err)
	}
	return nil
}
