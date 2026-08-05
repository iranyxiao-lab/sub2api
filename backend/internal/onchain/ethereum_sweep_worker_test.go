package onchain

import (
	"context"
	"math/big"
	"testing"
	"time"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

type fakeEthereumSweepExecutionStore struct {
	tasks        []EthereumSweepExecutionTask
	digest       string
	auditID      string
	txHash       string
	retryCode    string
	review       bool
	replacement  *EthereumSweepReplacement
	scheduled    bool
	finalization *EthereumSweepFinalization
}

func (s *fakeEthereumSweepExecutionStore) ListEthereumSweepExecutionTasks(context.Context, Network, time.Time, int) ([]EthereumSweepExecutionTask, error) {
	return append([]EthereumSweepExecutionTask(nil), s.tasks...), nil
}

func (s *fakeEthereumSweepExecutionStore) MarkEthereumSweepSigning(_ context.Context, _ int64, _ int, digest string) error {
	s.digest = digest
	return nil
}

func (s *fakeEthereumSweepExecutionStore) RecordEthereumSweepBroadcast(_ context.Context, _ int64, _ int, auditID, transactionHash string, _ uint64) error {
	s.auditID, s.txHash = auditID, transactionHash
	return nil
}

func (s *fakeEthereumSweepExecutionStore) RecordEthereumSweepReplacement(_ context.Context, input EthereumSweepReplacement) error {
	s.replacement = &input
	return nil
}

func (s *fakeEthereumSweepExecutionStore) ScheduleEthereumSweepCheck(context.Context, int64, int, time.Time) error {
	s.scheduled = true
	return nil
}

func (s *fakeEthereumSweepExecutionStore) FinalizeEthereumSweep(_ context.Context, input EthereumSweepFinalization) error {
	s.finalization = &input
	return nil
}

func (s *fakeEthereumSweepExecutionStore) RecordEthereumSweepRetry(_ context.Context, _ int64, _ int, _ TransferStatus, code, _ string, _ time.Time, review bool) error {
	s.retryCode, s.review = code, review
	return nil
}

type fakeERC20SweepSigner struct {
	requests []signerv1.SweepERC20Request
	response *signerv1.OperationResponse
}

func (s *fakeERC20SweepSigner) SweepERC20(_ context.Context, request signerv1.SweepERC20Request) (*signerv1.OperationResponse, error) {
	s.requests = append(s.requests, request)
	if s.response != nil {
		response := *s.response
		response.TaskID = request.TaskID
		response.IdempotencyKey = request.IdempotencyKey
		return &response, nil
	}
	return &signerv1.OperationResponse{
		Version: signerv1.APIVersion, TaskID: request.TaskID, IdempotencyKey: request.IdempotencyKey,
		Status: "broadcast", TransactionID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AuditID: "audit-sweep-1",
	}, nil
}

func TestEthereumSweepWorkerReplacesOnlyReceiptlessCurrentHash(t *testing.T) {
	xpub := ethereumGasFundingTestXPub(t)
	sourceAddress, err := DeriveEthereumAddress(xpub, 3)
	require.NoError(t, err)
	key, err := EthereumSweepIdempotencyKey(11, 3, 19, "250000000")
	require.NoError(t, err)
	original := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	replacement := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	store := &fakeEthereumSweepExecutionStore{tasks: []EthereumSweepExecutionTask{{ID: 8, IntentID: 11, ChainID: 1, DerivationIndex: 3, SourceAddress: sourceAddress, DestinationAddress: "0x0000000000000000000000000000000000000003", BalanceSnapshotRaw: "250000000", AmountRaw: "250000000", IdempotencyKey: key, TransactionHash: original, Status: TransferBroadcast, Version: 2}}}
	preflight := &fakeEthereumSweepPreflightSource{usdtBalance: mustBigInt(t, "250000000"), ethBalance: mustBigInt(t, DefaultEthereumMaxGasBudgetWei), gasLimit: 100_000, maxFee: mustBigInt(t, "50000000000"), receiptErr: ErrEthereumTransactionReceiptNotFound}
	signer := &fakeERC20SweepSigner{response: &signerv1.OperationResponse{Version: signerv1.APIVersion, Status: "broadcast", TransactionID: replacement, ReplacementOfTransactionID: original, TransactionVersion: 2, Nonce: 9, AuditID: "audit-replacement"}}
	worker := newEthereumSweepWorkerForTest(t, store, preflight, signer, xpub)
	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Broadcast)
	require.NotNil(t, store.replacement)
	require.Equal(t, original, store.replacement.OriginalTransactionHash)
	require.Equal(t, replacement, store.replacement.ReplacementTransactionHash)

	store.replacement = nil
	preflight.receiptErr = nil
	preflight.receipt = EthereumTransactionReceipt{TransactionHash: replacement}
	store.tasks[0].TransactionHash = replacement
	before := len(signer.requests)
	_, err = worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, signer.requests, before, "a transaction with a receipt must never be replaced")
	require.Nil(t, store.replacement)
}

