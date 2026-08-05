package onchain

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJavaTronClientReadsSolidifiedTransactionStatus(t *testing.T) {
	transactionID := fmt.Sprintf("%064x", 10)
	blockHash := fmt.Sprintf("%064x", 11)
	parentHash := fmt.Sprintf("%064x", 9)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case javaTronSolidifiedTransactionInfoEndpoint:
			_, _ = fmt.Fprintf(w, `{"id":%q,"blockNumber":42,"blockTimeStamp":1700000000000,"result":"SUCCESS","receipt":{"result":"SUCCESS","energy_usage_total":64000,"net_usage":345},"fee":1200000,"contractResult":["01"],"log":[]}`, transactionID)
		case javaTronSolidifiedBlockEndpoint:
			_, _ = fmt.Fprintf(w, `{"blockID":%q,"block_header":{"raw_data":{"number":42,"timestamp":1700000000000,"parentHash":%q}},"transactions":[{"txID":%q}]}`, blockHash, parentHash, transactionID)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)

	status, err := client.SolidifiedTRONTransaction(context.Background(), transactionID)
	require.NoError(t, err)
	require.True(t, status.Found)
	require.True(t, status.Success)
	require.Equal(t, int64(42), status.BlockHeight)
	require.Equal(t, blockHash, status.BlockHash)
	require.Equal(t, int64(1200000), status.FeeSun)
	require.Equal(t, int64(64000), status.EnergyUsed)
	require.Equal(t, int64(345), status.BandwidthUsed)
}

func TestJavaTronClientTreatsEmptySolidifiedTransactionAsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)
	status, err := client.SolidifiedTRONTransaction(context.Background(), fmt.Sprintf("%064x", 10))
	require.NoError(t, err)
	require.False(t, status.Found)
}
