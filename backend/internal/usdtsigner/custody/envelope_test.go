package custody

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestEncryptedKeyRoundTripAuthenticatesIdentityAndFingerprint(t *testing.T) {
	dataKey := randomBytes(t, DataKeySize)
	seed := []byte("0123456789abcdef0123456789abcdef")
	spec := EnvelopeSpec{
		KeyID: "00112233445566778899aabbccddeeff", Role: KeyRoleTRONRecharge,
		Network: "tron-mainnet", MaterialType: MaterialBIP32Seed,
	}
	envelope, err := EncryptKeyMaterial(dataKey, spec, seed, time.Unix(1_700_000_000, 0))
	require.NoError(t, err)
	require.NoError(t, envelope.Validate())
	require.NotContains(t, envelope.Ciphertext, seed)

	plaintext, err := DecryptKeyMaterial(dataKey, *envelope, ExpectedKey{
		KeyID: spec.KeyID, Role: spec.Role, Network: spec.Network,
		MaterialType: spec.MaterialType, WalletFingerprint: envelope.WalletFingerprint,
	})
	require.NoError(t, err)
	require.Equal(t, seed, plaintext)
	zero(plaintext)
}

func TestEncryptedKeyRejectsMetadataTamperingAndRoleCrossover(t *testing.T) {
	dataKey := randomBytes(t, DataKeySize)
	seed := []byte("0123456789abcdef0123456789abcdef")
	spec := EnvelopeSpec{
		KeyID: "00112233445566778899aabbccddeeff", Role: KeyRoleTRONRecharge,
		Network: "tron-mainnet", MaterialType: MaterialBIP32Seed,
	}
	envelope, err := EncryptKeyMaterial(dataKey, spec, seed, time.Now())
	require.NoError(t, err)

	_, err = DecryptKeyMaterial(dataKey, *envelope, ExpectedKey{
		KeyID: spec.KeyID, Role: KeyRoleEthereumRecharge, Network: "ethereum-mainnet",
		MaterialType: MaterialBIP32Seed, WalletFingerprint: envelope.WalletFingerprint,
	})
	require.ErrorContains(t, err, "identity does not match")

	tampered := *envelope
	tampered.Network = "tron-nile"
	_, err = DecryptKeyMaterial(dataKey, tampered, ExpectedKey{
		KeyID: spec.KeyID, Role: spec.Role, Network: tampered.Network,
		MaterialType: spec.MaterialType, WalletFingerprint: envelope.WalletFingerprint,
	})
	require.ErrorContains(t, err, "authenticate encrypted key carrier")
}

func TestWalletFingerprintUsesPublicIdentity(t *testing.T) {
	tronSeed := []byte("0123456789abcdef0123456789abcdef")
	ethereumSeed := []byte("fedcba9876543210fedcba9876543210")
	tronFingerprint, err := WalletFingerprint(KeyRoleTRONRecharge, MaterialBIP32Seed, tronSeed)
	require.NoError(t, err)
	ethereumFingerprint, err := WalletFingerprint(KeyRoleEthereumRecharge, MaterialBIP32Seed, ethereumSeed)
	require.NoError(t, err)
	require.NotEqual(t, tronFingerprint, ethereumFingerprint)
	require.True(t, validFingerprint(tronFingerprint))

	gasKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	gasFingerprint, err := WalletFingerprint(KeyRoleEthereumGas, MaterialSecp256k1PrivateKey, crypto.FromECDSA(gasKey))
	require.NoError(t, err)
	require.True(t, validFingerprint(gasFingerprint))
	_, err = WalletFingerprint(KeyRoleTRONRecharge, MaterialSecp256k1PrivateKey, crypto.FromECDSA(gasKey))
	require.Error(t, err)
}

func randomBytes(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	_, err := rand.Read(value)
	require.NoError(t, err)
	return value
}