func TestEthereumSweepWorkerWaitsForFinalizedGasAndRechecksBalances(t *testing.T) {
	xpub := ethereumGasFundingTestXPub(t)
	sourceAddress, err := DeriveEthereumAddress(xpub, 3)
	require.NoError(t, err)
	key, err := EthereumSweepIdempotencyKey(11, 3, 19, "250000000")
	require.NoError(t, err)
	store := &fakeEthereumSweepExecutionStore{tasks: []EthereumSweepExecutionTask{{
		ID: 8, IntentID: 11, ChainID: 1, DerivationIndex: 3, SourceAddress: sourceAddress,
		DestinationAddress: "0x0000000000000000000000000000000000000003",
		BalanceSnapshotRaw: "250000000", AmountRaw: "250000000", IdempotencyKey: key, Status: TransferPrepared,
	}}}
	preflight := &fakeEthereumSweepPreflightSource{
		usdtBalance: mustBigInt(t, "250000000"), ethBalance: mustBigInt(t, "0"), gasLimit: 100_000, maxFee: mustBigInt(t, "50000000000"),
	}
	signer := &fakeERC20SweepSigner{}
	worker := newEthereumSweepWorkerForTest(t, store, preflight, signer, xpub)

	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.GasWait)
	require.Equal(t, "GAS_FUNDING_NOT_FINALIZED", store.retryCode)
	require.Empty(t, signer.requests)

	preflight.ethBalance = mustBigInt(t, DefaultEthereumMaxGasBudgetWei)
	result, err = worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Broadcast)
	require.Len(t, signer.requests, 1)
	require.Equal(t, uint32(3), signer.requests[0].DerivationIndex)
	require.Equal(t, key, signer.requests[0].IdempotencyKey)
	require.NotEmpty(t, store.digest)
	require.Equal(t, "audit-sweep-1", store.auditID)
}

func TestEthereumSweepWorkerRejectsChangedBalanceAndDestination(t *testing.T) {
	xpub := ethereumGasFundingTestXPub(t)
	sourceAddress, err := DeriveEthereumAddress(xpub, 3)
	require.NoError(t, err)
	key, err := EthereumSweepIdempotencyKey(11, 3, 19, "250000000")
	require.NoError(t, err)
	store := &fakeEthereumSweepExecutionStore{tasks: []EthereumSweepExecutionTask{{
		ID: 8, IntentID: 11, ChainID: 1, DerivationIndex: 3, SourceAddress: sourceAddress,
		DestinationAddress: "0x0000000000000000000000000000000000000004",
		BalanceSnapshotRaw: "250000000", AmountRaw: "250000000", IdempotencyKey: key, Status: TransferPrepared,
	}}}
	preflight := &fakeEthereumSweepPreflightSource{
		usdtBalance: mustBigInt(t, "260000000"), ethBalance: mustBigInt(t, DefaultEthereumMaxGasBudgetWei),
		gasLimit: 100_000, maxFee: mustBigInt(t, "50000000000"),
	}
	signer := &fakeERC20SweepSigner{}
	worker := newEthereumSweepWorkerForTest(t, store, preflight, signer, xpub)

	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Review)
	require.Equal(t, "INVALID_SWEEP_TASK", store.retryCode)
	require.Empty(t, signer.requests)

	store.tasks[0].DestinationAddress = "0x0000000000000000000000000000000000000003"
	result, err = worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Review)
	require.Equal(t, "BALANCE_CHANGED", store.retryCode)
	require.Empty(t, signer.requests)
}

