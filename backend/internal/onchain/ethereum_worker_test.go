package onchain

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

type failingEthereumScanSource struct {
	err   error
	calls atomic.Int32
}

func (s *failingEthereumScanSource) FinalizedBlock(context.Context) (EthereumBlockRef, error) {
	s.calls.Add(1)
	return EthereumBlockRef{}, s.err
}

func (s *failingEthereumScanSource) BlockByNumber(context.Context, uint64) (EthereumBlockRef, error) {
	s.calls.Add(1)
	return EthereumBlockRef{}, s.err
}

func (s *failingEthereumScanSource) Logs(context.Context, uint64, uint64, string, [][]common.Hash) ([]types.Log, error) {
	s.calls.Add(1)
	return nil, s.err
}

func (s *failingEthereumScanSource) TransactionReceipt(context.Context, string) (EthereumTransactionReceipt, error) {
	s.calls.Add(1)
	return EthereumTransactionReceipt{}, s.err
}

func TestEthereumFailoverScanSourceUsesBackupOnlyForTransientErrors(t *testing.T) {
	transient := &EthereumRPCError{Kind: EthereumRPCErrorTransport, Method: "eth_getBlockByNumber", Retryable: true, Cause: errors.New("connection reset")}
	primary := &failingEthereumScanSource{err: transient}
	backup := &fakeEthereumScanSource{finalized: EthereumBlockRef{Number: 42, Hash: common.HexToHash("0x42").Hex()}}
	source, err := NewEthereumFailoverScanSource(primary, backup)
	require.NoError(t, err)

	finalized, err := source.FinalizedBlock(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(42), finalized.Number)
	require.Equal(t, int32(1), primary.calls.Load())

	permanent := &failingEthereumScanSource{err: &EthereumRPCError{
		Kind: EthereumRPCErrorInvalidRequest, Method: "eth_getLogs", Cause: errors.New("invalid filter"),
	}}
	source, err = NewEthereumFailoverScanSource(permanent, backup)
	require.NoError(t, err)
	_, err = source.FinalizedBlock(context.Background())
	require.Error(t, err)
	require.Equal(t, int32(1), permanent.calls.Load())
}

func TestEthereumFailoverScanSourceRejectsCommonFinalizedDivergence(t *testing.T) {
	primaryBlock := EthereumBlockRef{Number: 42, Hash: common.HexToHash("0x42").Hex()}
	backupBlock := EthereumBlockRef{Number: 42, Hash: common.HexToHash("0x99").Hex()}
	primary := &fakeEthereumScanSource{finalized: primaryBlock, blocks: map[uint64]EthereumBlockRef{42: primaryBlock}}
	backup := &fakeEthereumScanSource{finalized: backupBlock, blocks: map[uint64]EthereumBlockRef{42: backupBlock}}
	source, err := NewEthereumFailoverScanSource(primary, backup)
	require.NoError(t, err)

	_, err = source.CommonFinalizedBlock(context.Background())
	require.ErrorIs(t, err, ErrEthereumFinalizedDivergence)
}

func TestEthereumScannerPersistsPrimaryBackupDivergence(t *testing.T) {
	now := time.Now().UTC()
	primaryBlock := EthereumBlockRef{Number: 42, Hash: common.HexToHash("0x42").Hex(), Timestamp: now}
	backupBlock := EthereumBlockRef{Number: 42, Hash: common.HexToHash("0x99").Hex(), Timestamp: now}
	primary := &fakeEthereumScanSource{finalized: primaryBlock, blocks: map[uint64]EthereumBlockRef{42: primaryBlock}}
	backup := &fakeEthereumScanSource{finalized: backupBlock, blocks: map[uint64]EthereumBlockRef{42: backupBlock}}
	source, err := NewEthereumFailoverScanSource(primary, backup)
	require.NoError(t, err)
	store := &fakeEthereumScanStore{cursor: EthereumScanCursor{
		LeaseOwner: "scanner-a", LeaseUntil: now.Add(time.Minute), Health: CursorHealthy,
	}}
	scanner, err := NewEthereumScanner(source, store, EthereumScannerOptions{
		Network: NetworkEthereumMainnet, ContractAddress: EthereumMainnetUSDTContract,
		BatchSize: 10, MaxLogRange: 10, SafetyWindow: 2,
	})
	require.NoError(t, err)
	scanner.now = func() time.Time { return now }

	_, err = scanner.ScanBatch(context.Background(), "scanner-a")
	require.ErrorIs(t, err, ErrEthereumScanHashConflict)
	require.ErrorIs(t, store.conflict, ErrEthereumFinalizedDivergence)
	require.Equal(t, CursorHashConflict, store.cursor.Health)
}

type fakeEthereumBatchScanner struct {
	scan func(context.Context, string) (EthereumScanBatchResult, error)
}

func (s *fakeEthereumBatchScanner) ScanBatch(ctx context.Context, owner string) (EthereumScanBatchResult, error) {
	return s.scan(ctx, owner)
}

func TestEthereumScanWorkerOwnsRenewsAndReleasesSingleNetworkLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var scans atomic.Int32
	scanner := &fakeEthereumBatchScanner{scan: func(_ context.Context, owner string) (EthereumScanBatchResult, error) {
		require.Equal(t, "ethereum-scanner-a", owner)
		if scans.Add(1) >= 2 {
			cancel()
		}
		return EthereumScanBatchResult{}, nil
	}}
	leases := &fakeTRONWorkerLeaseStore{renewResult: true}
	worker, err := NewEthereumScanWorker(scanner, leases, EthereumScanWorkerOptions{
		Network: NetworkEthereumMainnet, Owner: "ethereum-scanner-a",
		LeaseDuration: 50 * time.Millisecond, RenewInterval: 10 * time.Millisecond,
		PollInterval: time.Millisecond, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	require.NoError(t, err)
	require.NoError(t, worker.Run(ctx))
	require.Equal(t, int32(1), leases.acquires.Load())
	require.Equal(t, int32(1), leases.releases.Load())
}
