package custody

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewEphemeralTestKeySetDerivesIndependentDeterministicRoles(t *testing.T) {
	seed := bytes.Repeat([]byte{0x42}, 32)
	keys, config, err := NewEphemeralTestKeySet(seed, "tron-nile", "ethereum-sepolia")
	require.NoError(t, err)
	t.Cleanup(keys.Close)
	require.NoError(t, config.Validate())

	tron, err := keys.Material(KeyRoleTRONRecharge)
	require.NoError(t, err)
	ethereum, err := keys.Material(KeyRoleEthereumRecharge)
	require.NoError(t, err)
	gas, err := keys.Material(KeyRoleEthereumGas)
	require.NoError(t, err)
	require.Len(t, tron, 64)
	require.Len(t, ethereum, 64)
	require.Len(t, gas, 32)
	require.NotEqual(t, tron, ethereum)
	require.NotEqual(t, tron[:32], gas)
	require.NotEqual(t, ethereum[:32], gas)

	again, againConfig, err := NewEphemeralTestKeySet(seed, "tron-nile", "ethereum-sepolia")
	require.NoError(t, err)
	t.Cleanup(again.Close)
	for _, role := range []KeyRole{KeyRoleTRONRecharge, KeyRoleEthereumRecharge, KeyRoleEthereumGas} {
		firstMaterial, materialErr := keys.Material(role)
		require.NoError(t, materialErr)
		secondMaterial, materialErr := again.Material(role)
		require.NoError(t, materialErr)
		require.Equal(t, firstMaterial, secondMaterial)
		firstFingerprint, ok := keys.Fingerprint(role)
		require.True(t, ok)
		secondFingerprint, ok := again.Fingerprint(role)
		require.True(t, ok)
		require.Equal(t, firstFingerprint, secondFingerprint)
	}
	require.Equal(t, config, againConfig)
}

func TestNewEphemeralTestKeySetRejectsInvalidSeedAndNetworks(t *testing.T) {
	_, _, err := NewEphemeralTestKeySet(make([]byte, 31), "tron-nile", "ethereum-sepolia")
	require.ErrorContains(t, err, "exactly 32 bytes")
	_, _, err = NewEphemeralTestKeySet(make([]byte, 32), "ethereum-sepolia", "tron-nile")
	require.ErrorContains(t, err, "explicit TRON and Ethereum networks")
}
