package signerv1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientHealthUsesVersionedHealthRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, HealthPath, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"v1"}`))
	}))
	t.Cleanup(server.Close)

	client := &Client{baseURL: server.URL, httpClient: server.Client()}
	health, err := client.Health(context.Background())
	require.NoError(t, err)
	require.Equal(t, &HealthResponse{Status: "ok", Version: APIVersion}, health)
}

func TestClientHealthRejectsUnhealthyOrMismatchedResponses(t *testing.T) {
	for _, response := range []string{
		`{"status":"degraded","version":"v1"}`,
		`{"status":"ok","version":"v2"}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(response))
			}))
			defer server.Close()
			client := &Client{baseURL: server.URL, httpClient: server.Client()}
			_, err := client.Health(context.Background())
			require.Error(t, err)
		})
	}
}
