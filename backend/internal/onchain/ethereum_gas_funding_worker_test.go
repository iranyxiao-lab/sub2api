package onchain

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/stretchr/testify/require"
)

type fakeEthereumGasFundingStore struct {
	tasks        []EthereumGasFundingTask
	digest       string
	auditID      string
	txHash       string
	retryCode    string
	review       bool
	confirming   bool
	finalization *EthereumGasFundingFinalization
	replacement  *EthereumGasFundingReplacement
}

func (s *fakeEthereumGasFundingStore) RecordEthereumGasFundingReplacement(_ context.Context, input EthereumGasFundingReplacement) error {
	s.replacement = &input
	return nil
}

func (s *fakeEthereumGasFundingStore) ListEthereumGasFundingTasks(context.Context, int64, time.Time, int, bool) ([]EthereumGasFundingTask, error) {
	return append([]EthereumGasFundingTask(nil), s.tasks...), nil
}

func (s *fakeEthereumGasFundingStore) MarkEthereumGasFundingSigning(_ context.Context, _ int64, _ int, digest string) error {
	s.digest = digest
	return nil
}

func (s *fakeEthereumGasFundingStore) RecordEthereumGasFundingBroadcast(_ context.Context, _ int64, _ int, auditID, transactionHash string) error {
	s.auditID, s.txHash = auditID, transactionHash
	return nil
}

func (s *fakeEthereumGasFundingStore) RecordEthereumGasFundingRetry(_ context.Context, _ int64, _ int, _ TransferStatus, code, _ string, _ time.Time, review bool) error {
	s.retryCode, s.review = code, review
	return nil
}

func (s *fakeEthereumGasFundingStore) ScheduleEthereumGasFundingCheck(context.Context, int64, int, TransferStatus, time.Time) error {
	return nil
}

func (s *fakeEthereumGasFundingStore) MarkEthereumGasFundingConfirming(context.Context, int64, int) error {
	s.confirming = true
	return nil
}

func (s *fakeEthereumGasFundingStore) FinalizeEthereumGasFunding(_ context.Context, input EthereumGasFundingFinalization) error {
	s.finalization = &input
	return nil
}

type fakeERC20GasFundingSigner struct {
	requests []signerv1.FundERC20GasRequest
	err      error
	response *signerv1.OperationResponse
}

func (s *fakeERC20GasFundingSigner) FundERC20Gas(_ context.Context, request signerv1.FundERC20GasRequest) (*signerv1.OperationResponse, error) {
	s.requests = append(s.requests, request)
	if s.err != nil {
		return nil, s.err
	}
	if s.response != nil {
		response := *s.response
		response.TaskID = request.TaskID
		response.IdempotencyKey = request.IdempotencyKey
		return &response, nil
	}
	return &signerv1.OperationResponse{
		Version: signerv1.APIVersion, TaskID: request.TaskID, IdempotencyKey: request.IdempotencyKey,
		Status: "broadcast", TransactionID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AuditID: "audit-gas-1",
	}, nil
}

func TestEthereumGasFundingWorkerPersistsRestrictedReplacement(t *testing.T) {
	xpub := ethereumGasFundingTestXPub(t)
	target, err := DeriveEthereumAddress(xpub, 3)
	require.NoError(t, err)
	key, err := EthereumGasFundingIdempotencyKey(11, 3, 19, "5000000000000000")
	require.NoError(t, err)
	original := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	replacement := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	store := &fakeEthereumGasFundingStore{tasks: []EthereumGasFundingTask{{ID: 7, IntentID: 11, ChainID: 1, TargetAddress: target, DerivationIndex: 3, AmountWei: "5000000000000000", IdempotencyKey: key, TransactionHash: original, Status: TransferBroadcast, Version: 2}}}
	signer := &fakeERC20GasFundingSigner{response: &signerv1.OperationResponse{Version: signerv1.APIVersion, Status: "broadcast", TransactionID: replacement, ReplacementOfTransactionID: original, TransactionVersion: 2, Nonce: 4, GasLimit: 21_000, MaxFeePerGasWei: "34500000000", MaxPriorityFeePerGasWei: "1150000000", AuditID: "audit-replacement"}}
	worker := newEthereumGasFundingTestWorker(t, store, &fakeEthereumGasFundingNode{receiptErr: ErrEthereumTransactionReceiptNotFound}, signer, xpub)
	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Broadcast)
	require.NotNil(t, store.replacement)
	require.Equal(t, original, store.replacement.OriginalTransactionHash)
	require.Equal(t, replacement, store.replacement.ReplacementTransactionHash)
	require.Equal(t, uint64(4), store.replacement.Nonce)
}

