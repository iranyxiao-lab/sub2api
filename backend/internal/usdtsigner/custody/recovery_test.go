package custody

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecoveryRequiresTwoDistinctCurrentApprovers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	seed := []byte("0123456789abcdef0123456789abcdef")
	fingerprint, err := WalletFingerprint(KeyRoleTRONRecharge, MaterialBIP32Seed, seed)
	require.NoError(t, err)
	authorization := RecoveryAuthorization{
		IncidentID: "SEC-1234", ApproverIDs: []string{"ops:alice", "security:bob"},
		Role: KeyRoleTRONRecharge, WalletFingerprint: fingerprint,
		IsolatedHostAttestation: "sha256:isolated-host-evidence", ApprovedAt: now.Add(-time.Hour),
	}
	envelope, record, err := RecoverEncryptedKey(randomBytes(t, DataKeySize), authorization, EnvelopeSpec{
		KeyID: "00112233445566778899aabbccddeeff", Role: KeyRoleTRONRecharge,
		Network: "tron-mainnet", MaterialType: MaterialBIP32Seed,
	}, seed, now)
	require.NoError(t, err)
	require.Equal(t, envelope.KeyID, record.RecoveredKeyID)
	require.Equal(t, authorization.ApproverIDs, record.ApproverIDs)
	require.True(t, validFingerprint(record.EncryptedCarrierDigest))

	authorization.ApproverIDs = []string{"ops:alice", "OPS:ALICE"}
	_, _, err = RecoverEncryptedKey(randomBytes(t, DataKeySize), authorization, EnvelopeSpec{
		KeyID: "00112233445566778899aabbccddeeff", Role: KeyRoleTRONRecharge,
		Network: "tron-mainnet", MaterialType: MaterialBIP32Seed,
	}, seed, now)
	require.ErrorContains(t, err, "two distinct")
}
