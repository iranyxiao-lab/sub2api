package custody

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

const EncryptedKeyVersion = 1

type KeyRole string

const (
	KeyRoleTRONRecharge     KeyRole = "tron_recharge"
	KeyRoleEthereumRecharge KeyRole = "ethereum_recharge"
	KeyRoleEthereumGas      KeyRole = "ethereum_gas_sponsor"
)

type MaterialType string

const (
	MaterialBIP32Seed           MaterialType = "bip32_seed"
	MaterialAccountExtendedKey  MaterialType = "account_extended_private_key"
	MaterialSecp256k1PrivateKey MaterialType = "secp256k1_private_key"
)

type EnvelopeSpec struct {
	KeyID        string
	Role         KeyRole
	Network      string
	MaterialType MaterialType
}

type EncryptedKey struct {
	Version           int          `json:"version"`
	KeyID             string       `json:"key_id"`
	Role              KeyRole      `json:"role"`
	Network           string       `json:"network"`
	MaterialType      MaterialType `json:"material_type"`
	WalletFingerprint string       `json:"wallet_fingerprint"`
	CreatedAt         time.Time    `json:"created_at"`
	Nonce             []byte       `json:"nonce"`
	Ciphertext        []byte       `json:"ciphertext"`
}

type ExpectedKey struct {
	KeyID             string
	Role              KeyRole
	Network           string
	MaterialType      MaterialType
	WalletFingerprint string
}

func EncryptKeyMaterial(dataKey []byte, spec EnvelopeSpec, material []byte, now time.Time) (*EncryptedKey, error) {
	if len(dataKey) != DataKeySize {
		return nil, fmt.Errorf("key carrier data key must be exactly %d bytes", DataKeySize)
	}
	if len(material) == 0 {
		return nil, fmt.Errorf("key material is required")
	}
	if err := validateEnvelopeIdentity(spec.KeyID, spec.Role, spec.Network, spec.MaterialType); err != nil {
		return nil, err
	}
	fingerprint, err := WalletFingerprint(spec.Role, spec.MaterialType, material)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return nil, fmt.Errorf("initialize key carrier cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize key carrier AEAD: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate key carrier nonce: %w", err)
	}
	envelope := &EncryptedKey{
		Version: EncryptedKeyVersion, KeyID: spec.KeyID, Role: spec.Role,
		Network: strings.TrimSpace(spec.Network), MaterialType: spec.MaterialType,
		WalletFingerprint: fingerprint, CreatedAt: now.UTC(), Nonce: nonce,
	}
	aad, err := envelope.additionalData()
	if err != nil {
		return nil, err
	}
	envelope.Ciphertext = aead.Seal(nil, nonce, material, aad)
	return envelope, nil
}

func DecryptKeyMaterial(dataKey []byte, envelope EncryptedKey, expected ExpectedKey) ([]byte, error) {
	if len(dataKey) != DataKeySize {
		return nil, fmt.Errorf("key carrier data key must be exactly %d bytes", DataKeySize)
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	if err := envelope.matches(expected); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return nil, fmt.Errorf("initialize key carrier cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize key carrier AEAD: %w", err)
	}
	aad, err := envelope.additionalData()
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("authenticate encrypted key carrier: %w", err)
	}
	fingerprint, err := WalletFingerprint(envelope.Role, envelope.MaterialType, plaintext)
	if err != nil {
		zero(plaintext)
		return nil, fmt.Errorf("validate decrypted wallet material: %w", err)
	}
	if !strings.EqualFold(fingerprint, envelope.WalletFingerprint) {
		zero(plaintext)
		return nil, fmt.Errorf("decrypted wallet fingerprint does not match encrypted carrier")
	}
	return plaintext, nil
}

func (e EncryptedKey) Validate() error {
	if e.Version != EncryptedKeyVersion {
		return fmt.Errorf("unsupported encrypted key carrier version %d", e.Version)
	}
	if err := validateEnvelopeIdentity(e.KeyID, e.Role, e.Network, e.MaterialType); err != nil {
		return err
	}
	if !validFingerprint(e.WalletFingerprint) {
		return fmt.Errorf("wallet fingerprint is invalid")
	}
	if e.CreatedAt.IsZero() {
		return fmt.Errorf("encrypted key carrier creation time is required")
	}
	if len(e.Nonce) != 12 {
		return fmt.Errorf("encrypted key carrier nonce must be 12 bytes")
	}
	if len(e.Ciphertext) <= 16 {
		return fmt.Errorf("encrypted key carrier ciphertext is invalid")
	}
	return nil
}

