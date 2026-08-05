package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/stretchr/testify/require"
)

type scannerRuntimeTestStore struct {
	cursor         onchain.TRONScanCursor
	loadErr        error
	initialization *onchain.TRONScanCursorInitialization
}

func (s *scannerRuntimeTestStore) LoadTRONScanCursor(context.Context, onchain.Network) (onchain.TRONScanCursor, error) {
	return s.cursor, s.loadErr
}

func (s *scannerRuntimeTestStore) InitializeTRONScanCursor(_ context.Context, input onchain.TRONScanCursorInitialization) error {
	s.initialization = &input
	s.loadErr = nil
	return nil
}

func (*scannerRuntimeTestStore) FindTRONPaymentIntentByAddress(context.Context, onchain.Network, string) (onchain.TRONPaymentIntentReference, bool, error) {
	return onchain.TRONPaymentIntentReference{}, false, nil
}

func (*scannerRuntimeTestStore) CommitTRONScanBlock(context.Context, onchain.TRONScanBlockCommit) error {
	return nil
}

func (*scannerRuntimeTestStore) ReconcileTRONScanBlock(context.Context, onchain.TRONScanBlockReconcile) error {
	return nil
}

func (*scannerRuntimeTestStore) MarkTRONScanHashConflict(context.Context, onchain.Network, string, time.Time, error) error {
	return nil
}

type scannerRuntimeTestSource struct {
	height int64
	block  onchain.TRONSolidifiedBlock
	calls  int
}

func (s *scannerRuntimeTestSource) LatestSolidifiedHeight(context.Context) (int64, error) {
	s.calls++
	return s.height, nil
}

func (s *scannerRuntimeTestSource) SolidifiedBlockByHeight(context.Context, int64) (onchain.TRONSolidifiedBlock, error) {
	s.calls++
	return s.block, nil
}

func (*scannerRuntimeTestSource) SolidifiedTransactionReceiptsByBlockHeight(context.Context, int64) ([]onchain.TRONTransactionReceipt, error) {
	return nil, nil
}

func TestTRONScannerLeaseOwnerIsStableAndBounded(t *testing.T) {
	owner := tronScannerLeaseOwner()
	require.NotEmpty(t, owner)
	require.LessOrEqual(t, len(owner), 128)
	require.Equal(t, owner, tronScannerLeaseOwner())
}

func TestDisabledOnchainScannerRuntimeStopsSafely(t *testing.T) {
	runtime := &OnchainScannerRuntime{}
	runtime.Stop()
	runtime.Stop()
}

func TestInitializeTRONScanCursorUsesCurrentSolidifiedHeadOnlyWhenMissing(t *testing.T) {
	cfg := config.SelfHostedTRONConfig{
		Network: string(onchain.NetworkTronMainnet), RequestTimeoutSeconds: 1,
	}
	source := &scannerRuntimeTestSource{
		height: 42,
		block: onchain.TRONSolidifiedBlock{
			Height: 42, Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	store := &scannerRuntimeTestStore{loadErr: onchain.ErrTRONScanCursorNotFound}
	require.NoError(t, initializeTRONScanCursor(store, source, cfg))
	require.NotNil(t, store.initialization)
	require.Equal(t, int64(42), store.initialization.FinalizedHeight)
	require.Equal(t, source.block.Hash, store.initialization.FinalizedHash)
	require.Equal(t, 2, source.calls)

	existing := &scannerRuntimeTestStore{cursor: onchain.TRONScanCursor{FinalizedHeight: 7, FinalizedHash: "saved"}}
	source.calls = 0
	require.NoError(t, initializeTRONScanCursor(existing, source, cfg))
	require.Nil(t, existing.initialization)
	require.Zero(t, source.calls, "an existing durable cursor must never be rebased from the node head")
}

func TestInitializeTRONScanCursorPreservesLoadErrors(t *testing.T) {
	store := &scannerRuntimeTestStore{loadErr: errors.New("database unavailable")}
	err := initializeTRONScanCursor(store, &scannerRuntimeTestSource{}, config.SelfHostedTRONConfig{
		Network: string(onchain.NetworkTronMainnet), RequestTimeoutSeconds: 1,
	})
	require.ErrorContains(t, err, "database unavailable")
}
