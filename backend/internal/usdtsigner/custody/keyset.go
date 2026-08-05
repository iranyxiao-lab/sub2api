package custody

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/google/go-tpm/tpm2/transport"
)

const maxCustodyFileBytes = 1 << 20

type DataKeyUnsealer interface {
	Unseal(SealedKey) ([]byte, error)
}

type TPMDataKeyUnsealer struct {
	TPM transport.TPM
}

func (u TPMDataKeyUnsealer) Unseal(sealed SealedKey) ([]byte, error) {
	return UnsealDataKey(u.TPM, sealed)
}

type CarrierConfig struct {
	SealedKeyFile     string
	EncryptedKeyFile  string
	KeyID             string
	Role              KeyRole
	Network           string
	MaterialType      MaterialType
	WalletFingerprint string
}

type KeySetConfig struct {
	TRONRecharge     CarrierConfig
	EthereumRecharge CarrierConfig
	EthereumGas      CarrierConfig
}

func (c KeySetConfig) Validate() error {
	carriers := []CarrierConfig{c.TRONRecharge, c.EthereumRecharge, c.EthereumGas}
	expectedRoles := []KeyRole{KeyRoleTRONRecharge, KeyRoleEthereumRecharge, KeyRoleEthereumGas}
	paths := make(map[string]KeyRole, 6)
	keyIDs := make(map[string]KeyRole, 3)
	for index, carrier := range carriers {
		if carrier.Role != expectedRoles[index] {
			return fmt.Errorf("key carrier slot %d must be configured for role %q", index, expectedRoles[index])
		}
		if err := validateEnvelopeIdentity(carrier.KeyID, carrier.Role, carrier.Network, carrier.MaterialType); err != nil {
			return fmt.Errorf("validate %s carrier identity: %w", carrier.Role, err)
		}
		if !validFingerprint(carrier.WalletFingerprint) {
			return fmt.Errorf("configured wallet fingerprint for role %q is invalid", carrier.Role)
		}
		for _, rawPath := range []string{carrier.SealedKeyFile, carrier.EncryptedKeyFile} {
			path := strings.TrimSpace(rawPath)
			if path == "" {
				return fmt.Errorf("sealed and encrypted key files are required for role %q", carrier.Role)
			}
			if previous, exists := paths[path]; exists {
				return fmt.Errorf("roles %q and %q reuse custody file %q", previous, carrier.Role, path)
			}
			paths[path] = carrier.Role
		}
		if previous, exists := keyIDs[carrier.KeyID]; exists {
			return fmt.Errorf("roles %q and %q reuse TPM sealed key ID %q", previous, carrier.Role, carrier.KeyID)
		}
		keyIDs[carrier.KeyID] = carrier.Role
	}
	return nil
}

type KeySet struct {
	materials    map[KeyRole][]byte
	fingerprints map[KeyRole]string
}

func LoadKeySet(config KeySetConfig, unsealer DataKeyUnsealer) (*KeySet, error) {
	if unsealer == nil {
		return nil, fmt.Errorf("TPM data key unsealer is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	carriers := []CarrierConfig{config.TRONRecharge, config.EthereumRecharge, config.EthereumGas}
	expectedRoles := []KeyRole{KeyRoleTRONRecharge, KeyRoleEthereumRecharge, KeyRoleEthereumGas}
	result := &KeySet{materials: make(map[KeyRole][]byte, 3), fingerprints: make(map[KeyRole]string, 3)}
	sealedIDs := make(map[string]KeyRole, 3)
	secretValues := make([][]byte, 0, 3)
	defer func() {
		for _, value := range secretValues {
			zero(value)
		}
	}()
	for index, carrier := range carriers {
		if carrier.Role != expectedRoles[index] {
			result.Close()
			return nil, fmt.Errorf("key carrier slot %d must be configured for role %q", index, expectedRoles[index])
		}
		if strings.TrimSpace(carrier.SealedKeyFile) == "" || strings.TrimSpace(carrier.EncryptedKeyFile) == "" {
			result.Close()
			return nil, fmt.Errorf("sealed and encrypted key files are required for role %q", carrier.Role)
		}
		var sealed SealedKey
		if err := readCustodyJSON(carrier.SealedKeyFile, &sealed); err != nil {
			result.Close()
			return nil, fmt.Errorf("read %s TPM sealed key: %w", carrier.Role, err)
		}
		if err := sealed.Validate(); err != nil {
			result.Close()
			return nil, fmt.Errorf("validate %s TPM sealed key: %w", carrier.Role, err)
		}
		if sealed.KeyID != strings.TrimSpace(carrier.KeyID) {
			result.Close()
			return nil, fmt.Errorf("TPM sealed key ID does not match configured role %q", carrier.Role)
		}
		if previous, exists := sealedIDs[sealed.KeyID]; exists {
			result.Close()
			return nil, fmt.Errorf("roles %q and %q reuse the same TPM sealed data key", previous, carrier.Role)
		}
		sealedIDs[sealed.KeyID] = carrier.Role
		var encrypted EncryptedKey
		if err := readCustodyJSON(carrier.EncryptedKeyFile, &encrypted); err != nil {
			result.Close()
			return nil, fmt.Errorf("read %s encrypted key: %w", carrier.Role, err)
		}
		dataKey, err := unsealer.Unseal(sealed)
		if err != nil {
			result.Close()
			return nil, fmt.Errorf("unseal %s data key: %w", carrier.Role, err)
		}
		material, err := DecryptKeyMaterial(dataKey, encrypted, ExpectedKey{
			KeyID: carrier.KeyID, Role: carrier.Role, Network: carrier.Network,
			MaterialType: carrier.MaterialType, WalletFingerprint: carrier.WalletFingerprint,
		})
		zero(dataKey)
		if err != nil {
			result.Close()
			return nil, fmt.Errorf("decrypt %s key carrier: %w", carrier.Role, err)
		}
		for otherIndex, other := range secretValues {
			if bytes.Equal(other, material) {
				zero(material)
				result.Close()
				return nil, fmt.Errorf("key material for roles %q and %q is not independent", carriers[otherIndex].Role, carrier.Role)
			}
		}
		secretValues = append(secretValues, append([]byte(nil), material...))
		result.materials[carrier.Role] = material
		result.fingerprints[carrier.Role] = encrypted.WalletFingerprint
	}
	return result, nil
}

func (k *KeySet) Material(role KeyRole) ([]byte, error) {
	if k == nil {
		return nil, fmt.Errorf("key set is unavailable")
	}
	material, ok := k.materials[role]
	if !ok {
		return nil, fmt.Errorf("key material for role %q is unavailable", role)
	}
	return append([]byte(nil), material...), nil
}

func (k *KeySet) Fingerprint(role KeyRole) (string, bool) {
	if k == nil {
		return "", false
	}
	value, ok := k.fingerprints[role]
	return value, ok
}

func (k *KeySet) Close() {
	if k == nil {
		return
	}
	for role, material := range k.materials {
		zero(material)
		delete(k.materials, role)
	}
	for role := range k.fingerprints {
		delete(k.fingerprints, role)
	}
}

func readCustodyJSON(path string, destination any) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("custody path must be a regular file")
	}
	if info.Size() <= 0 || info.Size() > maxCustodyFileBytes {
		return fmt.Errorf("custody file size is outside the allowed range")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("custody file must not be accessible by group or other users")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxCustodyFileBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("custody file contains trailing JSON values")
		}
		return err
	}
	return nil
}
