package usdtsigner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

type fakeTRONNode struct {
	version    int64
	balance    *big.Int
	mutate     func(map[string]any)
	broadcasts int
	lastIntent TRONTransferIntent
}

func (f *fakeTRONNode) NetworkVersion(context.Context) (int64, error) {
	return f.version, nil
}

func (f *fakeTRONNode) TRC20Balance(context.Context, string, string) (*big.Int, error) {
	return new(big.Int).Set(f.balance), nil
}

func (f *fakeTRONNode) BuildTRC20Transfer(_ context.Context, intent TRONTransferIntent) (*TRONUnsignedTransaction, error) {
	f.lastIntent = intent
	rawData := []byte{1, 2, 3, 4, 5}
	digest := sha256.Sum256(rawData)
	data, err := tronTransferData(intent.Destination, intent.Amount)
	if err != nil {
		return nil, err
	}
	transaction := map[string]any{
		"txID": hex.EncodeToString(digest[:]), "raw_data_hex": hex.EncodeToString(rawData),
		"raw_data": map[string]any{
			"fee_limit": intent.FeeLimitSun,
			"contract": []any{map[string]any{
				"type": "TriggerSmartContract",
				"parameter": map[string]any{
					"type_url": "type.googleapis.com/protocol.TriggerSmartContract",
					"value": map[string]any{
						"owner_address": intent.OwnerAddress, "contract_address": intent.Contract,
						"data": data, "call_value": 0,
					},
				},
			}},
		},
	}
	if f.mutate != nil {
		f.mutate(transaction)
	}
	payload, err := json.Marshal(transaction)
	if err != nil {
		return nil, err
	}
	return &TRONUnsignedTransaction{
		TransactionID: hex.EncodeToString(digest[:]), RawDataHex: hex.EncodeToString(rawData), RawJSON: payload,
	}, nil
}

func (f *fakeTRONNode) BroadcastTRC20Transfer(_ context.Context, transaction *TRONSignedTransaction) (string, error) {
	f.broadcasts++
	var payload map[string]any
	if err := json.Unmarshal(transaction.RawJSON, &payload); err != nil {
		return "", err
	}
	if len(payload["signature"].([]any)) != 1 {
		return "", nil
	}
	return transaction.TransactionID, nil
}

func TestExecuteTRC20SweepUsesFixedNodeVerifiedSemantics(t *testing.T) {
	config := testTRONOperationConfig()
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	node := &fakeTRONNode{version: onchain.TronNileP2PVersion, balance: big.NewInt(100_000_000)}

	transactionID, err := ExecuteTRC20Sweep(context.Background(), config, node, privateKey, big.NewInt(50_000_000))
	require.NoError(t, err)
	require.Len(t, transactionID, 64)
	require.Equal(t, tronAddressFromPrivateKey(privateKey), node.lastIntent.OwnerAddress)
	require.Equal(t, config.USDTContract, node.lastIntent.Contract)
	require.Equal(t, config.CollectionAddress, node.lastIntent.Destination)
	require.Equal(t, 1, node.broadcasts)
}

func TestExecuteTRC20SweepRejectsNodeSemanticTampering(t *testing.T) {
	config := testTRONOperationConfig()
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	node := &fakeTRONNode{
		version: onchain.TronNileP2PVersion, balance: big.NewInt(100_000_000),
		mutate: func(transaction map[string]any) {
			raw := transaction["raw_data"].(map[string]any)
			contract := raw["contract"].([]any)[0].(map[string]any)
			parameter := contract["parameter"].(map[string]any)
			value := parameter["value"].(map[string]any)
			value["contract_address"] = tronTestAddress(0x77)
		},
	}
	_, err = ExecuteTRC20Sweep(context.Background(), config, node, privateKey, big.NewInt(50_000_000))
	require.ErrorContains(t, err, "token contract")
	require.Zero(t, node.broadcasts)

	node.mutate = nil
	node.version = onchain.TronMainnetP2PVersion
	_, err = ExecuteTRC20Sweep(context.Background(), config, node, privateKey, big.NewInt(50_000_000))
	require.ErrorContains(t, err, "network mismatch")
	require.Zero(t, node.broadcasts)
}

func TestExecuteTRC20SweepRejectsDestinationAndAmountTampering(t *testing.T) {
	config := testTRONOperationConfig()
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	wrongData, err := tronTransferData(tronTestAddress(0x77), big.NewInt(49_000_000))
	require.NoError(t, err)
	node := &fakeTRONNode{
		version: onchain.TronNileP2PVersion, balance: big.NewInt(100_000_000),
		mutate: func(transaction map[string]any) {
			raw := transaction["raw_data"].(map[string]any)
			contract := raw["contract"].([]any)[0].(map[string]any)
			parameter := contract["parameter"].(map[string]any)
			value := parameter["value"].(map[string]any)
			value["data"] = wrongData
		},
	}

	_, err = ExecuteTRC20Sweep(context.Background(), config, node, privateKey, big.NewInt(50_000_000))
	require.ErrorContains(t, err, "transfer destination or amount")
	require.Zero(t, node.broadcasts)
}

func TestExecuteTRC20SweepRejectsInsufficientBalance(t *testing.T) {
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	node := &fakeTRONNode{version: onchain.TronNileP2PVersion, balance: big.NewInt(49_999_999)}
	_, err = ExecuteTRC20Sweep(context.Background(), testTRONOperationConfig(), node, privateKey, big.NewInt(50_000_000))
	require.ErrorContains(t, err, "balance")
	require.Zero(t, node.broadcasts)
}

func testTRONOperationConfig() TRONOperationConfig {
	return TRONOperationConfig{
		Network: onchain.NetworkTronNile, FullNodeURL: "https://tron-node.internal",
		USDTContract: tronTestAddress(0x11), CollectionAddress: tronTestAddress(0x22),
		FeeLimitSun: 100_000_000, Timeout: 5 * time.Second, ResponseMaxBytes: 1 << 20,
	}
}

func tronTestAddress(value byte) string {
	return base58.CheckEncode(bytes.Repeat([]byte{value}, 20), 0x41)
}
