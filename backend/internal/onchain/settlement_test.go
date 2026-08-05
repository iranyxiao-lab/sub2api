package onchain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type settlementStoreStub struct {
	ids         []int64
	prepared    map[int64]SettlementPreparation
	prepareErrs map[int64]error
	failures    []SettlementFailure
}

func (s *settlementStoreStub) RecordSettlementFailure(_ context.Context, failure SettlementFailure) error {
	s.failures = append(s.failures, failure)
	return nil
}

func (s *settlementStoreStub) ListSettlementCandidates(context.Context, Network, int) ([]int64, error) {
	return s.ids, nil
}

func (s *settlementStoreStub) PrepareIntentSettlement(_ context.Context, intentID int64) (SettlementPreparation, error) {
	return s.prepared[intentID], s.prepareErrs[intentID]
}

type settlementConfirmerStub struct {
	calls []int64
	errs  map[int64]error
}

func (s *settlementConfirmerStub) ConfirmOnchainDeposit(_ context.Context, intentID int64) error {
	s.calls = append(s.calls, intentID)
	return s.errs[intentID]
}

func TestSettlementWorkerDoesNotConfirmUnderpayment(t *testing.T) {
	store := &settlementStoreStub{
		ids: []int64{7},
		prepared: map[int64]SettlementPreparation{7: {
			IntentID: 7, Status: IntentPartiallyPaid, ExpectedAmountRaw: "10000000",
			ReceivedAmountRaw: "4000000", PendingAmountRaw: "6000000",
		}},
		prepareErrs: map[int64]error{},
	}
	confirmer := &settlementConfirmerStub{errs: map[int64]error{}}
	worker, err := NewSettlementWorker(store, confirmer, SettlementWorkerOptions{
		Network: NetworkTronMainnet, BatchSize: 10, PollInterval: time.Second,
	})
	require.NoError(t, err)

	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Underpaid)
	require.Zero(t, result.Ready)
	require.Empty(t, confirmer.calls)
}

func TestSettlementWorkerDoesNotConfirmReviewRequiredIntent(t *testing.T) {
	store := &settlementStoreStub{
		ids: []int64{8},
		prepared: map[int64]SettlementPreparation{8: {
			IntentID: 8, Status: IntentReviewRequired, ReviewRequired: true,
		}},
		prepareErrs: map[int64]error{},
	}
	confirmer := &settlementConfirmerStub{errs: map[int64]error{}}
	worker, err := NewSettlementWorker(store, confirmer, SettlementWorkerOptions{
		Network: NetworkTronMainnet, BatchSize: 10, PollInterval: time.Second,
	})
	require.NoError(t, err)

	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Review)
	require.Empty(t, confirmer.calls)
}

func TestSettlementWorkerContinuesAfterPerIntentFailure(t *testing.T) {
	store := &settlementStoreStub{
		ids: []int64{1, 2, 3},
		prepared: map[int64]SettlementPreparation{
			2: {IntentID: 2, Status: IntentSettlementDue, Ready: true},
			3: {IntentID: 3, Status: IntentSettlementDue, Ready: true},
		},
		prepareErrs: map[int64]error{1: errors.New("database unavailable")},
	}
	confirmer := &settlementConfirmerStub{errs: map[int64]error{2: errors.New("fulfillment unavailable")}}
	worker, err := NewSettlementWorker(store, confirmer, SettlementWorkerOptions{
		Network: NetworkTronMainnet, BatchSize: 10, PollInterval: time.Second,
	})
	require.NoError(t, err)

	result, err := worker.RunOnce(context.Background())
	require.Error(t, err)
	require.Equal(t, []int64{2, 3}, confirmer.calls)
	require.Equal(t, 2, result.Ready)
	require.Equal(t, 1, result.Confirmed)
	require.Equal(t, 2, result.Failed)
	require.Len(t, store.failures, 1)
	require.Equal(t, int64(2), store.failures[0].IntentID)
	require.True(t, store.failures[0].Retryable)
}

func TestSettlementRetryBackoffIsBounded(t *testing.T) {
	require.Equal(t, time.Second, settlementRetryBackoff(time.Second, 8*time.Second, 1))
	require.Equal(t, 4*time.Second, settlementRetryBackoff(time.Second, 8*time.Second, 3))
	require.Equal(t, 8*time.Second, settlementRetryBackoff(time.Second, 8*time.Second, 20))
}