func (e EncryptedKey) matches(expected ExpectedKey) error {
	if e.KeyID != strings.TrimSpace(expected.KeyID) || e.Role != expected.Role ||
		e.Network != strings.TrimSpace(expected.Network) || e.MaterialType != expected.MaterialType ||
		!strings.EqualFold(e.WalletFingerprint, strings.TrimSpace(expected.WalletFingerprint)) {
		return fmt.Errorf("encrypted key carrier identity does not match configured role, network, key ID, material type, and wallet fingerprint")
	}
	return nil
}

func (e EncryptedKey) additionalData() ([]byte, error) {
	metadata := struct {
		Version           int          `json:"version"`
		KeyID             string       `json:"key_id"`
		Role              KeyRole      `json:"role"`
		Network           string       `json:"network"`
		MaterialType      MaterialType `json:"material_type"`
		WalletFingerprint string       `json:"wallet_fingerprint"`
		CreatedAt         time.Time    `json:"created_at"`
	}{e.Version, e.KeyID, e.Role, e.Network, e.MaterialType, e.WalletFingerprint, e.CreatedAt}
	result, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode key carrier authenticated metadata: %w", err)
	}
	return result, nil
}

func WalletFingerprint(role KeyRole, materialType MaterialType, material []byte) (string, error) {
	var publicIdentity string
	switch materialType {
	case MaterialBIP32Seed:
		if role != KeyRoleTRONRecharge && role != KeyRoleEthereumRecharge {
			return "", fmt.Errorf("BIP32 seed is not valid for role %q", role)
		}
		key, err := externalBranchPrivateKey(role, materialType, material)
		if err != nil {
			return "", err
		}
		defer key.Zero()
		publicKey, err := key.Neuter()
		if err != nil {
			return "", fmt.Errorf("derive wallet account public key: %w", err)
		}
		defer publicKey.Zero()
		publicIdentity = publicKey.String()
	case MaterialAccountExtendedKey:
		if role != KeyRoleTRONRecharge && role != KeyRoleEthereumRecharge {
			return "", fmt.Errorf("account extended key is not valid for role %q", role)
		}
		key, err := externalBranchPrivateKey(role, materialType, material)
		if err != nil {
			return "", err
		}
		defer key.Zero()
		publicKey, err := key.Neuter()
		if err != nil {
			return "", fmt.Errorf("derive account public key: %w", err)
		}
		defer publicKey.Zero()
		publicIdentity = publicKey.String()
	case MaterialSecp256k1PrivateKey:
		if role != KeyRoleEthereumGas {
			return "", fmt.Errorf("secp256k1 private key is not valid for role %q", role)
		}
		privateKey, err := crypto.ToECDSA(material)
		if err != nil {
			return "", fmt.Errorf("Ethereum gas sponsor private key is invalid")
		}
		publicIdentity = strings.ToLower(crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
	default:
		return "", fmt.Errorf("unsupported key material type %q", materialType)
	}
	digest := sha256.Sum256([]byte(publicIdentity))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateEnvelopeIdentity(keyID string, role KeyRole, network string, materialType MaterialType) error {
	keyID = strings.TrimSpace(keyID)
	if len(keyID) != 32 {
		return fmt.Errorf("key carrier ID must contain 32 hexadecimal characters")
	}
	if _, err := hex.DecodeString(keyID); err != nil {
		return fmt.Errorf("key carrier ID must contain 32 hexadecimal characters")
	}
	network = strings.TrimSpace(network)
	switch role {
	case KeyRoleTRONRecharge:
		if !strings.HasPrefix(network, "tron-") || (materialType != MaterialBIP32Seed && materialType != MaterialAccountExtendedKey) {
			return fmt.Errorf("TRON recharge carrier requires a TRON network and HD wallet material")
		}
	case KeyRoleEthereumRecharge:
		if !strings.HasPrefix(network, "ethereum-") || (materialType != MaterialBIP32Seed && materialType != MaterialAccountExtendedKey) {
			return fmt.Errorf("Ethereum recharge carrier requires an Ethereum network and HD wallet material")
		}
	case KeyRoleEthereumGas:
		if !strings.HasPrefix(network, "ethereum-") || materialType != MaterialSecp256k1PrivateKey {
			return fmt.Errorf("Ethereum gas carrier requires an Ethereum network and secp256k1 private key material")
		}
	default:
		return fmt.Errorf("unsupported key carrier role %q", role)
	}
	return nil
}

func validFingerprint(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
