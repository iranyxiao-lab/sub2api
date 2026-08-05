package main

import (
	"bytes"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/usdtsigner"
	"github.com/Wei-Shaw/sub2api/internal/usdtsigner/custody"
	"github.com/stretchr/testify/require"
)

func TestLoadSignerKeySetUsesAndConsumesEphemeralTestSeed(t *testing.T) {
	cfg := usdtsigner.Config{
		Environment: "test",
		RuntimeMode: usdtsigner.RuntimeModeTestSeed,
		TestSeed:    bytes.Repeat([]byte{0x24}, 32),
		TRONOperation: usdtsigner.TRONOperationConfig{
			Network: "tron-nile",
		},
		EthereumOperation: usdtsigner.EthereumOperationConfig{
			Network: "ethereum-sepolia",
		},
	}
	keys, keyConfig, err := loadSignerKeySet(&cfg)
	require.NoError(t, err)
	t.Cleanup(keys.Close)
	require.Nil(t, cfg.TestSeed)
	require.NoError(t, keyConfig.Validate())

	_, err = keys.Material(custody.KeyRoleTRONRecharge)
	require.NoError(t, err)
	_, err = keys.Material(custody.KeyRoleEthereumRecharge)
	require.NoError(t, err)
	_, err = keys.Material(custody.KeyRoleEthereumGas)
	require.NoError(t, err)
}

func TestLoadSignerKeySetRejectsMockMode(t *testing.T) {
	cfg := usdtsigner.Config{RuntimeMode: usdtsigner.RuntimeModeMock}
	_, _, err := loadSignerKeySet(&cfg)
	require.ErrorContains(t, err, "does not use signer key carriers")
}
