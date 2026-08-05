package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/stretchr/testify/require"
)

func ethereumStartupServer(t *testing.T, chainID string) *httptest.Server {
	t.Helper()
	blockHash := "0x" + strings.Repeat("22", 32)
	decimals := "0x" + strings.Repeat("00", 31) + "06"
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&call))
		results := map[string]any{
			"eth_chainId": chainID, "eth_syncing": false, "eth_blockNumber": "0x64",
			"eth_getBlockByNumber": map[string]any{"number": "0x62", "hash": blockHash},
			"eth_getCode":          "0x6001", "eth_call": decimals,
		}
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0", "id": json.RawMessage(call.ID), "result": results[call.Method],
		}))
	}))
}

func ethereumStartupTestConfig(primary, backup string) config.SelfHostedEthereumConfig {
	return config.SelfHostedEthereumConfig{
		Enabled: true, Network: string(onchain.NetworkEthereumSepolia), ChainID: onchain.EthereumSepoliaChainID,
		PrimaryRPCURL: primary, BackupRPCURL: backup,
		USDTContract: "0x0000000000000000000000000000000000000002", USDTDecimals: onchain.USDTDecimals,
		RequestTimeoutSeconds: 1, ResponseMaxBytes: 4096, RPCBatchLimit: 10, MaxFinalizedLag: 8,
	}
}

func TestValidateEthereumOnchainStartupUsesBothConfiguredNodes(t *testing.T) {
	primary := ethereumStartupServer(t, "0xaa36a7")
	defer primary.Close()
	backup := ethereumStartupServer(t, "0xaa36a7")
	defer backup.Close()
	require.NoError(t, validateEthereumOnchainStartup(context.Background(), ethereumStartupTestConfig(primary.URL, backup.URL)))
}

func TestValidateEthereumOnchainStartupFailsOnBackupMismatch(t *testing.T) {
	primary := ethereumStartupServer(t, "0xaa36a7")
	defer primary.Close()
	backup := ethereumStartupServer(t, "0x1")
	defer backup.Close()
	err := validateEthereumOnchainStartup(context.Background(), ethereumStartupTestConfig(primary.URL, backup.URL))
	require.ErrorContains(t, err, "backup Ethereum endpoint chain ID")
}

func TestValidateEthereumOnchainStartupSkipsDisabledNetwork(t *testing.T) {
	require.NoError(t, validateEthereumOnchainStartup(context.Background(), config.SelfHostedEthereumConfig{}))
}
