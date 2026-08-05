package onchain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
)

type ethereumRPCTestRequest struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func newEthereumRPCTestClient(t *testing.T, handler http.Handler, mutate func(*EthereumRPCClientOptions)) *EthereumRPCClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	options := EthereumRPCClientOptions{
		Endpoint: server.URL, Timeout: time.Second, ResponseMaxBytes: 4096,
		BatchLimit: 10, MaxRetries: 0,
	}
	if mutate != nil {
		mutate(&options)
	}
	client, err := NewEthereumRPCClient(context.Background(), options)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func ethereumRPCResultHandler(t *testing.T, results map[string]any) http.HandlerFunc {
	t.Helper()
	return func(writer http.ResponseWriter, request *http.Request) {
		var call ethereumRPCTestRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&call))
		result, ok := results[call.Method]
		if !ok {
			http.Error(writer, "unexpected method", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0", "id": json.RawMessage(call.ID), "result": result,
		}))
	}
}

func TestEthereumRPCClientImplementsStartupValidation(t *testing.T) {
	blockHash := "0x" + strings.Repeat("11", 32)
	decimals := "0x" + strings.Repeat("00", 31) + "06"
	client := newEthereumRPCTestClient(t, ethereumRPCResultHandler(t, map[string]any{
		"eth_chainId": "0x1", "eth_syncing": false, "eth_blockNumber": "0x64",
		"eth_getBlockByNumber": map[string]any{"number": "0x62", "hash": blockHash},
		"eth_getCode":          "0x6001", "eth_call": decimals,
	}), nil)

	report, err := ValidateEthereumStartup(context.Background(), client, client, ethereumStartupTestOptions())
	require.NoError(t, err)
	require.Equal(t, uint64(98), report.Primary.Finalized.Number)
}

func TestEthereumRPCClientReadsFinalizedHeadersBoundedLogsAndReceipts(t *testing.T) {
	blockHash := "0x" + strings.Repeat("11", 32)
	transactionHash := "0x" + strings.Repeat("22", 32)
	contract := EthereumMainnetUSDTContract
	transferTopic := "0x" + strings.Repeat("33", 32)
	recipientTopic := "0x" + strings.Repeat("00", 12) + strings.Repeat("44", 20)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var call ethereumRPCTestRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&call))
		var result any
		switch call.Method {
		case "eth_getBlockByNumber":
			require.Len(t, call.Params, 2)
			if string(call.Params[0]) == `"finalized"` {
				result = map[string]any{"number": "0x64", "hash": blockHash}
			} else {
				require.JSONEq(t, `"0x63"`, string(call.Params[0]))
				result = map[string]any{"number": "0x63", "hash": blockHash}
			}
		case "eth_getLogs":
			require.Len(t, call.Params, 1)
			var filter map[string]any
			require.NoError(t, json.Unmarshal(call.Params[0], &filter))
			require.Equal(t, "0x63", filter["fromBlock"])
			require.Equal(t, "0x64", filter["toBlock"])
			require.Equal(t, strings.ToLower(contract), strings.ToLower(filter["address"].(string)))
			result = []map[string]any{{
				"address": contract, "topics": []string{transferTopic, recipientTopic}, "data": "0x01",
				"blockNumber": "0x63", "transactionHash": transactionHash, "transactionIndex": "0x0",
				"blockHash": blockHash, "logIndex": "0x1", "removed": false,
			}}
		case "eth_getTransactionReceipt":
			require.Len(t, call.Params, 1)
			require.Equal(t, strings.ToLower(`"`+transactionHash+`"`), strings.ToLower(string(call.Params[0])))
			result = map[string]any{
				"transactionHash": transactionHash, "blockHash": blockHash,
				"blockNumber": "0x63", "status": "0x1", "logs": []any{},
			}
		case "eth_getTransactionByHash":
			result = map[string]any{"hash": transactionHash, "chainId": "0x1", "from": "0x1111111111111111111111111111111111111111", "to": contract, "nonce": "0x7", "gas": "0x186a0", "value": "0x0", "input": "0xa9059cbb"}
		default:
			t.Fatalf("unexpected RPC method %s", call.Method)
		}
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0", "id": json.RawMessage(call.ID), "result": result,
		}))
	}))
	t.Cleanup(server.Close)
	client := newEthereumRPCTestClient(t, server.Config.Handler, nil)

	finalized, err := client.FinalizedBlock(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(100), finalized.Number)
	header, err := client.BlockByNumber(context.Background(), 99)
	require.NoError(t, err)
	require.Equal(t, uint64(99), header.Number)
	logs, err := client.Logs(context.Background(), 99, 100, contract, nil)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, uint64(99), logs[0].BlockNumber)
	require.Equal(t, uint(1), logs[0].Index)
	receipt, err := client.TransactionReceipt(context.Background(), transactionHash)
	require.NoError(t, err)
	require.Equal(t, uint64(1), receipt.Status)
	require.Equal(t, uint64(99), receipt.BlockNumber)
	transaction, err := client.TransactionByHash(context.Background(), transactionHash)
	require.NoError(t, err)
	require.Equal(t, uint64(1), transaction.ChainID)
	require.Equal(t, uint64(7), transaction.Nonce)
	require.Equal(t, contract, transaction.To)
	require.Equal(t, "0", transaction.ValueWei)
	require.Equal(t, []byte{0xa9, 0x05, 0x9c, 0xbb}, transaction.Input)
}

