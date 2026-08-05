package onchain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type javaTronHealthFixture struct {
	p2pVersion int64
	latest     int64
	solid      int64
	decimals   uint8
}

func newJavaTronHealthServer(t *testing.T, fixture javaTronHealthFixture) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/wallet/getnodeinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"configNodeInfo": map[string]any{"p2pVersion": fixture.p2pVersion},
			})
		case "/wallet/getnowblock":
			writeJavaTronTestBlock(t, w, fixture.latest)
		case "/walletsolidity/getnowblock":
			writeJavaTronTestBlock(t, w, fixture.solid)
		case "/wallet/triggerconstantcontract":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result":          map[string]any{"result": true},
				"constant_result": []string{fmt.Sprintf("%064x", fixture.decimals)},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeJavaTronTestBlock(t *testing.T, w http.ResponseWriter, height int64) {
	t.Helper()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"blockID": fmt.Sprintf("%064x", height),
		"block_header": map[string]any{
			"raw_data": map[string]any{"number": height, "timestamp": 1700000000000},
		},
	})
}

func checkJavaTronHealth(t *testing.T, fixture javaTronHealthFixture, mutate func(*TRONHealthCheckOptions)) (TRONHealthReport, error) {
	t.Helper()
	server := newJavaTronHealthServer(t, fixture)
	client := newJavaTronTestClient(t, server.URL, server.URL, nil)
	options := TRONHealthCheckOptions{
		Network:          NetworkTronMainnet,
		USDTContract:     TronMainnetUSDTContract,
		ContractCaller:   TronMainnetUSDTContract,
		ExpectedDecimals: USDTDecimals,
		MaxBlockLag:      10,
	}
	if mutate != nil {
		mutate(&options)
	}
	return client.CheckTRONHealth(context.Background(), options)
}

func TestJavaTronHealthCheckSuccess(t *testing.T) {
	report, err := checkJavaTronHealth(t, javaTronHealthFixture{
		p2pVersion: TronMainnetP2PVersion,
		latest:     105,
		solid:      100,
		decimals:   USDTDecimals,
	}, nil)
	require.NoError(t, err)
	require.True(t, report.Healthy)
	require.Equal(t, int64(5), report.BlockLag)
	require.Equal(t, uint8(6), report.ContractDecimals)
	require.NotEmpty(t, report.FullNodeBlockID)
	require.NotEmpty(t, report.SolidBlockID)
}

func TestJavaTronHealthCheckRejectsWrongNetwork(t *testing.T) {
	report, err := checkJavaTronHealth(t, javaTronHealthFixture{
		p2pVersion: TronNileP2PVersion,
		latest:     105,
		solid:      100,
		decimals:   USDTDecimals,
	}, nil)
	require.False(t, report.Healthy)
	var healthErr *TRONHealthError
	require.ErrorAs(t, err, &healthErr)
	require.Equal(t, TRONHealthNetworkMismatch, healthErr.Code)
}

func TestJavaTronHealthCheckRejectsWrongMainnetContract(t *testing.T) {
	report, err := checkJavaTronHealth(t, javaTronHealthFixture{
		p2pVersion: TronMainnetP2PVersion,
		latest:     105,
		solid:      100,
		decimals:   USDTDecimals,
	}, func(options *TRONHealthCheckOptions) {
		options.USDTContract = "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"
	})
	require.False(t, report.Healthy)
	var healthErr *TRONHealthError
	require.ErrorAs(t, err, &healthErr)
	require.Equal(t, TRONHealthContractMismatch, healthErr.Code)
}

func TestJavaTronHealthCheckRejectsLagAndDecimals(t *testing.T) {
	t.Run("lag", func(t *testing.T) {
		report, err := checkJavaTronHealth(t, javaTronHealthFixture{
			p2pVersion: TronMainnetP2PVersion,
			latest:     120,
			solid:      100,
			decimals:   USDTDecimals,
		}, nil)
		require.False(t, report.Healthy)
		var healthErr *TRONHealthError
		require.ErrorAs(t, err, &healthErr)
		require.Equal(t, TRONHealthScanLag, healthErr.Code)
	})

	t.Run("decimals", func(t *testing.T) {
		report, err := checkJavaTronHealth(t, javaTronHealthFixture{
			p2pVersion: TronMainnetP2PVersion,
			latest:     105,
			solid:      100,
			decimals:   18,
		}, nil)
		require.False(t, report.Healthy)
		var healthErr *TRONHealthError
		require.ErrorAs(t, err, &healthErr)
		require.Equal(t, TRONHealthDecimalsMismatch, healthErr.Code)
	})
}
