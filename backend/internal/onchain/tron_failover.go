package onchain

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type TRONFailoverBlockSource struct {
	mu      sync.RWMutex
	sources []TRONBlockSource
	active  int
}

func NewTRONFailoverBlockSource(primary TRONBlockSource, fallbacks ...TRONBlockSource) (*TRONFailoverBlockSource, error) {
	if primary == nil {
		return nil, fmt.Errorf("primary TRON block source is required")
	}
	sources := make([]TRONBlockSource, 0, len(fallbacks)+1)
	sources = append(sources, primary)
	for index, fallback := range fallbacks {
		if fallback == nil {
			return nil, fmt.Errorf("TRON fallback block source %d is nil", index)
		}
		sources = append(sources, fallback)
	}
	return &TRONFailoverBlockSource{sources: sources}, nil
}

func (s *TRONFailoverBlockSource) LatestSolidifiedHeight(ctx context.Context) (int64, error) {
	var height int64
	err := s.trySources(ctx, func(source TRONBlockSource) error {
		value, err := source.LatestSolidifiedHeight(ctx)
		if err == nil {
			height = value
		}
		return err
	})
	return height, err
}

func (s *TRONFailoverBlockSource) SolidifiedBlockByHeight(ctx context.Context, height int64) (TRONSolidifiedBlock, error) {
	var block TRONSolidifiedBlock
	err := s.trySources(ctx, func(source TRONBlockSource) error {
		value, err := source.SolidifiedBlockByHeight(ctx, height)
		if err == nil {
			block = value
		}
		return err
	})
	return block, err
}

func (s *TRONFailoverBlockSource) SolidifiedTransactionReceiptsByBlockHeight(ctx context.Context, height int64) ([]TRONTransactionReceipt, error) {
	var receipts []TRONTransactionReceipt
	err := s.trySources(ctx, func(source TRONBlockSource) error {
		value, err := source.SolidifiedTransactionReceiptsByBlockHeight(ctx, height)
		if err == nil {
			receipts = value
		}
		return err
	})
	return receipts, err
}

func (s *TRONFailoverBlockSource) ActiveIndex() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

func (s *TRONFailoverBlockSource) trySources(ctx context.Context, call func(TRONBlockSource) error) error {
	s.mu.RLock()
	start := s.active
	sources := append([]TRONBlockSource(nil), s.sources...)
	s.mu.RUnlock()

	failures := make([]error, 0, len(sources))
	for offset := range len(sources) {
		index := (start + offset) % len(sources)
		err := call(sources[index])
		if err == nil {
			s.mu.Lock()
			s.active = index
			s.mu.Unlock()
			return nil
		}
		failures = append(failures, fmt.Errorf("source %d: %w", index, err))
		if ctx.Err() != nil || !shouldFailoverTRONSource(err) {
			break
		}
	}
	return fmt.Errorf("all TRON block sources failed: %w", errors.Join(failures...))
}

func shouldFailoverTRONSource(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var javaTronErr *JavaTronError
	if !errors.As(err, &javaTronErr) {
		return true
	}
	return javaTronErr.Kind != JavaTronErrorRequestEncode
}
