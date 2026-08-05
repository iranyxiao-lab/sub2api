package onchain

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type EthereumFailoverScanSource struct {
	primary EthereumScanSource
	backup  EthereumScanSource
}

func NewEthereumFailoverScanSource(primary, backup EthereumScanSource) (*EthereumFailoverScanSource, error) {
	if primary == nil || backup == nil {
		return nil, fmt.Errorf("primary and backup Ethereum scan sources are required")
	}
	return &EthereumFailoverScanSource{primary: primary, backup: backup}, nil
}

func (s *EthereumFailoverScanSource) FinalizedBlock(ctx context.Context) (EthereumBlockRef, error) {
	return ethereumScanReadWithFailover(ctx, s.primary, s.backup, func(ctx context.Context, source EthereumScanSource) (EthereumBlockRef, error) {
		return source.FinalizedBlock(ctx)
	})
}

func (s *EthereumFailoverScanSource) CommonFinalizedBlock(ctx context.Context) (EthereumBlockRef, error) {
	primaryFinalized, err := s.primary.FinalizedBlock(ctx)
	if err != nil {
		return EthereumBlockRef{}, fmt.Errorf("read primary Ethereum finalized head: %w", err)
	}
	backupFinalized, err := s.backup.FinalizedBlock(ctx)
	if err != nil {
		return EthereumBlockRef{}, fmt.Errorf("read backup Ethereum finalized head: %w", err)
	}
	commonHeight := min(primaryFinalized.Number, backupFinalized.Number)
	primaryBlock := primaryFinalized
	if primaryBlock.Number != commonHeight {
		primaryBlock, err = s.primary.BlockByNumber(ctx, commonHeight)
		if err != nil {
			return EthereumBlockRef{}, fmt.Errorf("read primary common finalized block %d: %w", commonHeight, err)
		}
	}
	backupBlock := backupFinalized
	if backupBlock.Number != commonHeight {
		backupBlock, err = s.backup.BlockByNumber(ctx, commonHeight)
		if err != nil {
			return EthereumBlockRef{}, fmt.Errorf("read backup common finalized block %d: %w", commonHeight, err)
		}
	}
	if !strings.EqualFold(primaryBlock.Hash, backupBlock.Hash) {
		return EthereumBlockRef{}, fmt.Errorf("%w at common height %d", ErrEthereumFinalizedDivergence, commonHeight)
	}
	return primaryBlock, nil
}

func (s *EthereumFailoverScanSource) BlockByNumber(ctx context.Context, number uint64) (EthereumBlockRef, error) {
	return ethereumScanReadWithFailover(ctx, s.primary, s.backup, func(ctx context.Context, source EthereumScanSource) (EthereumBlockRef, error) {
		return source.BlockByNumber(ctx, number)
	})
}

func (s *EthereumFailoverScanSource) Logs(ctx context.Context, fromBlock, toBlock uint64, contract string, topics [][]common.Hash) ([]types.Log, error) {
	return ethereumScanReadWithFailover(ctx, s.primary, s.backup, func(ctx context.Context, source EthereumScanSource) ([]types.Log, error) {
		return source.Logs(ctx, fromBlock, toBlock, contract, topics)
	})
}

func (s *EthereumFailoverScanSource) TransactionReceipt(ctx context.Context, transactionHash string) (EthereumTransactionReceipt, error) {
	return ethereumScanReadWithFailover(ctx, s.primary, s.backup, func(ctx context.Context, source EthereumScanSource) (EthereumTransactionReceipt, error) {
		return source.TransactionReceipt(ctx, transactionHash)
	})
}

func ethereumScanReadWithFailover[T any](ctx context.Context, primary, backup EthereumScanSource, read func(context.Context, EthereumScanSource) (T, error)) (T, error) {
	value, primaryErr := read(ctx, primary)
	if primaryErr == nil {
		return value, nil
	}
	if !IsEthereumRPCRetryable(primaryErr) {
		var zero T
		return zero, primaryErr
	}
	value, backupErr := read(ctx, backup)
	if backupErr == nil {
		return value, nil
	}
	var zero T
	return zero, fmt.Errorf("primary and backup Ethereum scan reads failed: %w", errors.Join(primaryErr, backupErr))
}