func TestEthereumRPCClientRejectsInvalidLogAndReceiptRequests(t *testing.T) {
	client := newEthereumRPCTestClient(t, ethereumRPCResultHandler(t, map[string]any{}), nil)
	_, err := client.Logs(context.Background(), 10, 9, EthereumMainnetUSDTContract, nil)
	require.ErrorContains(t, err, "start 10 exceeds end 9")
	_, err = client.Logs(context.Background(), 9, 10, "not-an-address", nil)
	require.ErrorContains(t, err, "contract address is invalid")
	_, err = client.TransactionReceipt(context.Background(), "not-a-hash")
	require.ErrorContains(t, err, "transaction hash is invalid")
	_, err = client.TransactionByHash(context.Background(), "not-a-hash")
	require.ErrorContains(t, err, "transaction hash is invalid")
}

func TestEthereumRPCClientReadsSweepBalancesGasAndFeeSuggestion(t *testing.T) {
	decimals32 := func(value string) string { return "0x" + strings.Repeat("0", 64-len(value)) + value }
	client := newEthereumRPCTestClient(t, ethereumRPCResultHandler(t, map[string]any{
		"eth_getBalance":           "0xde0b6b3a7640000",
		"eth_getTransactionCount":  "0x7",
		"eth_call":                 decimals32("bebc200"),
		"eth_estimateGas":          "0x186a0",
		"eth_maxPriorityFeePerGas": "0x3b9aca00",
		"eth_getBlockByNumber":     map[string]any{"baseFeePerGas": "0x5d21dba00"},
	}), nil)
	source := "0x0000000000000000000000000000000000000002"
	destination := "0x0000000000000000000000000000000000000003"

	ethBalance, err := client.EthereumBalance(context.Background(), source)
	require.NoError(t, err)
	require.Equal(t, "1000000000000000000", ethBalance.String())
	pendingNonce, err := client.PendingNonce(context.Background(), source)
	require.NoError(t, err)
	require.Equal(t, uint64(7), pendingNonce)
	usdtBalance, err := client.ERC20Balance(context.Background(), EthereumMainnetUSDTContract, source)
	require.NoError(t, err)
	require.Equal(t, "200000000", usdtBalance.String())
	gas, err := client.EstimateERC20TransferGas(context.Background(), EthereumMainnetUSDTContract, source, destination, usdtBalance)
	require.NoError(t, err)
	require.Equal(t, uint64(100000), gas)
	maxFee, err := client.SuggestedMaxFeePerGas(context.Background())
	require.NoError(t, err)
	require.Equal(t, "51000000000", maxFee.String())
}

func TestEthereumRPCClientRetriesRetryableHTTPFailures(t *testing.T) {
	var calls atomic.Int32
	client := newEthereumRPCTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(writer, "temporary", http.StatusServiceUnavailable)
			return
		}
		var call ethereumRPCTestRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&call))
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(call.ID), "result": "0x1"}))
	}), func(options *EthereumRPCClientOptions) {
		options.MaxRetries = 1
		options.RetryBackoff = time.Millisecond
	})

	chainID, err := client.ChainID(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), chainID)
	require.Equal(t, int32(2), calls.Load())
}

func TestEthereumRPCClientEnforcesResponseAndBatchLimits(t *testing.T) {
	t.Run("response body", func(t *testing.T) {
		client := newEthereumRPCTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(writer, strings.Repeat("x", 2048))
		}), func(options *EthereumRPCClientOptions) { options.ResponseMaxBytes = 1024 })
		_, err := client.ChainID(context.Background())
		var rpcErr *EthereumRPCError
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, EthereumRPCErrorResponseLimit, rpcErr.Kind)
		require.False(t, rpcErr.Retryable)
	})

	t.Run("batch size", func(t *testing.T) {
		client := newEthereumRPCTestClient(t, ethereumRPCResultHandler(t, map[string]any{"eth_chainId": "0x1"}), func(options *EthereumRPCClientOptions) {
			options.BatchLimit = 1
		})
		err := client.BatchCallContext(context.Background(), []rpc.BatchElem{{Method: "eth_chainId", Result: new(string)}, {Method: "eth_chainId", Result: new(string)}})
		var rpcErr *EthereumRPCError
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, EthereumRPCErrorInvalidRequest, rpcErr.Kind)
	})
}

func TestEthereumRPCClientClassifiesTimeout(t *testing.T) {
	client := newEthereumRPCTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = fmt.Fprint(writer, `{}`)
	}), func(options *EthereumRPCClientOptions) { options.Timeout = 10 * time.Millisecond })
	_, err := client.ChainID(context.Background())
	var rpcErr *EthereumRPCError
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, EthereumRPCErrorTimeout, rpcErr.Kind)
}

func TestCallEthereumWithFailoverUsesBackupOnlyForTransientFailures(t *testing.T) {
	primary := newEthereumRPCTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "temporary", http.StatusServiceUnavailable)
	}), nil)
	backup := newEthereumRPCTestClient(t, ethereumRPCResultHandler(t, map[string]any{"eth_chainId": "0x1"}), nil)
	var chainID uint64
	err := CallEthereumWithFailover(context.Background(), primary, backup, func(ctx context.Context, client *EthereumRPCClient) error {
		value, callErr := client.ChainID(ctx)
		chainID = value
		return callErr
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), chainID)

	backupCalls := 0
	nonRetryable := &EthereumRPCError{Kind: EthereumRPCErrorRemote, Method: "eth_call", Cause: fmt.Errorf("invalid params")}
	err = CallEthereumWithFailover(context.Background(), primary, backup, func(_ context.Context, client *EthereumRPCClient) error {
		if client == primary {
			return nonRetryable
		}
		backupCalls++
		return nil
	})
	require.ErrorIs(t, err, nonRetryable)
	require.Zero(t, backupCalls)
}
