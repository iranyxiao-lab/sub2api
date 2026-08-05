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

	"github.com/stretchr/testify/require"
)

const (
	javaTronTestBlockHash  = "0000000000000064AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	javaTronTestParentHash = "0000000000000063BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	javaTronTestTxID       = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
)

func TestJavaTronClientReadsAndNormalizesSolidifiedBlockData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case javaTronSolidifiedNowBlockEndpoint:
			writeJavaTronSolidifiedBlockFixture(t, w, 101, strings.Repeat("d", 64), javaTronTestBlockHash, nil)
		case javaTronSolidifiedBlockEndpoint:
			requireJavaTronBlockHeightRequest(t, r, 100)
			writeJavaTronSolidifiedBlockFixture(t, w, 100, javaTronTestBlockHash, javaTronTestParentHash, []string{javaTronTestTxID})
		case javaTronSolidifiedReceiptsEndpoint:
			requireJavaTronBlockHeightRequest(t, r, 100)
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":             javaTronTestTxID,
					"blockNumber":    int64(100),
					"blockTimeStamp": int64(1700000000123),
					"result":         "failed",
					"contractResult": []string{"", "ABCD"},
					"receipt":        map[string]any{"result": "success"},
					"log": []map[string]any{
						{
							"address": strings.Repeat("A1", 20),
							"topics":  []string{strings.Repeat("B2", 32)},
							"data":    "CAFE",
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, func(options *JavaTronClientOptions) {
		options.ResponseMaxBytes = 4096
	})

	height, err := client.LatestSolidifiedHeight(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(101), height)

	block, err := client.SolidifiedBlockByHeight(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, int64(100), block.Height)
	require.Equal(t, strings.ToLower(javaTronTestBlockHash), block.Hash)
	require.Equal(t, strings.ToLower(javaTronTestParentHash), block.ParentHash)
	require.Equal(t, time.UnixMilli(1700000000123).UTC(), block.Timestamp)
	require.Equal(t, []string{strings.ToLower(javaTronTestTxID)}, block.TransactionIDs)

	receipts, err := client.SolidifiedTransactionReceiptsByBlockHeight(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	receipt := receipts[0]
	require.Equal(t, strings.ToLower(javaTronTestTxID), receipt.TransactionID)
	require.Equal(t, int64(100), receipt.BlockHeight)
	require.Equal(t, time.UnixMilli(1700000000123).UTC(), receipt.BlockTimestamp)
	require.Equal(t, "FAILED", receipt.Result)
	require.Equal(t, "SUCCESS", receipt.ReceiptResult)
	require.Equal(t, []string{"", "abcd"}, receipt.ContractResult)
	require.Equal(t, TRONTransactionLog{
		Index:           0,
		ContractAddress: strings.Repeat("a1", 20),
		Topics:          []string{strings.Repeat("b2", 32)},
		Data:            "cafe",
	}, receipt.Logs[0])
}

func TestJavaTronClientReturnsEmptySolidifiedBlockReceipts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, javaTronSolidifiedReceiptsEndpoint, r.URL.Path)
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)

	receipts, err := client.SolidifiedTransactionReceiptsByBlockHeight(context.Background(), 100)
	require.NoError(t, err)
	require.NotNil(t, receipts)
	require.Empty(t, receipts)
}

func TestJavaTronClientRejectsInvalidSolidifiedBlockResponse(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]any
	}{
		{
			name:     "height mismatch",
			response: javaTronSolidifiedBlockFixture(99, javaTronTestBlockHash, javaTronTestParentHash, nil),
		},
		{
			name:     "malformed block hash",
			response: javaTronSolidifiedBlockFixture(100, "not-hex", javaTronTestParentHash, nil),
		},
		{
			name: "missing parent hash",
			response: javaTronSolidifiedBlockFixture(
				100,
				javaTronTestBlockHash,
				"",
				nil,
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(test.response)
			}))
			t.Cleanup(server.Close)
			client := newJavaTronTestClient(t, server.URL, server.URL, nil)

			_, err := client.SolidifiedBlockByHeight(context.Background(), 100)
			var structured *JavaTronError
			require.ErrorAs(t, err, &structured)
			require.Equal(t, JavaTronErrorInvalidResponse, structured.Kind)
			require.Equal(t, JavaTronSolidityNode, structured.Node)
			require.Equal(t, javaTronSolidifiedBlockEndpoint, structured.Endpoint)
		})
	}
}

