package onchain

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
	"github.com/stretchr/testify/require"
)

type fakeTRONSweepExecutionStore struct {
	tasks []TRONSweepExecutionTask
}

func (s *fakeTRONSweepExecutionStore) ListTRONSweepExecutionTasks(_ context.Context, _ Network, _ time.Time, limit int, includeSigning bool) ([]TRONSweepExecutionTask, error) {
	result := make([]TRONSweepExecutionTask, 0, min(limit, len(s.tasks)))
	for _, task := range s.tasks {
		if !includeSigning && task.Status != TransferBroadcast && task.Status != TransferConfirming {
			continue
		}
		result = append(result, task)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (s *fakeTRONSweepExecutionStore) task(id int64) *TRONSweepExecutionTask {
	for index := range s.tasks {
		if s.tasks[index].ID == id {
			return &s.tasks[index]
		}
	}
	return nil
}

func (s *fakeTRONSweepExecutionStore) MarkTRONSweepSigning(_ context.Context, id int64, version int, _ string) error {
	task := s.task(id)
	if task == nil || task.Version != version || task.Status != TransferPrepared {
		return errors.New("claim conflict")
	}
	task.Status = TransferSigning
	task.Version++
	return nil
}

func (s *fakeTRONSweepExecutionStore) RecordTRONSweepBroadcast(_ context.Context, id int64, version int, _, transactionID string) error {
	task := s.task(id)
	if task == nil || task.Version != version || task.Status != TransferSigning {
		return errors.New("broadcast conflict")
	}
	task.Status = TransferBroadcast
	task.TransactionID = transactionID
	task.Version++
	return nil
}

func (s *fakeTRONSweepExecutionStore) RecordTRONSweepRetry(_ context.Context, id int64, version int, status TransferStatus, _, _ string, _ time.Time, review bool) error {
	task := s.task(id)
	if task == nil || task.Version != version || task.Status != status {
		return errors.New("retry conflict")
	}
	task.RetryCount++
	if review {
		task.Status = TransferReviewRequired
	}
	task.Version++
	return nil
}

func (s *fakeTRONSweepExecutionStore) ScheduleTRONSweepCheck(_ context.Context, id int64, version int, status TransferStatus, _ time.Time) error {
	task := s.task(id)
	if task == nil || task.Version != version || task.Status != status {
		return errors.New("schedule conflict")
	}
	task.Version++
	return nil
}

func (s *fakeTRONSweepExecutionStore) MarkTRONSweepConfirming(_ context.Context, id int64, version int) error {
	task := s.task(id)
	if task == nil || task.Version != version || task.Status != TransferBroadcast {
		return errors.New("confirm conflict")
	}
	task.Status = TransferConfirming
	task.Version++
	return nil
}

func (s *fakeTRONSweepExecutionStore) FinalizeTRONSweep(_ context.Context, input TRONSweepFinalization) error {
	task := s.task(input.TaskID)
	if task == nil || task.Version != input.Version || task.Status != TransferConfirming {
		return errors.New("finalize conflict")
	}
	task.Status = TransferFinalized
	task.Version++
	return nil
}

type fakeTRONSweepExecutionNode struct {
	balances  []*big.Int
	status    TRONSolidifiedTransaction
	statusErr error
	calls     int
}

func (n *fakeTRONSweepExecutionNode) TRC20Balance(context.Context, string, string) (*big.Int, error) {
	if len(n.balances) == 0 {
		return nil, errors.New("no balance")
	}
	index := min(n.calls, len(n.balances)-1)
	n.calls++
	return new(big.Int).Set(n.balances[index]), nil
}

func (n *fakeTRONSweepExecutionNode) SolidifiedTRONTransaction(context.Context, string) (TRONSolidifiedTransaction, error) {
	return n.status, n.statusErr
}

type fakeTRC20SweepSigner struct {
	response *signerv1.OperationResponse
	err      error
	calls    int
}

func (s *fakeTRC20SweepSigner) SweepTRC20(_ context.Context, request signerv1.SweepTRC20Request) (*signerv1.OperationResponse, error) {
	s.calls++
	if s.response != nil {
		response := *s.response
		response.TaskID = request.TaskID
		response.IdempotencyKey = request.IdempotencyKey
		return &response, s.err
	}
	return nil, s.err
}

func newTRONSweepExecutorForTest(t *testing.T, store TRONSweepExecutionStore, node TRONSweepExecutionNode, signer TRC20SweepSigner, destination string) *TRONSweepExecutor {
	t.Helper()
	executor, err := NewTRONSweepExecutor(store, node, signer, TRONSweepExecutorOptions{
		Network: NetworkTronMainnet, ContractAddress: TronMainnetUSDTContract, DestinationAddress: destination,
		BatchSize: 10, MaxConsecutiveFailures: 3, RetryBackoff: time.Second, ConfirmationPoll: time.Second,
	})
	require.NoError(t, err)
	executor.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	return executor
}

func TestTRONSweepExecutorBroadcastsPreparedTask(t *testing.T) {
	task := TRONSweepExecutionTask{
		ID: 7, DerivationIndex: 3, SourceAddress: javaTronAccountTestAddress,
		DestinationAddress: javaTronAccountTestAddress, BalanceSnapshotRaw: "50000000", AmountRaw: "50000000",
		IdempotencyKey: "tron-sweep-v1:prepared", Status: TransferPrepared,
	}
	store := &fakeTRONSweepExecutionStore{tasks: []TRONSweepExecutionTask{task}}
	node := &fakeTRONSweepExecutionNode{balances: []*big.Int{big.NewInt(50_000_000)}}
	signer := &fakeTRC20SweepSigner{response: &signerv1.OperationResponse{
		Status: "broadcast", TransactionID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AuditID: "audit-1",
	}}
	result, err := newTRONSweepExecutorForTest(t, store, node, signer, task.DestinationAddress).RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Broadcast)
	require.Equal(t, TransferBroadcast, store.tasks[0].Status)
	require.Equal(t, 1, signer.calls)
}

func TestTRONSweepExecutorReviewsUnknownBroadcastAfterBalanceChanges(t *testing.T) {
	task := TRONSweepExecutionTask{
		ID: 8, DerivationIndex: 4, SourceAddress: javaTronAccountTestAddress,
		DestinationAddress: javaTronAccountTestAddress, BalanceSnapshotRaw: "50000000", AmountRaw: "50000000",
		IdempotencyKey: "tron-sweep-v1:unknown", Status: TransferSigning,
	}
	store := &fakeTRONSweepExecutionStore{tasks: []TRONSweepExecutionTask{task}}
	node := &fakeTRONSweepExecutionNode{balances: []*big.Int{big.NewInt(100_000_000), big.NewInt(75_000_000)}}
	signer := &fakeTRC20SweepSigner{err: errors.New("response lost")}
	result, err := newTRONSweepExecutorForTest(t, store, node, signer, task.DestinationAddress).RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Review)
	require.Equal(t, TransferReviewRequired, store.tasks[0].Status)
	require.Equal(t, 1, signer.calls, "the same signer idempotency key is retried only after a chain balance lookup")
}

