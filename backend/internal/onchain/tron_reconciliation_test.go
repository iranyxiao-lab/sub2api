package onchain

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeTRONReconciliationStore struct {
	targets []TRONBalanceReconciliationTarget
	reviews []TRONBalanceReconciliationMismatch
	markErr error
	cursor  TRONScanCursor
}

func (s *fakeTRONReconciliationStore) LoadTRONScanCursor(context.Context, Network) (TRONScanCursor, error) {
	return s.cursor, nil
}

func (s *fakeTRONReconciliationStore) ListTRONBalanceReconciliationTargets(_ context.Context, _ Network, afterIntentID int64, limit int) ([]TRONBalanceReconciliationTarget, error) {
	result := make([]TRONBalanceReconciliationTarget, 0, limit)
	for _, target := range s.targets {
		if target.IntentID > afterIntentID {
			result = append(result, target)
		}
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (s *fakeTRONReconciliationStore) MarkTRONBalanceReconciliationReview(_ context.Context, mismatch TRONBalanceReconciliationMismatch) error {
	if s.markErr != nil {
		return s.markErr
	}
	s.reviews = append(s.reviews, mismatch)
	return nil
}

type fakeTRONReconciliationNode struct {
	balances map[string]*big.Int
	err      error
	calls    int
	height   int64
}

func (n *fakeTRONReconciliationNode) LatestSolidifiedHeight(context.Context) (int64, error) {
	if n.err != nil {
		return 0, n.err
	}
	return n.height, nil
}

func (n *fakeTRONReconciliationNode) SolidifiedTRC20Balance(_ context.Context, _ string, address string) (*big.Int, error) {
	n.calls++
	if n.err != nil {
		return nil, n.err
	}
	if balance, ok := n.balances[address]; ok {
		return new(big.Int).Set(balance), nil
	}
	return new(big.Int), nil
}

func newTRONReconcilerForTest(t *testing.T, store TRONBalanceReconciliationStore, node TRONBalanceReconciliationNode, batchSize int) *TRONBalanceReconciler {
	t.Helper()
	reconciler, err := NewTRONBalanceReconciler(store, node, TRONBalanceReconcilerOptions{
		Network: NetworkTronMainnet, ContractAddress: TronMainnetUSDTContract, BatchSize: batchSize,
	})
	require.NoError(t, err)
	return reconciler
}

func reconciliationStore(targets ...TRONBalanceReconciliationTarget) *fakeTRONReconciliationStore {
	return &fakeTRONReconciliationStore{
		targets: targets,
		cursor:  TRONScanCursor{FinalizedHeight: 100, Health: CursorHealthy},
	}
}

func reconciliationTarget(id int64, address string) TRONBalanceReconciliationTarget {
	return TRONBalanceReconciliationTarget{
		IntentID: id, Network: NetworkTronMainnet, TokenContract: TronMainnetUSDTContract,
		Address: address, IntentStatus: IntentSettled,
		RecordedDepositsRaw: "100000000", CreditedAmountRaw: "100000000", FinalizedSweptRaw: "60000000",
	}
}

func TestTRONBalanceReconcilerAcceptsConservedBalancesAcrossPages(t *testing.T) {
	store := reconciliationStore(
		reconciliationTarget(1, TronMainnetUSDTContract),
		reconciliationTarget(2, TronMainnetUSDTContract),
	)
	node := &fakeTRONReconciliationNode{balances: map[string]*big.Int{TronMainnetUSDTContract: big.NewInt(40_000_000)}, height: 100}

	result, err := newTRONReconcilerForTest(t, store, node, 1).RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, result.Targets)
	require.Equal(t, 2, result.Checked)
	require.Zero(t, result.Mismatches)
	require.Empty(t, store.reviews)
}

func TestTRONBalanceReconcilerSendsUnexplainedBalanceDifferenceToReview(t *testing.T) {
	target := reconciliationTarget(7, TronMainnetUSDTContract)
	store := reconciliationStore(target)
	node := &fakeTRONReconciliationNode{balances: map[string]*big.Int{target.Address: big.NewInt(39_000_000)}, height: 100}

	result, err := newTRONReconcilerForTest(t, store, node, 100).RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Mismatches)
	require.Equal(t, 1, result.Reviewed)
	require.Len(t, store.reviews, 1)
	require.Equal(t, "ONCHAIN_BALANCE_MISMATCH", store.reviews[0].ReasonCode)
	require.Equal(t, "40000000", store.reviews[0].ExpectedBalanceRaw)
	require.Equal(t, "39000000", store.reviews[0].ActualBalanceRaw)
	require.Contains(t, store.reviews[0].Reason, "recorded=100000000")
}