type fakeEthereumGasFundingNode struct {
	receipt      EthereumTransactionReceipt
	receiptErr   error
	finalized    EthereumBlockRef
	block        EthereumBlockRef
	transactions map[string]EthereumTransaction
}

func (n *fakeEthereumGasFundingNode) TransactionByHash(_ context.Context, hash string) (EthereumTransaction, error) {
	if transaction, ok := n.transactions[hash]; ok {
		return transaction, nil
	}
	return EthereumTransaction{}, ErrEthereumTransactionNotFound
}

func (n *fakeEthereumGasFundingNode) TransactionReceipt(context.Context, string) (EthereumTransactionReceipt, error) {
	return n.receipt, n.receiptErr
}

func (n *fakeEthereumGasFundingNode) FinalizedBlock(context.Context) (EthereumBlockRef, error) {
	return n.finalized, nil
}

func (n *fakeEthereumGasFundingNode) BlockByNumber(context.Context, uint64) (EthereumBlockRef, error) {
	return n.block, nil
}

func TestEthereumGasFundingWorkerRecomputesRegisteredTargetBeforeSigner(t *testing.T) {
	xpub := ethereumGasFundingTestXPub(t)
	key, err := EthereumGasFundingIdempotencyKey(11, 3, 19, "5000000000000000")
	require.NoError(t, err)
	store := &fakeEthereumGasFundingStore{tasks: []EthereumGasFundingTask{{
		ID: 7, IntentID: 11, ChainID: 1, TargetAddress: "0x0000000000000000000000000000000000000001",
		DerivationIndex: 3, AmountWei: "5000000000000000", IdempotencyKey: key, Status: TransferPrepared,
	}}}
	signer := &fakeERC20GasFundingSigner{}
	worker := newEthereumGasFundingTestWorker(t, store, &fakeEthereumGasFundingNode{}, signer, xpub)

	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Review)
	require.Empty(t, signer.requests)
	require.Equal(t, "INVALID_FUNDING_TASK", store.retryCode)
	require.True(t, store.review)
}

func TestEthereumGasFundingWorkerUsesDedicatedIdempotentSignerRequest(t *testing.T) {
	xpub := ethereumGasFundingTestXPub(t)
	target, err := DeriveEthereumAddress(xpub, 3)
	require.NoError(t, err)
	key, err := EthereumGasFundingIdempotencyKey(11, 3, 19, "5000000000000000")
	require.NoError(t, err)
	store := &fakeEthereumGasFundingStore{tasks: []EthereumGasFundingTask{{
		ID: 7, IntentID: 11, ChainID: 1, TargetAddress: target, DerivationIndex: 3,
		AmountWei: "5000000000000000", IdempotencyKey: key, Status: TransferPrepared,
	}}}
	signer := &fakeERC20GasFundingSigner{}
	worker := newEthereumGasFundingTestWorker(t, store, &fakeEthereumGasFundingNode{}, signer, xpub)

	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Broadcast)
	require.Len(t, signer.requests, 1)
	require.Equal(t, uint32(3), signer.requests[0].DerivationIndex)
	require.Equal(t, key, signer.requests[0].IdempotencyKey)
	require.NotEmpty(t, store.digest)
	require.Equal(t, "audit-gas-1", store.auditID)
	require.Equal(t, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", store.txHash)

	signer.err = errors.New("response lost")
	store.tasks[0].Status = TransferSigning
	store.tasks[0].Version = 1
	result, err = worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Retried)
	require.Len(t, signer.requests, 2)
	require.Equal(t, signer.requests[0], signer.requests[1], "a retry must replay the exact same signer request")
}

