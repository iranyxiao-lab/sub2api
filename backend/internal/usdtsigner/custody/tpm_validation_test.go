package custody

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSealedKeyValidationRejectsMalformedPolicy(t *testing.T) {
	sealed := SealedKey{Version: SealedKeyVersion}
	require.Error(t, sealed.Validate())
	require.Error(t, (&SealedKey{Version: SealedKeyVersion, KeyID: "00", PCRs: []int{24}}).Validate())
}

func TestPCRSelectionNormalizesAndRejectsUnsafeIndexes(t *testing.T) {
	_, normalized, err := pcrSelection([]int{7, 0, 2})
	require.NoError(t, err)
	require.Equal(t, []int{0, 2, 7}, normalized)

	_, _, err = pcrSelection([]int{7, 7})
	require.ErrorContains(t, err, "duplicated")
	_, _, err = pcrSelection([]int{24})
	require.ErrorContains(t, err, "outside")
}
