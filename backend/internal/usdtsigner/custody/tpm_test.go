//go:build tpm_simulator && cgo

package custody

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpm2/transport/simulator"
	"github.com/stretchr/testify/require"
)

func TestSealAndUnsealDataKeyWithPCRPolicy(t *testing.T) {
	theTPM, err := simulator.OpenSimulator()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, theTPM.Close()) })

	dataKey, sealed, err := SealNewDataKey(theTPM, []int{7}, time.Unix(1_700_000_000, 0))
	require.NoError(t, err)
	require.Len(t, dataKey, DataKeySize)
	require.NoError(t, sealed.Validate())
	require.NotContains(t, sealed.PublicBlob, dataKey)
	require.NotContains(t, sealed.PrivateBlob, dataKey)

	unsealed, err := UnsealDataKey(theTPM, *sealed)
	require.NoError(t, err)
	require.True(t, bytes.Equal(dataKey, unsealed))

	_, err = (tpm2.PCREvent{
		PCRHandle: tpm2.TPMHandle(7),
		EventData: tpm2.TPM2BEvent{Buffer: []byte("change measured boot state")},
	}).Execute(theTPM)
	require.NoError(t, err)
	_, err = UnsealDataKey(theTPM, *sealed)
	require.ErrorContains(t, err, "PCR state does not match")
}

func TestSealedKeysAreIndependent(t *testing.T) {
	theTPM, err := simulator.OpenSimulator()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, theTPM.Close()) })

	firstKey, first, err := SealNewDataKey(theTPM, []int{7}, time.Now())
	require.NoError(t, err)
	secondKey, second, err := SealNewDataKey(theTPM, []int{7}, time.Now())
	require.NoError(t, err)
	require.NotEqual(t, first.KeyID, second.KeyID)
	require.False(t, bytes.Equal(firstKey, secondKey))
	require.False(t, bytes.Equal(first.PrivateBlob, second.PrivateBlob))
}