func TestEthereumSweepWorkerFinalizesWinningOriginalVersionAndStopsReplacement(t *testing.T) {
	xpub := ethereumGasFundingTestXPub(t)
	sourceAddress, err := DeriveEthereumAddress(xpub, 3)
	require.NoError(t, err)
	key, err := EthereumSweepIdempotencyKey(11, 3, 19, "250000000")
	require.NoError(t, err)
	original := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	replacement := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	blockHash := "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	destination := "0x0000000000000000000000000000000000000003"
	contract := EthereumMainnetUSDTContract
	amount := big.NewInt(250_000_000)
	input := make([]byte, 68)
	copy(input[:4], crypto.Keccak256([]byte("transfer(address,uint256)"))[:4])
	copy(input[16:36], common.HexToAddress(destination).Bytes())
	amount.FillBytes(input[36:68])
	data := make([]byte, 32)
	amount.FillBytes(data)
	logEntry := types.Log{Address: common.HexToAddress(contract), Topics: []common.Hash{ERC20TransferTopic, common.BytesToHash(common.LeftPadBytes(common.HexToAddress(sourceAddress).Bytes(), 32)), common.BytesToHash(common.LeftPadBytes(common.HexToAddress(destination).Bytes(), 32))}, Data: data, TxHash: common.HexToHash(original), BlockHash: common.HexToHash(blockHash), BlockNumber: 100, Index: 0}
	store := &fakeEthereumSweepExecutionStore{tasks: []EthereumSweepExecutionTask{{ID: 8, IntentID: 11, ChainID: 1, DerivationIndex: 3, SourceAddress: sourceAddress, DestinationAddress: destination, BalanceSnapshotRaw: amount.String(), AmountRaw: amount.String(), IdempotencyKey: key, TransactionHash: replacement, Nonce: 9, Versions: []EthereumTransactionVersionReference{{ID: 9, TransactionHash: original, Status: TransferReplaced}, {ID: 8, TransactionHash: replacement, Status: TransferBroadcast}}, Status: TransferBroadcast, Version: 3}}}
	preflight := &fakeEthereumSweepPreflightSource{usdtBalance: amount, ethBalance: mustBigInt(t, DefaultEthereumMaxGasBudgetWei), gasLimit: 100_000, maxFee: mustBigInt(t, "50000000000"), receipt: EthereumTransactionReceipt{TransactionHash: original, BlockHash: blockHash, BlockNumber: 100, Status: 1, GasUsed: 65_000, EffectiveGasPrice: big.NewInt(30_000_000_000), Logs: []types.Log{logEntry}}, receiptErr: nil, transactions: map[string]EthereumTransaction{original: {Hash: original, ChainID: 1, From: sourceAddress, To: contract, Nonce: 9, GasLimit: 65_000, ValueWei: "0", Input: input}}, finalized: EthereumBlockRef{Number: 120, Hash: "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}, block: EthereumBlockRef{Number: 100, Hash: blockHash, Timestamp: time.Unix(1_700_000_000, 0).UTC()}}
	signer := &fakeERC20SweepSigner{}
	worker := newEthereumSweepWorkerForTest(t, store, preflight, signer, xpub)
	result, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Finalized)
	require.NotNil(t, store.finalization)
	require.Equal(t, int64(9), store.finalization.WinnerID)
	require.Equal(t, original, store.finalization.WinnerHash)
	require.Empty(t, signer.requests)
}

func TestEthereumSweepReceiptRejectsDuplicateOrTamperedTransferSemantics(t *testing.T) {
	source := "0x0000000000000000000000000000000000000002"
	destination := "0x0000000000000000000000000000000000000003"
	contract := EthereumMainnetUSDTContract
	amount := big.NewInt(250_000_000)
	input := make([]byte, 68)
	copy(input[:4], crypto.Keccak256([]byte("transfer(address,uint256)"))[:4])
	copy(input[16:36], common.HexToAddress(destination).Bytes())
	amount.FillBytes(input[36:])
	transaction := EthereumTransaction{Hash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ChainID: 1, From: source, To: contract, Nonce: 9, GasLimit: 65_000, ValueWei: "0", Input: input}
	data := make([]byte, 32)
	amount.FillBytes(data)
	validLog := types.Log{Address: common.HexToAddress(contract), Topics: []common.Hash{ERC20TransferTopic, common.BytesToHash(common.LeftPadBytes(common.HexToAddress(source).Bytes(), 32)), common.BytesToHash(common.LeftPadBytes(common.HexToAddress(destination).Bytes(), 32))}, Data: data}
	task := EthereumSweepExecutionTask{ChainID: 1, SourceAddress: source, AmountRaw: amount.String(), Nonce: 9}
	require.NoError(t, validateEthereumSweepSemantics(transaction, EthereumTransactionReceipt{Logs: []types.Log{validLog}}, task, contract, destination))
	require.ErrorContains(t, validateEthereumSweepSemantics(transaction, EthereumTransactionReceipt{Logs: []types.Log{validLog, validLog}}, task, contract, destination), "Transfer receipt semantics")
	tampered := validLog
	tampered.Topics = append([]common.Hash(nil), validLog.Topics...)
	tampered.Topics[2] = common.BytesToHash(common.LeftPadBytes(common.HexToAddress(source).Bytes(), 32))
	require.ErrorContains(t, validateEthereumSweepSemantics(transaction, EthereumTransactionReceipt{Logs: []types.Log{tampered}}, task, contract, destination), "Transfer receipt semantics")
}

func newEthereumSweepWorkerForTest(t *testing.T, store EthereumSweepExecutionStore, source EthereumSweepPreflightSource, signer ERC20SweepSigner, xpub string) *EthereumSweepWorker {
	t.Helper()
	worker, err := NewEthereumSweepWorker(store, source, signer, EthereumSweepWorkerOptions{
		Network: NetworkEthereumMainnet, ChainID: EthereumMainnetChainID, AccountXPub: xpub,
		ContractAddress: EthereumMainnetUSDTContract, DestinationAddress: "0x0000000000000000000000000000000000000003",
		MinimumAmountRaw: DefaultEthereumMinimumSweepRaw, MaxGasBudgetWei: DefaultEthereumMaxGasBudgetWei,
		BatchSize: 10, MaxConsecutiveFailures: 3, RetryBackoff: time.Second,
	})
	require.NoError(t, err)
	return worker
}

func mustBigInt(t *testing.T, value string) *big.Int {
	t.Helper()
	result, ok := new(big.Int).SetString(value, 10)
	require.True(t, ok)
	return result
}
