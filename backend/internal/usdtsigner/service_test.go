package usdtsigner

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
	"github.com/Wei-Shaw/sub2api/internal/usdtsigner/custody"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestServiceReplacesStuckEthereumTransactionWithPersistedIdenticalSemantics(t *testing.T) {
	keys, keyConfig := testServiceKeySet(t)
	journalPath := filepath.Join(t.TempDir(), "operations.jsonl")
	journal, err := OpenOperationJournal(journalPath)
	require.NoError(t, err)
	policy, err := NewPolicyEngine(testServicePolicyConfig(), journal)
	require.NoError(t, err)
	now := time.Unix(1_700_000_000, 0).UTC()
	node := testEthereumNode()
	node.gasLimit = 21_000
	service, err := NewService(ServiceOptions{Keys: keys, KeyConfig: keyConfig, TRONConfig: testTRONOperationConfig(), EthereumConfig: testEthereumOperationConfig(), TRONNode: &fakeTRONNode{version: 201910292, balance: big.NewInt(1_000_000_000)}, EthereumNode: node, Policy: policy, Journal: journal, Clock: func() time.Time { return now }})
	require.NoError(t, err)
	request := signerv1.FundERC20GasRequest{TaskID: "gas-replace-1", DerivationIndex: 3, RawAmount: "3000000000000000", IdempotencyKey: "gas-replace-idem-1"}
	first, err := service.FundERC20Gas(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 1, first.TransactionVersion)
	require.Len(t, node.sent, 1)

	now = now.Add(16 * time.Minute)
	node.nonce = node.sent[0].Nonce() + 1
	second, err := service.FundERC20Gas(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, node.sent, 2)
	require.Equal(t, first.TransactionID, second.ReplacementOfTransactionID)
	require.Equal(t, 2, second.TransactionVersion)
	original, replacement := node.sent[0], node.sent[1]
	require.Equal(t, original.Nonce(), replacement.Nonce())
	require.Equal(t, original.To(), replacement.To())
	require.Zero(t, original.Value().Cmp(replacement.Value()))
	require.Equal(t, original.Data(), replacement.Data())
	require.Equal(t, original.Gas(), replacement.Gas())
	require.Positive(t, replacement.GasFeeCapCmp(original))
	require.Positive(t, replacement.GasTipCapCmp(original))

	require.NoError(t, journal.Close())
	reloaded, err := OpenOperationJournal(journalPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reloaded.Close()) })
	record, replay, err := reloaded.Begin(OperationInput{Operation: OperationFundERC20Gas, TaskID: request.TaskID, DerivationIndex: request.DerivationIndex, RawAmount: request.RawAmount, IdempotencyKey: request.IdempotencyKey, RequestDigest: mustSignerRequestDigest(t, OperationFundERC20Gas, request.TaskID, request.DerivationIndex, request.RawAmount, request.IdempotencyKey), Now: now})
	require.NoError(t, err)
	require.True(t, replay)
	require.Len(t, record.EthereumVersions, 2)
	require.Equal(t, first.TransactionID, record.EthereumVersions[1].ReplacementOf)
	originalDayTotal, err := reloaded.DailyAmount(OperationFundERC20Gas, time.Unix(1_700_000_000, 0).UTC())
	require.NoError(t, err)
	require.Equal(t, request.RawAmount, originalDayTotal.String())
	replacementDayTotal, err := reloaded.DailyAmount(OperationFundERC20Gas, time.Unix(1_700_000_000, 0).UTC().Add(24*time.Hour))
	require.NoError(t, err)
	require.Zero(t, replacementDayTotal.Sign(), "replacement must not double-count or move policy spending to another day")
}

