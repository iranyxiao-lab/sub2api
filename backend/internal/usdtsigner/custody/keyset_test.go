package custody

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

type fakeDataKeyUnsealer map[string][]byte

func (f fakeDataKeyUnsealer) Unseal(sealed SealedKey) ([]byte, error) {
	return append([]byte(nil), f[sealed.KeyID]...), nil
}

type failingDataKeyUnsealer struct{}

func (failingDataKeyUnsealer) Unseal(SealedKey) ([]byte, error) {
	return nil, errors.New("TPM PCR policy mismatch")
}

func TestLoadKeySetRequiresIndependentRoleMaterial(t *testing.T) {
	directory := t.TempDir()
	sharedSeed := []byte("0123456789abcdef0123456789abcdef")
	gasPrivate, err := crypto.GenerateKey()
	require.NoError(t, err)
	gasMaterial := crypto.FromECDSA(gasPrivate)

	configs := []struct {
		role         KeyRole
		network      string
		materialType MaterialType
		material     []byte
		keyID        string
	}{
		{KeyRoleTRONRecharge, "tron-mainnet", MaterialBIP32Seed, sharedSeed, "00112233445566778899aabbccddeeff"},
		{KeyRoleEthereumRecharge, "ethereum-mainnet", MaterialBIP32Seed, sharedSeed, "11112233445566778899aabbccddeeff"},
		{KeyRoleEthereumGas, "ethereum-mainnet", MaterialSecp256k1PrivateKey, gasMaterial, "22112233445566778899aabbccddeeff"},
	}
	carriers := make([]CarrierConfig, 0, len(configs))
	unsealer := fakeDataKeyUnsealer{}
	for _, item := range configs {
		dataKey := randomBytes(t, DataKeySize)
		unsealer[item.keyID] = dataKey
		encrypted, err := EncryptKeyMaterial(dataKey, EnvelopeSpec{
			KeyID: item.keyID, Role: item.role, Network: item.network, MaterialType: item.materialType,
		}, item.material, time.Now())
		require.NoError(t, err)
		sealed := validTestSealedKey(item.keyID)
		sealedPath := writeCustodyTestJSON(t, directory, string(item.role)+"-sealed.json", sealed)
		encryptedPath := writeCustodyTestJSON(t, directory, string(item.role)+"-encrypted.json", encrypted)
		carriers = append(carriers, CarrierConfig{
			SealedKeyFile: sealedPath, EncryptedKeyFile: encryptedPath, KeyID: item.keyID,
			Role: item.role, Network: item.network, MaterialType: item.materialType,
			WalletFingerprint: encrypted.WalletFingerprint,
		})
	}

	_, err = LoadKeySet(KeySetConfig{
		TRONRecharge: carriers[0], EthereumRecharge: carriers[1], EthereumGas: carriers[2],
	}, unsealer)
	require.ErrorContains(t, err, "is not independent")

	newSeed := []byte("abcdef0123456789abcdef0123456789")
	dataKey := unsealer[configs[1].keyID]
	replacement, err := EncryptKeyMaterial(dataKey, EnvelopeSpec{
		KeyID: configs[1].keyID, Role: configs[1].role, Network: configs[1].network,
		MaterialType: configs[1].materialType,
	}, newSeed, time.Now())
	require.NoError(t, err)
	writeCustodyTestJSONAt(t, carriers[1].EncryptedKeyFile, replacement)
	carriers[1].WalletFingerprint = replacement.WalletFingerprint

	keySet, err := LoadKeySet(KeySetConfig{
		TRONRecharge: carriers[0], EthereumRecharge: carriers[1], EthereumGas: carriers[2],
	}, unsealer)
	require.NoError(t, err)
	t.Cleanup(keySet.Close)
	material, err := keySet.Material(KeyRoleEthereumRecharge)
	require.NoError(t, err)
	require.Equal(t, newSeed, material)
	zero(material)
}

func TestLoadKeySetRejectsSharedTPMDataKeyAndUnknownJSON(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "unknown.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"unknown":true}`), 0o600))
	var sealed SealedKey
	require.Error(t, readCustodyJSON(path, &sealed))
}

func TestLoadKeySetFailsClosedWhenTPMUnsealFails(t *testing.T) {
	directory := t.TempDir()
	dataKey := randomBytes(t, DataKeySize)
	encrypted, err := EncryptKeyMaterial(dataKey, EnvelopeSpec{
		KeyID: "00112233445566778899aabbccddeeff", Role: KeyRoleTRONRecharge,
		Network: "tron-mainnet", MaterialType: MaterialBIP32Seed,
	}, []byte("0123456789abcdef0123456789abcdef"), time.Now())
	require.NoError(t, err)
	tronCarrier := CarrierConfig{
		SealedKeyFile:    writeCustodyTestJSON(t, directory, "tron-sealed.json", validTestSealedKey("00112233445566778899aabbccddeeff")),
		EncryptedKeyFile: writeCustodyTestJSON(t, directory, "tron-encrypted.json", encrypted),
		KeyID:            "00112233445566778899aabbccddeeff", Role: KeyRoleTRONRecharge,
		Network: "tron-mainnet", MaterialType: MaterialBIP32Seed, WalletFingerprint: encrypted.WalletFingerprint,
	}
	config := KeySetConfig{
		TRONRecharge: tronCarrier,
		EthereumRecharge: CarrierConfig{
			SealedKeyFile: "ethereum-sealed.json", EncryptedKeyFile: "ethereum-encrypted.json",
			KeyID: "11112233445566778899aabbccddeeff", Role: KeyRoleEthereumRecharge,
			Network: "ethereum-mainnet", MaterialType: MaterialBIP32Seed,
			WalletFingerprint: "sha256:11112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		},
		EthereumGas: CarrierConfig{
			SealedKeyFile: "gas-sealed.json", EncryptedKeyFile: "gas-encrypted.json",
			KeyID: "22112233445566778899aabbccddeeff", Role: KeyRoleEthereumGas,
			Network: "ethereum-mainnet", MaterialType: MaterialSecp256k1PrivateKey,
			WalletFingerprint: "sha256:22112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		},
	}

	keys, err := LoadKeySet(config, failingDataKeyUnsealer{})
	require.Nil(t, keys)
	require.ErrorContains(t, err, "unseal tron_recharge data key")
	require.ErrorContains(t, err, "TPM PCR policy mismatch")
}

func validTestSealedKey(keyID string) SealedKey {
	return SealedKey{
		Version: SealedKeyVersion, KeyID: keyID, PCRs: []int{7}, PCRDigest: make([]byte, 32),
		PublicBlob: []byte{1}, PrivateBlob: []byte{2}, CreatedAt: time.Now().UTC(),
	}
}

func writeCustodyTestJSON(t *testing.T, directory, name string, value any) string {
	t.Helper()
	path := filepath.Join(directory, name)
	writeCustodyTestJSONAt(t, path, value)
	return path
}

func writeCustodyTestJSONAt(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, payload, 0o600))
}
