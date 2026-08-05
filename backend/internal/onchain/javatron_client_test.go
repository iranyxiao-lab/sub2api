package onchain

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newJavaTronTestClient(t *testing.T, fullNodeURL, solidityNodeURL string, mutate func(*JavaTronClientOptions)) *JavaTronClient {
	t.Helper()
	options := JavaTronClientOptions{
		FullNodeURL:      fullNodeURL,
		SolidityNodeURL:  solidityNodeURL,
		Timeout:          time.Second,
		ResponseMaxBytes: 1024,
		MaxAttempts:      3,
		RetryBackoff:     time.Millisecond,
	}
	if mutate != nil {
		mutate(&options)
	}
	client, err := NewJavaTronClient(options)
	require.NoError(t, err)
	return client
}

func TestJavaTronClientRoutesNodeRolesAndPostsJSON(t *testing.T) {
	fullNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/wallet/getnodeinfo", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		_, _ = w.Write([]byte(`{"node":"full"}`))
	}))
	t.Cleanup(fullNode.Close)
	solidityNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/walletsolidity/getnowblock", r.URL.Path)
		_, _ = w.Write([]byte(`{"node":"solidity"}`))
	}))
	t.Cleanup(solidityNode.Close)

	client := newJavaTronTestClient(t, fullNode.URL, solidityNode.URL, nil)
	var full, solidity map[string]string
	require.NoError(t, client.postJSON(context.Background(), JavaTronFullNode, "/wallet/getnodeinfo", map[string]any{}, &full))
	require.NoError(t, client.postJSON(context.Background(), JavaTronSolidityNode, "/walletsolidity/getnowblock", map[string]any{}, &solidity))
	require.Equal(t, "full", full["node"])
	require.Equal(t, "solidity", solidity["node"])
}

func TestJavaTronClientRetriesRetryableHTTPStatus(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)

	var response struct {
		OK bool `json:"ok"`
	}
	require.NoError(t, client.postJSON(context.Background(), JavaTronFullNode, "/wallet/getnowblock", struct{}{}, &response))
	require.True(t, response.OK)
	require.Equal(t, int32(3), attempts.Load())
}

func TestJavaTronClientDoesNotRetryPermanentHTTPStatus(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)

	err := client.postJSON(context.Background(), JavaTronFullNode, "/wallet/getnowblock", struct{}{}, &struct{}{})
	var structured *JavaTronError
	require.ErrorAs(t, err, &structured)
	require.Equal(t, JavaTronErrorHTTP, structured.Kind)
	require.False(t, structured.Retryable)
	require.Equal(t, http.StatusBadRequest, structured.StatusCode)
	require.Equal(t, int32(1), attempts.Load())
}

func TestJavaTronClientEnforcesResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 65))
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, func(options *JavaTronClientOptions) {
		options.ResponseMaxBytes = 64
	})

	err := client.postJSON(context.Background(), JavaTronSolidityNode, "/walletsolidity/getnowblock", struct{}{}, &struct{}{})
	var structured *JavaTronError
	require.ErrorAs(t, err, &structured)
	require.Equal(t, JavaTronErrorResponseTooLarge, structured.Kind)
	require.False(t, structured.Retryable)
}

func TestJavaTronClientClassifiesTimeout(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, func(options *JavaTronClientOptions) {
		options.Timeout = 5 * time.Millisecond
		options.RetryBackoff = 0
	})

	err := client.postJSON(context.Background(), JavaTronFullNode, "/wallet/getnodeinfo", struct{}{}, &struct{}{})
	var structured *JavaTronError
	require.ErrorAs(t, err, &structured)
	require.Equal(t, JavaTronErrorTimeout, structured.Kind)
	require.True(t, structured.Retryable)
	require.Equal(t, 3, structured.Attempt)
	require.Equal(t, int32(3), attempts.Load())
}

func TestJavaTronClientStopsWhenCallerCancels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.postJSON(ctx, JavaTronFullNode, "/wallet/getnodeinfo", struct{}{}, &struct{}{})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled))
	var structured *JavaTronError
	require.ErrorAs(t, err, &structured)
	require.False(t, structured.Retryable)
}