func TestServiceRecoversUnknownBroadcastAndRejectsReplacementAboveFeeCaps(t *testing.T) {
	keys, keyConfig := testServiceKeySet(t)
	journal, err := OpenOperationJournal(filepath.Join(t.TempDir(), "operations.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, journal.Close()) })
	policy, err := NewPolicyEngine(testServicePolicyConfig(), journal)
	require.NoError(t, err)
	now := time.Unix(1_700_000_000, 0).UTC()
	node := testEthereumNode()
	node.gasLimit = 21_000
	config := testEthereumOperationConfig()
	service, err := NewService(ServiceOptions{Keys: keys, KeyConfig: keyConfig, TRONConfig: testTRONOperationConfig(), EthereumConfig: config, TRONNode: &fakeTRONNode{version: 201910292, balance: big.NewInt(1_000_000_000)}, EthereumNode: node, Policy: policy, Journal: journal, Clock: func() time.Time { return now }})
	require.NoError(t, err)
	request := signerv1.FundERC20GasRequest{TaskID: "gas-unknown-1", DerivationIndex: 3, RawAmount: "3000000000000000", IdempotencyKey: "gas-unknown-idem-1"}
	first, err := service.FundERC20Gas(context.Background(), request)
	require.NoError(t, err)
	delete(node.transactions, common.HexToHash(first.TransactionID))
	now = now.Add(config.PendingThreshold + time.Second)
	second, err := service.FundERC20Gas(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, first.TransactionID, second.ReplacementOfTransactionID)
	require.Len(t, node.sent, 2, "unknown broadcast recovery must rebuild only from the original high-level request")

	latest := node.sent[1]
	config.MaxFeePerGasWei = latest.GasFeeCap()
	config.MaxPriorityFeePerGasWei = latest.GasTipCap()
	service.ethereumConfig = config
	delete(node.transactions, latest.Hash())
	now = now.Add(config.PendingThreshold + time.Second)
	_, err = service.FundERC20Gas(context.Background(), request)
	require.ErrorContains(t, err, "fees exceed signer policy")
	require.Len(t, node.sent, 2, "a replacement above fixed fee caps must not be broadcast")
}

func mustSignerRequestDigest(t *testing.T, operation OperationKind, taskID string, derivationIndex uint32, amount, key string) string {
	t.Helper()
	digest, err := signerRequestDigest(operation, taskID, derivationIndex, amount, key)
	require.NoError(t, err)
	return digest
}

type serviceTestUnsealer map[string][]byte

func (u serviceTestUnsealer) Unseal(sealed custody.SealedKey) ([]byte, error) {
	return append([]byte(nil), u[sealed.KeyID]...), nil
}

func TestServiceProvidesPersistentIdempotencyAndRoleIsolatedOperations(t *testing.T) {
	keys, keyConfig := testServiceKeySet(t)
	journal, err := OpenOperationJournal(filepath.Join(t.TempDir(), "operations.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, journal.Close()) })
	policy, err := NewPolicyEngine(testServicePolicyConfig(), journal)
	require.NoError(t, err)
	tronNode := &fakeTRONNode{version: 201910292, balance: big.NewInt(1_000_000_000)}
	ethereumNode := testEthereumNode()
	service, err := NewService(ServiceOptions{
		Keys: keys, KeyConfig: keyConfig, TRONConfig: testTRONOperationConfig(),
		EthereumConfig: testEthereumOperationConfig(), TRONNode: tronNode, EthereumNode: ethereumNode,
		Policy: policy, Journal: journal, Clock: func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	require.NoError(t, err)

	request := signerv1.SweepTRC20Request{
		TaskID: "tron-task-1", DerivationIndex: 2, RawAmount: "50000000", IdempotencyKey: "tron-idem-1",
	}
	first, err := service.SweepTRC20(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, string(OperationStatusBroadcast), first.Status)
	require.Equal(t, 1, tronNode.broadcasts)
	second, err := service.SweepTRC20(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, first.TransactionID, second.TransactionID)
	require.Equal(t, first.AuditID, second.AuditID)
	require.Equal(t, 1, tronNode.broadcasts)

	request.RawAmount = "50000001"
	_, err = service.SweepTRC20(t.Context(), request)
	require.ErrorIs(t, err, ErrOperationUnavailable)
	require.Equal(t, 1, tronNode.broadcasts)

	ethereumNode.gasLimit = 21_000
	gasResponse, err := service.FundERC20Gas(t.Context(), signerv1.FundERC20GasRequest{
		TaskID: "gas-task-1", DerivationIndex: 3, RawAmount: "3000000000000000", IdempotencyKey: "gas-idem-1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, gasResponse.TransactionID)
	require.Len(t, ethereumNode.sent, 1)

	ethereumNode.gasLimit = 65_000
	ercResponse, err := service.SweepERC20(t.Context(), signerv1.SweepERC20Request{
		TaskID: "erc-task-1", DerivationIndex: 4, RawAmount: "250000000", IdempotencyKey: "erc-idem-1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, ercResponse.TransactionID)
	require.Len(t, ethereumNode.sent, 2)
}

func TestServicePolicyDenialPausesOnlyFundsOperation(t *testing.T) {
	keys, keyConfig := testServiceKeySet(t)
	journal, err := OpenOperationJournal(filepath.Join(t.TempDir(), "operations.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, journal.Close()) })
	policyConfig := testServicePolicyConfig()
	policyConfig.SweepTRC20.SingleAmount = big.NewInt(49_999_999)
	policy, err := NewPolicyEngine(policyConfig, journal)
	require.NoError(t, err)
	tronNode := &fakeTRONNode{version: 201910292, balance: big.NewInt(1_000_000_000)}
	ethereumNode := testEthereumNode()
	service, err := NewService(ServiceOptions{
		Keys: keys, KeyConfig: keyConfig, TRONConfig: testTRONOperationConfig(),
		EthereumConfig: testEthereumOperationConfig(), TRONNode: tronNode, EthereumNode: ethereumNode,
		Policy: policy, Journal: journal,
	})
	require.NoError(t, err)
	_, err = service.SweepTRC20(t.Context(), signerv1.SweepTRC20Request{
		TaskID: "tron-task-1", DerivationIndex: 2, RawAmount: "50000000", IdempotencyKey: "tron-idem-1",
	})
	require.ErrorIs(t, err, ErrOperationUnavailable)
	require.Zero(t, tronNode.broadcasts)

	overLimit := new(big.Int).Add(policyConfig.FundERC20Gas.SingleAmount, big.NewInt(1))
	_, err = service.FundERC20Gas(t.Context(), signerv1.FundERC20GasRequest{
		TaskID: "gas-task-1", DerivationIndex: 2, RawAmount: overLimit.String(), IdempotencyKey: "gas-idem-1",
	})
	require.ErrorIs(t, err, ErrOperationUnavailable)
	require.Empty(t, ethereumNode.sent)
}

func testServicePolicyConfig() PolicyConfig {
	return PolicyConfig{
		SweepTRC20:   OperationLimit{SingleAmount: big.NewInt(100_000_000_000), DailyAmount: big.NewInt(500_000_000_000), Concurrency: 4},
		FundERC20Gas: OperationLimit{SingleAmount: big.NewInt(6_000_000_000_000_000), DailyAmount: big.NewInt(1_000_000_000_000_000_000), Concurrency: 1},
		SweepERC20:   OperationLimit{SingleAmount: big.NewInt(100_000_000_000), DailyAmount: big.NewInt(500_000_000_000), Concurrency: 4},
	}
}

func testServiceKeySet(t *testing.T) (*custody.KeySet, custody.KeySetConfig) {
	t.Helper()
	directory := t.TempDir()
	gasKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	items := []struct {
		role         custody.KeyRole
		network      string
		materialType custody.MaterialType
		material     []byte
		keyID        string
	}{
		{custody.KeyRoleTRONRecharge, "tron-nile", custody.MaterialBIP32Seed, []byte("0123456789abcdef0123456789abcdef"), "00112233445566778899aabbccddeeff"},
		{custody.KeyRoleEthereumRecharge, "ethereum-sepolia", custody.MaterialBIP32Seed, []byte("abcdef0123456789abcdef0123456789"), "11112233445566778899aabbccddeeff"},
		{custody.KeyRoleEthereumGas, "ethereum-sepolia", custody.MaterialSecp256k1PrivateKey, crypto.FromECDSA(gasKey), "22112233445566778899aabbccddeeff"},
	}
	configs := make([]custody.CarrierConfig, 0, 3)
	unsealer := serviceTestUnsealer{}
	for _, item := range items {
		dataKey := make([]byte, custody.DataKeySize)
		_, err := rand.Read(dataKey)
		require.NoError(t, err)
		unsealer[item.keyID] = dataKey
		encrypted, err := custody.EncryptKeyMaterial(dataKey, custody.EnvelopeSpec{
			KeyID: item.keyID, Role: item.role, Network: item.network, MaterialType: item.materialType,
		}, item.material, time.Now())
		require.NoError(t, err)
		sealed := custody.SealedKey{
			Version: custody.SealedKeyVersion, KeyID: item.keyID, PCRs: []int{7}, PCRDigest: make([]byte, 32),
			PublicBlob: []byte{1}, PrivateBlob: []byte{2}, CreatedAt: time.Now(),
		}
		sealedPath := filepath.Join(directory, string(item.role)+"-sealed.json")
		encryptedPath := filepath.Join(directory, string(item.role)+"-encrypted.json")
		writeServiceJSON(t, sealedPath, sealed)
		writeServiceJSON(t, encryptedPath, encrypted)
		configs = append(configs, custody.CarrierConfig{
			SealedKeyFile: sealedPath, EncryptedKeyFile: encryptedPath, KeyID: item.keyID,
			Role: item.role, Network: item.network, MaterialType: item.materialType,
			WalletFingerprint: encrypted.WalletFingerprint,
		})
	}
	config := custody.KeySetConfig{TRONRecharge: configs[0], EthereumRecharge: configs[1], EthereumGas: configs[2]}
	keySet, err := custody.LoadKeySet(config, unsealer)
	require.NoError(t, err)
	t.Cleanup(keySet.Close)
	return keySet, config
}

func writeServiceJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, payload, 0o600))
}