func TestEthereumGasFundingWorkerFinalizesOnlyAtCanonicalFinalizedBlock(t *testing.T) {
	xpub := ethereumGasFundingTestXPub(t)
	target, err := DeriveEthereumAddress(xpub, 3)
	require.NoError(t, err)
	key, err := EthereumGasFundingIdempotencyKey(11, 3, 19, "5000000000000000")
	require.NoError(t, err)
	txHash := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	blockHash := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	store := &fakeEthereumGasFundingStore{tasks: []EthereumGasFundingTask{{
		ID: 7, IntentID: 11, ChainID: 1, TargetAddress: target, DerivationIndex: 3,
		AmountWei: "5000000000000000", IdempotencyKey: key, TransactionHash: txHash,
		Status: TransferBroadcast, Version: 2,
	}}}
	node := &fakeEthereumGasFundingNode{
		receipt: EthereumTransactionReceipt{
			TransactionHash: txHash, BlockHash: blockHash, BlockNumber: 100, Status: 1,
			GasUsed: 21_000, EffectiveGasPrice: big.NewInt(30_000_000_000),
		},
		finalized:    EthereumBlockRef{Number: 120, Hash: "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		block:        EthereumBlockRef{Number: 100, Hash: blockHash, Timestamp: time.Unix(1_700_000_000, 0).UTC()},
		transactions: map[string]EthereumTransaction{txHash: {Hash: txHash, ChainID: 1, From: "0x1111111111111111111111111111111111111111", To: target, Nonce: 4, GasLimit: 21_000, ValueWei: "5000000000000000"}},
	}
	store.tasks[0].SponsorAddress = "0x1111111111111111111111111111111111111111"
	store.tasks[0].Nonce = 4
	store.tasks[0].GasLimit = 21_000
	store.tasks[0].Versions = []EthereumTransactionVersionReference{{ID: 7, TransactionHash: txHash, Status: TransferBroadcast}}
	worker := newEthereumGasFundingTestWorker(t, store, node, &fakeERC20GasFundingSigner{}, xpub)

	result, err := worker.TrackFinalizationOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Finalized)
	require.NotNil(t, store.finalization)
	require.Equal(t, int64(7), store.finalization.WinnerID)
	require.Equal(t, txHash, store.finalization.WinnerHash)
	require.Equal(t, int64(100), store.finalization.BlockHeight)
	require.Equal(t, "630000000000000", store.finalization.ActualFeeWei)
	require.Equal(t, node.block.Timestamp, store.finalization.FinalizedAt)
}

func newEthereumGasFundingTestWorker(t *testing.T, store EthereumGasFundingStore, node EthereumGasFundingNode, signer ERC20GasFundingSigner, xpub string) *EthereumGasFundingWorker {
	t.Helper()
	worker, err := NewEthereumGasFundingWorker(store, node, signer, EthereumGasFundingWorkerOptions{
		Network: NetworkEthereumMainnet, ChainID: EthereumMainnetChainID, AccountXPub: xpub,
		BatchSize: 10, MaxConsecutiveFailures: 3, RetryBackoff: time.Second, ConfirmationPoll: time.Second,
	})
	require.NoError(t, err)
	worker.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	return worker
}

func ethereumGasFundingTestXPub(t *testing.T) string {
	t.Helper()
	key, err := hdkeychain.NewMaster([]byte("ethereum-gas-funding-worker-test"), &chaincfg.MainNetParams)
	require.NoError(t, err)
	for _, index := range []uint32{44 + hdkeychain.HardenedKeyStart, 60 + hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, 0} {
		key, err = key.Derive(index)
		require.NoError(t, err)
	}
	xpub, err := key.Neuter()
	require.NoError(t, err)
	return xpub.String()
}