func TestTRONBalanceReconcilerValidatesCreditedAndSweptLedgerAmounts(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*TRONBalanceReconciliationTarget)
		reasonCode string
	}{
		{name: "credited exceeds deposits", mutate: func(target *TRONBalanceReconciliationTarget) { target.CreditedAmountRaw = "100000001" }, reasonCode: "CREDIT_EXCEEDS_DEPOSITS"},
		{name: "settled credit differs", mutate: func(target *TRONBalanceReconciliationTarget) { target.CreditedAmountRaw = "90000000" }, reasonCode: "SETTLED_CREDIT_MISMATCH"},
		{name: "sweeps exceed deposits", mutate: func(target *TRONBalanceReconciliationTarget) { target.FinalizedSweptRaw = "100000001" }, reasonCode: "SWEEPS_EXCEED_DEPOSITS"},
		{name: "non canonical amount", mutate: func(target *TRONBalanceReconciliationTarget) { target.RecordedDepositsRaw = "0100000000" }, reasonCode: "INVALID_RECORDED_DEPOSITS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := reconciliationTarget(9, TronMainnetUSDTContract)
			tt.mutate(&target)
			store := reconciliationStore(target)
			node := &fakeTRONReconciliationNode{height: 100}
			result, err := newTRONReconcilerForTest(t, store, node, 100).RunOnce(context.Background())
			require.NoError(t, err)
			require.Equal(t, 1, result.Reviewed)
			require.Equal(t, tt.reasonCode, store.reviews[0].ReasonCode)
			require.Zero(t, node.calls, "ledger mismatches should not require a node query")
		})
	}
}

func TestTRONBalanceReconcilerSkipsInFlightSweepsAndIsolatesNodeErrors(t *testing.T) {
	inFlight := reconciliationTarget(1, TronMainnetUSDTContract)
	inFlight.HasInFlightSweep = true
	failing := reconciliationTarget(2, TronMainnetUSDTContract)
	store := reconciliationStore(inFlight, failing)
	node := &fakeTRONReconciliationNode{height: 100}
	nodeBalanceError := errors.New("node unavailable")
	failingNode := &balanceFailingTRONReconciliationNode{fakeTRONReconciliationNode: node, balanceErr: nodeBalanceError}

	result, err := newTRONReconcilerForTest(t, store, failingNode, 100).RunOnce(context.Background())
	require.ErrorContains(t, err, "node unavailable")
	require.Equal(t, 1, result.Skipped)
	require.Equal(t, 1, result.Failed)
	require.Equal(t, 1, node.calls)
	require.Empty(t, store.reviews)
}

type balanceFailingTRONReconciliationNode struct {
	*fakeTRONReconciliationNode
	balanceErr error
}

func (n *balanceFailingTRONReconciliationNode) SolidifiedTRC20Balance(context.Context, string, string) (*big.Int, error) {
	n.calls++
	return nil, n.balanceErr
}

func TestTRONBalanceReconcilerDefersUntilScannerReachesSolidifiedHead(t *testing.T) {
	target := reconciliationTarget(1, TronMainnetUSDTContract)
	store := reconciliationStore(target)
	store.cursor.FinalizedHeight = 99
	node := &fakeTRONReconciliationNode{height: 100}

	result, err := newTRONReconcilerForTest(t, store, node, 100).RunOnce(context.Background())
	require.NoError(t, err)
	require.True(t, result.Deferred)
	require.Zero(t, result.Targets)
	require.Zero(t, node.calls)
}

type boundaryMovingTRONReconciliationNode struct {
	heightCalls int
}

func (n *boundaryMovingTRONReconciliationNode) LatestSolidifiedHeight(context.Context) (int64, error) {
	n.heightCalls++
	if n.heightCalls == 1 {
		return 100, nil
	}
	return 101, nil
}

func (*boundaryMovingTRONReconciliationNode) SolidifiedTRC20Balance(context.Context, string, string) (*big.Int, error) {
	return big.NewInt(39_000_000), nil
}

func TestTRONBalanceReconcilerDoesNotReviewAcrossMovingSolidifiedBoundary(t *testing.T) {
	target := reconciliationTarget(1, TronMainnetUSDTContract)
	store := reconciliationStore(target)

	result, err := newTRONReconcilerForTest(t, store, &boundaryMovingTRONReconciliationNode{}, 100).RunOnce(context.Background())
	require.ErrorIs(t, err, ErrTRONReconciliationBoundaryMoved)
	require.Zero(t, result.Mismatches)
	require.Empty(t, store.reviews)
}