func TestTRONSweepExecutorTracksWithoutSigningWhenFundingOperationsArePaused(t *testing.T) {
	task := TRONSweepExecutionTask{
		ID: 11, DerivationIndex: 7, SourceAddress: javaTronAccountTestAddress,
		DestinationAddress: javaTronAccountTestAddress, AmountRaw: "50000000",
		IdempotencyKey: "tron-sweep-v1:paused", Status: TransferPrepared,
	}
	store := &fakeTRONSweepExecutionStore{tasks: []TRONSweepExecutionTask{task}}
	node := &fakeTRONSweepExecutionNode{}
	executor, err := NewTRONSweepTracker(store, node, TRONSweepExecutorOptions{
		Network: NetworkTronMainnet, ContractAddress: TronMainnetUSDTContract, DestinationAddress: task.DestinationAddress,
		BatchSize: 10, MaxConsecutiveFailures: 3, RetryBackoff: time.Second, ConfirmationPoll: time.Second,
	})
	require.NoError(t, err)
	result, err := executor.TrackSolidificationOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, result.Tasks)
	require.Equal(t, TransferPrepared, store.tasks[0].Status)
	_, err = executor.RunOnce(context.Background())
	require.ErrorContains(t, err, "signing is disabled")
}

func TestTRONSweepTrackerFinalizesBroadcastTaskWithoutSigner(t *testing.T) {
	receipt, options, source := validTRC20TransferFixture(t)
	transfer, err := ParseTRC20Transfer(receipt, 0, options)
	require.NoError(t, err)
	task := TRONSweepExecutionTask{
		ID: 13, DerivationIndex: 9, SourceAddress: source, DestinationAddress: options.RecipientAddress,
		AmountRaw: transfer.AmountRaw, IdempotencyKey: "tron-sweep-v1:tracking-only", TransactionID: receipt.TransactionID,
		Status: TransferBroadcast,
	}
	store := &fakeTRONSweepExecutionStore{tasks: []TRONSweepExecutionTask{task}}
	node := &fakeTRONSweepExecutionNode{status: TRONSolidifiedTransaction{
		Found: true, Success: true, BlockHeight: receipt.BlockHeight,
		BlockHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Receipt:   receipt,
	}}
	executor, err := NewTRONSweepTracker(store, node, TRONSweepExecutorOptions{
		Network: NetworkTronMainnet, ContractAddress: TronMainnetUSDTContract, DestinationAddress: task.DestinationAddress,
		BatchSize: 10, MaxConsecutiveFailures: 3, RetryBackoff: time.Second, ConfirmationPoll: time.Second,
	})
	require.NoError(t, err)
	result, err := executor.TrackSolidificationOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Tasks)
	require.Equal(t, 1, result.Finalized)
	require.Equal(t, TransferFinalized, store.tasks[0].Status)
}

