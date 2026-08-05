package onchain

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type countingTRONBlockSource struct {
	head  int64
	err   error
	calls atomic.Int32
}

func (s *countingTRONBlockSource) LatestSolidifiedHeight(context.Context) (int64, error) {
	s.calls.Add(1)
	return s.head, s.err
}

func (s *countingTRONBlockSource) SolidifiedBlockByHeight(_ context.Context, height int64) (TRONSolidifiedBlock, error) {
	s.calls.Add(1)
	if s.err != nil {
		return TRONSolidifiedBlock{}, s.err
	}
	return tronScannerEmptyBlock(height), nil
}

func (s *countingTRONBlockSource) SolidifiedTransactionReceiptsByBlockHeight(context.Context, int64) ([]TRONTransactionReceipt, error) {
	s.calls.Add(1)
	return []TRONTransactionReceipt{}, s.err
}

func TestTRONFailoverBlockSourceSwitchesAndSticksToHealthyNode(t *testing.T) {
	primary := &countingTRONBlockSource{err: errors.New("primary unavailable")}
	fallback := &countingTRONBlockSource{head: 42}
	source, err := NewTRONFailoverBlockSource(primary, fallback)
	require.NoError(t, err)

	height, err := source.LatestSolidifiedHeight(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(42), height)
	require.Equal(t, 1, source.ActiveIndex())
	_, err = source.SolidifiedBlockByHeight(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, int32(1), primary.calls.Load(), "healthy fallback remains active for the next call")
	require.Equal(t, int32(2), fallback.calls.Load())
}

func TestTRONFailoverBlockSourceDoesNotSwitchOnRequestError(t *testing.T) {
	primary := &countingTRONBlockSource{err: &JavaTronError{
		Kind: JavaTronErrorRequestEncode, Node: JavaTronSolidityNode, Endpoint: "test", Attempt: 1,
	}}
	fallback := &countingTRONBlockSource{head: 42}
	source, err := NewTRONFailoverBlockSource(primary, fallback)
	require.NoError(t, err)

	_, err = source.LatestSolidifiedHeight(context.Background())
	require.Error(t, err)
	require.Zero(t, fallback.calls.Load())
	require.Zero(t, source.ActiveIndex())
}

type fakeTRONBatchScanner struct {
	scan func(context.Context, string) (TRONScanBatchResult, error)
}

func (s *fakeTRONBatchScanner) ScanBatch(ctx context.Context, owner string) (TRONScanBatchResult, error) {
	return s.scan(ctx, owner)
}

type fakeTRONWorkerLeaseStore struct {
	acquires    atomic.Int32
	renews      atomic.Int32
	releases    atomic.Int32
	renewResult bool
	onRenew     func()
}

func (s *fakeTRONWorkerLeaseStore) AcquireCursorLease(context.Context, string, string, time.Time, time.Time) (bool, error) {
	s.acquires.Add(1)
	return true, nil
}

func (s *fakeTRONWorkerLeaseStore) RenewCursorLease(context.Context, string, string, time.Time, time.Time) (bool, error) {
	s.renews.Add(1)
	if s.onRenew != nil {
		s.onRenew()
	}
	return s.renewResult, nil
}

func (s *fakeTRONWorkerLeaseStore) ReleaseCursorLease(context.Context, string, string) (bool, error) {
	s.releases.Add(1)
	return true, nil
}

func TestTRONScanWorkerRetriesScanErrorsAndReleasesLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var scans atomic.Int32
	scanner := &fakeTRONBatchScanner{scan: func(_ context.Context, owner string) (TRONScanBatchResult, error) {
		require.Equal(t, "scanner-a", owner)
		switch scans.Add(1) {
		case 1:
			return TRONScanBatchResult{}, errors.New("temporary node error")
		case 2:
			return TRONScanBatchResult{BlocksScanned: 1}, nil
		default:
			cancel()
			return TRONScanBatchResult{}, nil
		}
	}}
	leases := &fakeTRONWorkerLeaseStore{renewResult: true}
	worker := newFastTRONScanWorker(t, scanner, leases)

	require.NoError(t, worker.Run(ctx))
	require.GreaterOrEqual(t, scans.Load(), int32(3))
	require.Equal(t, int32(1), leases.acquires.Load())
	require.Equal(t, int32(1), leases.releases.Load())
}

func TestTRONScanWorkerStopsOnHashConflict(t *testing.T) {
	scanner := &fakeTRONBatchScanner{scan: func(context.Context, string) (TRONScanBatchResult, error) {
		return TRONScanBatchResult{}, ErrTRONScanHashConflict
	}}
	leases := &fakeTRONWorkerLeaseStore{renewResult: true}
	worker := newFastTRONScanWorker(t, scanner, leases)

	err := worker.Run(context.Background())
	require.ErrorIs(t, err, ErrTRONScanHashConflict)
	require.Equal(t, int32(1), leases.releases.Load())
}

func TestTRONScanWorkerCancelsWorkWhenRenewalLosesLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	scanner := &fakeTRONBatchScanner{scan: func(ctx context.Context, _ string) (TRONScanBatchResult, error) {
		<-ctx.Done()
		return TRONScanBatchResult{}, ctx.Err()
	}}
	leases := &fakeTRONWorkerLeaseStore{renewResult: false, onRenew: cancel}
	worker := newFastTRONScanWorker(t, scanner, leases)

	require.NoError(t, worker.Run(ctx))
	require.GreaterOrEqual(t, leases.renews.Load(), int32(1))
	require.Equal(t, int32(1), leases.releases.Load())
}

func newFastTRONScanWorker(t *testing.T, scanner TRONBatchScanner, leases TRONScanLeaseStore) *TRONScanWorker {
	t.Helper()
	worker, err := NewTRONScanWorker(scanner, leases, TRONScanWorkerOptions{
		Network: NetworkTronMainnet, Owner: "scanner-a",
		LeaseDuration: 50 * time.Millisecond, RenewInterval: 10 * time.Millisecond,
		PollInterval: time.Millisecond, MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond,
	})
	require.NoError(t, err)
	return worker
}
