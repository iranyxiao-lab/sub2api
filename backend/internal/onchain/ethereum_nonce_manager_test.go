package onchain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeEthereumPendingNonceSource struct {
	values []uint64
	index  int
}

func (s *fakeEthereumPendingNonceSource) PendingNonce(context.Context, string) (uint64, error) {
	value := s.values[s.index]
	s.index++
	return value, nil
}

type fakeEthereumNonceStore struct {
	reserveInput EthereumNonceReserveInput
	reservation  EthereumNonceReservation
	committed    uint64
	conflicted   uint64
}

func (s *fakeEthereumNonceStore) ReserveEthereumNonce(_ context.Context, input EthereumNonceReserveInput) (EthereumNonceReservation, bool, error) {
	s.reserveInput = input
	return s.reservation, true, nil
}

func (s *fakeEthereumNonceStore) CommitEthereumNonce(_ context.Context, _ EthereumNonceReservation, observedPending uint64, _ time.Time) error {
	s.committed = observedPending
	return nil
}

func (s *fakeEthereumNonceStore) MarkEthereumNonceConflict(_ context.Context, _ EthereumNonceReservation, observedPending uint64, _, _ string, _ time.Time) error {
	s.conflicted = observedPending
	return nil
}

func TestEthereumNonceManagerReconcilesBeforeReserveAndAfterBroadcast(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := &fakeEthereumNonceStore{reservation: EthereumNonceReservation{
		StateID: 3, ChainID: 1, SenderAddress: "0x1111111111111111111111111111111111111111",
		Nonce: 7, Owner: "worker-1", Version: 1,
	}}
	source := &fakeEthereumPendingNonceSource{values: []uint64{7, 8}}
	manager, err := NewEthereumNonceManager(store, source, EthereumNonceManagerOptions{
		ChainID: 1, SenderAddress: store.reservation.SenderAddress, Owner: "worker-1", LeaseDuration: 30 * time.Second,
	})
	require.NoError(t, err)
	manager.now = func() time.Time { return now }

	reservation, err := manager.Reserve(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(7), reservation.Nonce)
	require.Equal(t, uint64(7), store.reserveInput.ObservedPending)
	require.Equal(t, now.Add(30*time.Second), store.reserveInput.LeaseUntil)
	require.NoError(t, manager.Commit(context.Background(), reservation))
	require.Equal(t, uint64(8), store.committed)
	require.Zero(t, store.conflicted)
}

func TestEthereumNonceManagerPersistsUnexpectedPendingNonceAsConflict(t *testing.T) {
	store := &fakeEthereumNonceStore{reservation: EthereumNonceReservation{
		StateID: 3, ChainID: 1, SenderAddress: "0x1111111111111111111111111111111111111111",
		Nonce: 7, Owner: "worker-1", Version: 1,
	}}
	source := &fakeEthereumPendingNonceSource{values: []uint64{7, 7}}
	manager, err := NewEthereumNonceManager(store, source, EthereumNonceManagerOptions{
		ChainID: 1, SenderAddress: store.reservation.SenderAddress, Owner: "worker-1", LeaseDuration: 30 * time.Second,
	})
	require.NoError(t, err)

	reservation, err := manager.Reserve(context.Background())
	require.NoError(t, err)
	err = manager.Commit(context.Background(), reservation)
	require.ErrorIs(t, err, ErrEthereumNonceConflict)
	require.Equal(t, uint64(7), store.conflicted)
	require.Zero(t, store.committed)
}