func TestPausedFundingOperationsDoNotBlockDepositSettlement(t *testing.T) {
	sweepTask := TRONSweepExecutionTask{
		ID: 12, DerivationIndex: 8, SourceAddress: javaTronAccountTestAddress,
		DestinationAddress: javaTronAccountTestAddress, AmountRaw: "50000000",
		IdempotencyKey: "tron-sweep-v1:paused-settlement", Status: TransferPrepared,
	}
	sweepStore := &fakeTRONSweepExecutionStore{tasks: []TRONSweepExecutionTask{sweepTask}}
	signer := &fakeTRC20SweepSigner{}
	executor := newTRONSweepExecutorForTest(t, sweepStore, &fakeTRONSweepExecutionNode{}, signer, sweepTask.DestinationAddress)
	tracking, err := executor.TrackSolidificationOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, tracking.Tasks)
	require.Equal(t, TransferPrepared, sweepStore.tasks[0].Status)
	require.Zero(t, signer.calls)

	settlementStore := &settlementStoreStub{
		ids: []int64{42},
		prepared: map[int64]SettlementPreparation{42: {
			IntentID: 42, Status: IntentSettlementDue, ExpectedAmountRaw: "50000000",
			ReceivedAmountRaw: "50000000", Ready: true,
		}},
		prepareErrs: map[int64]error{},
	}
	confirmer := &settlementConfirmerStub{errs: map[int64]error{}}
	worker, err := NewSettlementWorker(settlementStore, confirmer, SettlementWorkerOptions{
		Network: NetworkTronMainnet, BatchSize: 10, PollInterval: time.Second,
	})
	require.NoError(t, err)
	settled, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, settled.Confirmed)
	require.Equal(t, []int64{42}, confirmer.calls)
}

func TestTRONSweepExecutorPausesAfterConsecutiveFailures(t *testing.T) {
	task := TRONSweepExecutionTask{
		ID: 9, DerivationIndex: 5, SourceAddress: javaTronAccountTestAddress,
		DestinationAddress: javaTronAccountTestAddress, AmountRaw: "50000000",
		IdempotencyKey: "tron-sweep-v1:failure", Status: TransferSigning, RetryCount: 2,
	}
	store := &fakeTRONSweepExecutionStore{tasks: []TRONSweepExecutionTask{task}}
	node := &fakeTRONSweepExecutionNode{balances: []*big.Int{big.NewInt(50_000_000), big.NewInt(50_000_000)}}
	signer := &fakeTRC20SweepSigner{err: errors.New("signer unavailable")}
	result, err := newTRONSweepExecutorForTest(t, store, node, signer, task.DestinationAddress).RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Review)
	require.Equal(t, TransferReviewRequired, store.tasks[0].Status)
}

func TestTRONSweepExecutorFinalizesOnlyMatchingSolidifiedTransfer(t *testing.T) {
	receipt, options, source := validTRC20TransferFixture(t)
	transfer, err := ParseTRC20Transfer(receipt, 0, options)
	require.NoError(t, err)
	task := TRONSweepExecutionTask{
		ID: 10, DerivationIndex: 6, SourceAddress: source, DestinationAddress: options.RecipientAddress,
		AmountRaw: transfer.AmountRaw, IdempotencyKey: "tron-sweep-v1:final", TransactionID: receipt.TransactionID,
		Status: TransferBroadcast,
	}
	store := &fakeTRONSweepExecutionStore{tasks: []TRONSweepExecutionTask{task}}
	node := &fakeTRONSweepExecutionNode{status: TRONSolidifiedTransaction{
		Found: true, Success: true, BlockHeight: receipt.BlockHeight,
		BlockHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		FeeSun:    1000, EnergyUsed: 64000, BandwidthUsed: 345, Receipt: receipt,
	}}
	signer := &fakeTRC20SweepSigner{}
	result, err := newTRONSweepExecutorForTest(t, store, node, signer, task.DestinationAddress).RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Finalized)
	require.Equal(t, TransferFinalized, store.tasks[0].Status)
	require.Equal(t, 0, signer.calls)
}
