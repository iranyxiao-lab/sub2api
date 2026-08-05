package onchain

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

const javaTronAccountTestAddress = "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"

func TestJavaTronClientReadsAccountBalancesAndResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, javaTronAccountTestAddress, request["address"])
		require.Equal(t, true, request["visible"])
		switch r.URL.Path {
		case "/wallet/getaccount":
			_, _ = w.Write([]byte(`{"balance":125000000}`))
		case "/wallet/getaccountresource":
			_, _ = w.Write([]byte(`{"EnergyLimit":150000,"EnergyUsed":20000,"freeNetLimit":600,"freeNetUsed":100,"NetLimit":300,"NetUsed":50}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)

	state, err := client.TRONAccountState(context.Background(), javaTronAccountTestAddress)
	require.NoError(t, err)
	require.Equal(t, int64(125000000), state.TRXBalanceSun)
	require.Equal(t, int64(130000), state.AvailableEnergy())
	require.Equal(t, int64(750), state.AvailableBandwidth())
}

func TestJavaTronClientReadsTRC20Balance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/wallet/triggerconstantcontract", r.URL.Path)
		var request map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, "balanceOf(address)", request["function_selector"])
		require.Equal(t, javaTronAccountTestAddress, request["owner_address"])
		require.Len(t, request["parameter"], 64)
		_, _ = w.Write([]byte(`{"result":{"result":true},"constant_result":["0000000000000000000000000000000000000000000000000000000002faf080"]}`))
	}))
	t.Cleanup(server.Close)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)

	balance, err := client.TRC20Balance(context.Background(), TronMainnetUSDTContract, javaTronAccountTestAddress)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(50_000_000), balance)
}

func TestJavaTronClientReadsSolidifiedTRC20BalanceFromSolidityNode(t *testing.T) {
	fullNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "full node must not be queried", http.StatusInternalServerError)
	}))
	t.Cleanup(fullNode.Close)
	solidityNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/walletsolidity/triggerconstantcontract", r.URL.Path)
		_, _ = w.Write([]byte(`{"result":{"result":true},"constant_result":["0000000000000000000000000000000000000000000000000000000002faf080"]}`))
	}))
	t.Cleanup(solidityNode.Close)
	client := newJavaTronTestClient(t, fullNode.URL, solidityNode.URL, nil)

	balance, err := client.SolidifiedTRC20Balance(context.Background(), TronMainnetUSDTContract, javaTronAccountTestAddress)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(50_000_000), balance)
}

func TestJavaTronClientFailsClosedOnInvalidAccountResponses(t *testing.T) {
	t.Run("negative resource", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/wallet/getaccount" {
				_, _ = w.Write([]byte(`{"balance":1}`))
				return
			}
			_, _ = w.Write([]byte(`{"EnergyLimit":-1}`))
		}))
		t.Cleanup(server.Close)
		client := newJavaTronTestClient(t, server.URL, server.URL, nil)
		_, err := client.TRONAccountState(context.Background(), javaTronAccountTestAddress)
		var structured *JavaTronError
		require.ErrorAs(t, err, &structured)
		require.Equal(t, JavaTronErrorInvalidResponse, structured.Kind)
	})

	t.Run("lite node closed API body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("API is closed because this node is a lite fullnode"))
		}))
		t.Cleanup(server.Close)
		client := newJavaTronTestClient(t, server.URL, server.URL, nil)
		_, err := client.TRC20Balance(context.Background(), TronMainnetUSDTContract, javaTronAccountTestAddress)
		var structured *JavaTronError
		require.True(t, errors.As(err, &structured))
		require.Equal(t, JavaTronErrorDecode, structured.Kind)
	})
}
