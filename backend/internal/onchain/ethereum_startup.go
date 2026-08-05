package onchain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrEthereumFinalizedDivergence = errors.New("Ethereum finalized hash divergence")

type EthereumBlockRef struct {
	Number     uint64
	Hash       string
	ParentHash string
	Timestamp  time.Time
}

type EthereumStartupSource interface {
	ChainID(context.Context) (uint64, error)
	Syncing(context.Context) (bool, error)
	LatestBlockNumber(context.Context) (uint64, error)
	FinalizedBlock(context.Context) (EthereumBlockRef, error)
	BlockByNumber(context.Context, uint64) (EthereumBlockRef, error)
	ContractCode(context.Context, string, EthereumBlockRef) ([]byte, error)
	ERC20Decimals(context.Context, string, EthereumBlockRef) (uint8, error)
}

type EthereumStartupOptions struct {
	Network         Network
	ChainID         uint64
	USDTContract    string
	USDTDecimals    uint8
	MaxFinalizedLag uint64
}

type EthereumEndpointHealth struct {
	Latest    uint64
	Finalized EthereumBlockRef
}

type EthereumStartupReport struct {
	Primary         EthereumEndpointHealth
	Backup          EthereumEndpointHealth
	CommonFinalized EthereumBlockRef
}

// ValidateEthereumStartup verifies both independently operated execution
// endpoints without treating latest or a fixed confirmation count as finality.
func ValidateEthereumStartup(ctx context.Context, primary, backup EthereumStartupSource, options EthereumStartupOptions) (EthereumStartupReport, error) {
	if primary == nil || backup == nil {
		return EthereumStartupReport{}, fmt.Errorf("primary and backup Ethereum startup sources are required")
	}
	if err := ValidateTokenIdentity(options.Network, options.ChainID, options.USDTContract, options.USDTDecimals); err != nil {
		return EthereumStartupReport{}, fmt.Errorf("validate configured Ethereum token identity: %w", err)
	}
	primaryHealth, err := validateEthereumEndpointStartup(ctx, "primary", primary, options)
	if err != nil {
		return EthereumStartupReport{}, err
	}
	backupHealth, err := validateEthereumEndpointStartup(ctx, "backup", backup, options)
	if err != nil {
		return EthereumStartupReport{}, err
	}
	commonHeight := min(primaryHealth.Finalized.Number, backupHealth.Finalized.Number)
	primaryCommon, err := ethereumBlockAtHeight(ctx, "primary", primary, primaryHealth.Finalized, commonHeight)
	if err != nil {
		return EthereumStartupReport{}, err
	}
	backupCommon, err := ethereumBlockAtHeight(ctx, "backup", backup, backupHealth.Finalized, commonHeight)
	if err != nil {
		return EthereumStartupReport{}, err
	}
	if !strings.EqualFold(primaryCommon.Hash, backupCommon.Hash) {
		return EthereumStartupReport{}, fmt.Errorf("%w at common height %d", ErrEthereumFinalizedDivergence, commonHeight)
	}
	return EthereumStartupReport{
		Primary: primaryHealth, Backup: backupHealth,
		CommonFinalized: primaryCommon,
	}, nil
}

func ethereumBlockAtHeight(ctx context.Context, name string, source EthereumStartupSource, finalized EthereumBlockRef, height uint64) (EthereumBlockRef, error) {
	if finalized.Number == height {
		return finalized, nil
	}
	block, err := source.BlockByNumber(ctx, height)
	if err != nil {
		return EthereumBlockRef{}, fmt.Errorf("%s Ethereum endpoint common finalized block %d: %w", name, height, err)
	}
	if block.Number != height || block.Hash == "" {
		return EthereumBlockRef{}, fmt.Errorf("%s Ethereum endpoint returned an invalid block for common finalized height %d", name, height)
	}
	return block, nil
}

func validateEthereumEndpointStartup(ctx context.Context, name string, source EthereumStartupSource, options EthereumStartupOptions) (EthereumEndpointHealth, error) {
	chainID, err := source.ChainID(ctx)
	if err != nil {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint chain ID: %w", name, err)
	}
	if chainID != options.ChainID {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint chain ID is %d, want %d", name, chainID, options.ChainID)
	}
	syncing, err := source.Syncing(ctx)
	if err != nil {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint sync status: %w", name, err)
	}
	if syncing {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint is still syncing", name)
	}
	latest, err := source.LatestBlockNumber(ctx)
	if err != nil {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint latest block: %w", name, err)
	}
	finalized, err := source.FinalizedBlock(ctx)
	if err != nil {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint finalized block is unavailable: %w", name, err)
	}
	if finalized.Hash == "" || finalized.Number > latest {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint returned an invalid finalized block", name)
	}
	if latest-finalized.Number > options.MaxFinalizedLag {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint finalized lag is %d blocks, maximum is %d", name, latest-finalized.Number, options.MaxFinalizedLag)
	}
	code, err := source.ContractCode(ctx, options.USDTContract, finalized)
	if err != nil {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint USDT contract code: %w", name, err)
	}
	if len(code) == 0 {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint USDT contract has no code", name)
	}
	decimals, err := source.ERC20Decimals(ctx, options.USDTContract, finalized)
	if err != nil {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint USDT decimals: %w", name, err)
	}
	if decimals != options.USDTDecimals {
		return EthereumEndpointHealth{}, fmt.Errorf("%s Ethereum endpoint USDT decimals are %d, want %d", name, decimals, options.USDTDecimals)
	}
	return EthereumEndpointHealth{Latest: latest, Finalized: finalized}, nil
}
