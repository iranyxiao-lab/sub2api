package onchain

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeTRONSweepStore struct {
	candidates []TRONSweepCandidate
	tasks      map[string]TRONSweepTask
	inputs     []TRONSweepCreate
}

func (s *fakeTRONSweepStore) ListTRONSweepCandidates(context.Context, Network, int) ([]TRONSweepCandidate, error) {
	return append([]TRONSweepCandidate(nil), s.candidates...), nil
}

func (s *fakeTRONSweepStore) EnsureTRONSweep(_ context.Context, input TRONSweepCreate) (TRONSweepTask, bool, error) {
	if s.tasks == nil {
		s.tasks = make(map[string]TRONSweepTask)
	}
	if existing, ok := s.tasks[input.IdempotencyKey]; ok {
		return existing, false, nil
	}
	task := TRONSweepTask{ID: int64(len(s.tasks) + 1), IdempotencyKey: input.IdempotencyKey, AmountRaw: input.AmountRaw, Status: input.Status}
	s.tasks[input.IdempotencyKey] = task
	s.inputs = append(s.inputs, input)
	return task, true, nil
}

func (s *fakeTRONSweepStore) MarkTRONSweepResourcesReady(_ context.Context, taskID int64, version int) error {
	for key, task := range s.tasks {
		if task.ID == taskID && task.Version == version && task.Status == TransferResourceWait {
			task.Status = TransferPrepared
			task.Version++
			s.tasks[key] = task
			return nil
		}
	}
	return errors.New("waiting task not found")
}

type fakeTRONSweepSource struct {
	balances   map[string]*big.Int
	accounts   map[string]TRONAccountState
	balanceErr error
}

func (s *fakeTRONSweepSource) TRC20Balance(context.Context, string, string) (*big.Int, error) {
	if s.balanceErr != nil {
		return nil, s.balanceErr
	}
	for _, value := range s.balances {
		return new(big.Int).Set(value), nil
	}
	return new(big.Int), nil
}

func (s *fakeTRONSweepSource) TRONAccountState(_ context.Context, address string) (TRONAccountState, error) {
	return s.accounts[address], nil
}

func newTRONSweepPlannerForTest(t *testing.T, store TRONSweepStore, source TRONSweepBalanceSource) *TRONSweepPlanner {
	t.Helper()
	planner, err := NewTRONSweepPlanner(store, source, TRONSweepPlannerOptions{
		Network: NetworkTronMainnet, ContractAddress: TronMainnetUSDTContract,
		DestinationAddress: javaTronAccountTestAddress, MinimumAmountRaw: "50000000",
		RequiredEnergy: 130000, RequiredBandwidth: 400, MinimumTRXBalanceSun: 100000000,
		BatchSize: 100,
	})
	require.NoError(t, err)
	return planner
}

func TestTRONSweepPlannerAppliesThresholdAndResourcePreflight(t *testing.T) {
	tests := []struct {
		name           string
		balance        int64
		account        TRONAccountState
		expectedStatus TransferStatus
		belowMinimum   int
	}{
		{name: "below minimum", balance: 49_999_999, belowMinimum: 1},
		{name: "delegated resources", balance: 50_000_000, account: TRONAccountState{EnergyLimit: 130000, FreeNetLimit: 400}, expectedStatus: TransferPrepared},
		{name: "TRX burn fallback", balance: 75_000_000, account: TRONAccountState{TRXBalanceSun: 100000000}, expectedStatus: TransferPrepared},
		{name: "resource wait", balance: 80_000_000, account: TRONAccountState{EnergyLimit: 129999, FreeNetLimit: 399, TRXBalanceSun: 99999999}, expectedStatus: TransferResourceWait},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := TRONSweepCandidate{
				IntentID: 7, Network: NetworkTronMainnet, SourceAddress: javaTronAccountTestAddress,
				DerivationIndex: 3, FundingMarker: 11,
			}
			store := &fakeTRONSweepStore{candidates: []TRONSweepCandidate{candidate}}
			source := &fakeTRONSweepSource{
				balances: map[string]*big.Int{candidate.SourceAddress: big.NewInt(tt.balance)},
				accounts: map[string]TRONAccountState{candidate.SourceAddress: tt.account},
			}
			result, err := newTRONSweepPlannerForTest(t, store, source).PlanBatch(context.Background())
			require.NoError(t, err)
			require.Equal(t, tt.belowMinimum, result.BelowMinimum)
			if tt.belowMinimum != 0 {
				require.Empty(t, store.inputs)
				return
			}
			require.Len(t, store.inputs, 1)
			require.Equal(t, tt.expectedStatus, store.inputs[0].Status)
			require.Equal(t, tt.balance, mustInt64(t, store.inputs[0].AmountRaw))
		})
	}
}

func TestTRONSweepPlannerUsesDeterministicIdempotencyAndIsolatesNodeErrors(t *testing.T) {
	candidate := TRONSweepCandidate{
		IntentID: 7, Network: NetworkTronMainnet, SourceAddress: javaTronAccountTestAddress,
		DerivationIndex: 3, FundingMarker: 11,
	}
	store := &fakeTRONSweepStore{candidates: []TRONSweepCandidate{candidate}}
	source := &fakeTRONSweepSource{
		balances: map[string]*big.Int{candidate.SourceAddress: big.NewInt(50_000_000)},
		accounts: map[string]TRONAccountState{candidate.SourceAddress: {EnergyLimit: 130000, FreeNetLimit: 400}},
	}
	planner := newTRONSweepPlannerForTest(t, store, source)
	first, err := planner.PlanBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, first.Prepared)
	second, err := planner.PlanBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, second.Existing)
	require.Len(t, store.inputs, 1)
	require.Contains(t, store.inputs[0].IdempotencyKey, "tron-sweep-v1:")

	source.balanceErr = errors.New("node unavailable")
	failed, err := planner.PlanBatch(context.Background())
	require.ErrorContains(t, err, "node unavailable")
	require.Equal(t, 1, failed.Failed)
	require.Len(t, store.inputs, 1)
}

func TestTRONSweepPlannerReleasesResourceWaitAfterResourcesRecover(t *testing.T) {
	candidate := TRONSweepCandidate{
		IntentID: 7, Network: NetworkTronMainnet, SourceAddress: javaTronAccountTestAddress,
		DerivationIndex: 3, FundingMarker: 11,
		WaitingTask: &TRONSweepTask{
			ID: 9, IdempotencyKey: "tron-sweep-v1:waiting", AmountRaw: "50000000",
			Status: TransferResourceWait, Version: 2,
		},
	}
	store := &fakeTRONSweepStore{
		candidates: []TRONSweepCandidate{candidate},
		tasks: map[string]TRONSweepTask{
			candidate.WaitingTask.IdempotencyKey: *candidate.WaitingTask,
		},
	}
	source := &fakeTRONSweepSource{
		balances: map[string]*big.Int{candidate.SourceAddress: big.NewInt(50_000_000)},
		accounts: map[string]TRONAccountState{candidate.SourceAddress: {EnergyLimit: 130000, FreeNetLimit: 400}},
	}
	result, err := newTRONSweepPlannerForTest(t, store, source).PlanBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Prepared)
	require.Equal(t, TransferPrepared, store.tasks[candidate.WaitingTask.IdempotencyKey].Status)
	require.Empty(t, store.inputs, "resource recovery must update the existing task")
}

func mustInt64(t *testing.T, raw string) int64 {
	t.Helper()
	value, ok := new(big.Int).SetString(raw, 10)
	require.True(t, ok)
	require.True(t, value.IsInt64())
	return value.Int64()
}
