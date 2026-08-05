package custody

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

const ephemeralTestSeedSize = 32

// NewEphemeralTestKeySet derives role-separated in-memory keys for explicit
// non-production test_seed mode. The caller remains responsible for enforcing
// the environment boundary and zeroing the input seed.
func NewEphemeralTestKeySet(seed []byte, tronNetwork, ethereumNetwork string) (*KeySet, KeySetConfig, error) {
	if len(seed) != ephemeralTestSeedSize {
		return nil, KeySetConfig{}, fmt.Errorf("ephemeral test seed must be exactly %d bytes", ephemeralTestSeedSize)
	}
	tronNetwork = strings.TrimSpace(tronNetwork)
	ethereumNetwork = strings.TrimSpace(ethereumNetwork)
	if !strings.HasPrefix(tronNetwork, "tron-") || !strings.HasPrefix(ethereumNetwork, "ethereum-") {
		return nil, KeySetConfig{}, fmt.Errorf("ephemeral test keys require explicit TRON and Ethereum networks")
	}

	tronSeed := deriveTestMaterial(seed, "sub2api/usdt-signer/test/tron-recharge", sha512.Size)
	ethereumSeed := deriveTestMaterial(seed, "sub2api/usdt-signer/test/ethereum-recharge", sha512.Size)
	gasKey, err := deriveValidTestPrivateKey(seed)
	if err != nil {
		zero(tronSeed)
		zero(ethereumSeed)
		return nil, KeySetConfig{}, err
	}

	materials := map[KeyRole][]byte{
		KeyRoleTRONRecharge:     tronSeed,
		KeyRoleEthereumRecharge: ethereumSeed,
		KeyRoleEthereumGas:      gasKey,
	}
	fingerprints := make(map[KeyRole]string, len(materials))
	for role, material := range materials {
		materialType := MaterialBIP32Seed
		if role == KeyRoleEthereumGas {
			materialType = MaterialSecp256k1PrivateKey
		}
		fingerprint, fingerprintErr := WalletFingerprint(role, materialType, material)
		if fingerprintErr != nil {
			for _, value := range materials {
				zero(value)
			}
			return nil, KeySetConfig{}, fingerprintErr
		}
		fingerprints[role] = fingerprint
	}

	config := KeySetConfig{
		TRONRecharge: ephemeralTestCarrier(KeyRoleTRONRecharge, tronNetwork, MaterialBIP32Seed, fingerprints[KeyRoleTRONRecharge]),
		EthereumRecharge: ephemeralTestCarrier(
			KeyRoleEthereumRecharge, ethereumNetwork, MaterialBIP32Seed, fingerprints[KeyRoleEthereumRecharge],
		),
		EthereumGas: ephemeralTestCarrier(
			KeyRoleEthereumGas, ethereumNetwork, MaterialSecp256k1PrivateKey, fingerprints[KeyRoleEthereumGas],
		),
	}
	if err := config.Validate(); err != nil {
		for _, value := range materials {
			zero(value)
		}
		return nil, KeySetConfig{}, err
	}
	return &KeySet{materials: materials, fingerprints: fingerprints}, config, nil
}

func ephemeralTestCarrier(role KeyRole, network string, materialType MaterialType, fingerprint string) CarrierConfig {
	digest := sha256.Sum256([]byte("sub2api/usdt-signer/test/key-id/" + string(role)))
	name := string(role)
	return CarrierConfig{
		SealedKeyFile:     "ephemeral-test://" + name + "/sealed",
		EncryptedKeyFile:  "ephemeral-test://" + name + "/encrypted",
		KeyID:             hex.EncodeToString(digest[:16]),
		Role:              role,
		Network:           network,
		MaterialType:      materialType,
		WalletFingerprint: fingerprint,
	}
}

func deriveTestMaterial(seed []byte, label string, size int) []byte {
	var result []byte
	for counter := byte(1); len(result) < size; counter++ {
		mac := hmac.New(sha512.New, seed)
		_, _ = mac.Write([]byte(label))
		_, _ = mac.Write([]byte{counter})
		result = append(result, mac.Sum(nil)...)
	}
	return result[:size]
}

func deriveValidTestPrivateKey(seed []byte) ([]byte, error) {
	for counter := byte(1); counter != 0; counter++ {
		mac := hmac.New(sha256.New, seed)
		_, _ = mac.Write([]byte("sub2api/usdt-signer/test/ethereum-gas"))
		_, _ = mac.Write([]byte{counter})
		candidate := mac.Sum(nil)
		if _, err := crypto.ToECDSA(candidate); err == nil {
			return candidate, nil
		}
		zero(candidate)
	}
	return nil, fmt.Errorf("failed to derive valid ephemeral Ethereum gas key")
}