func TestJavaTronClientRejectsInvalidSolidifiedReceiptResponse(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		entries int
	}{
		{
			name: "height mismatch",
			mutate: func(receipt map[string]any) {
				receipt["blockNumber"] = int64(99)
			},
			entries: 1,
		},
		{
			name: "invalid log address",
			mutate: func(receipt map[string]any) {
				receipt["log"] = []map[string]any{{"address": "xyz", "topics": []string{}, "data": ""}}
			},
			entries: 1,
		},
		{
			name:    "duplicate transaction info",
			mutate:  func(map[string]any) {},
			entries: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := javaTronTransactionInfoFixture()
			test.mutate(receipt)
			response := make([]map[string]any, test.entries)
			for index := range response {
				response[index] = receipt
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(response)
			}))
			t.Cleanup(server.Close)
			client := newJavaTronTestClient(t, server.URL, server.URL, func(options *JavaTronClientOptions) {
				options.ResponseMaxBytes = 4096
			})

			_, err := client.SolidifiedTransactionReceiptsByBlockHeight(context.Background(), 100)
			var structured *JavaTronError
			require.ErrorAs(t, err, &structured)
			require.Equal(t, JavaTronErrorInvalidResponse, structured.Kind)
			require.Equal(t, javaTronSolidifiedReceiptsEndpoint, structured.Endpoint)
		})
	}
}

func TestJavaTronClientRejectsNegativeSolidifiedBlockHeightWithoutRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)

	_, blockErr := client.SolidifiedBlockByHeight(context.Background(), -1)
	_, receiptErr := client.SolidifiedTransactionReceiptsByBlockHeight(context.Background(), -1)
	require.Error(t, blockErr)
	require.Error(t, receiptErr)
	require.Zero(t, requests.Load())
}

func requireJavaTronBlockHeightRequest(t *testing.T, request *http.Request, height int64) {
	t.Helper()
	require.Equal(t, http.MethodPost, request.Method)
	var payload map[string]int64
	require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
	require.Equal(t, map[string]int64{"num": height}, payload)
}

func writeJavaTronSolidifiedBlockFixture(t *testing.T, writer http.ResponseWriter, height int64, hash, parentHash string, transactionIDs []string) {
	t.Helper()
	_ = json.NewEncoder(writer).Encode(javaTronSolidifiedBlockFixture(height, hash, parentHash, transactionIDs))
}

func javaTronSolidifiedBlockFixture(height int64, hash, parentHash string, transactionIDs []string) map[string]any {
	transactions := make([]map[string]any, len(transactionIDs))
	for index, transactionID := range transactionIDs {
		transactions[index] = map[string]any{"txID": transactionID}
	}
	return map[string]any{
		"blockID": hash,
		"block_header": map[string]any{
			"raw_data": map[string]any{
				"number":     height,
				"timestamp":  int64(1700000000123),
				"parentHash": parentHash,
			},
		},
		"transactions": transactions,
	}
}

func javaTronTransactionInfoFixture() map[string]any {
	return map[string]any{
		"id":             javaTronTestTxID,
		"blockNumber":    int64(100),
		"blockTimeStamp": int64(1700000000123),
		"contractResult": []string{""},
		"receipt":        map[string]any{"result": "SUCCESS"},
		"log": []map[string]any{
			{
				"address": strings.Repeat("a1", 20),
				"topics":  []string{fmt.Sprintf("%064x", 1)},
				"data":    fmt.Sprintf("%064x", 2),
			},
		},
	}
}
