package custody

import (
	"crypto/ecdsa"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/crypto"
)

func DeriveRechargePrivateKey(role KeyRole, materialType MaterialType, material []byte, index uint32) (*ecdsa.PrivateKey, error) {
	if role != KeyRoleTRONRecharge && role != KeyRoleEthereumRecharge {
		return nil, fmt.Errorf("role %q does not contain a recharge HD wallet", role)
	}
	if index >= hdkeychain.HardenedKeyStart {
		return nil, fmt.Errorf("recharge derivation index must be non-hardened")
	}
	branch, err := externalBranchPrivateKey(role, materialType, material)
	if err != nil {
		return nil, err
	}
	defer branch.Zero()
	child, err := branch.Derive(index)
	if err != nil {
		return nil, fmt.Errorf("derive recharge child index %d: %w", index, err)
	}
	defer child.Zero()
	privateKey, err := child.ECPrivKey()
	if err != nil {
		return nil, fmt.Errorf("read recharge child private key: %w", err)
	}
	serialized := privateKey.Serialize()
	defer zero(serialized)
	result, err := crypto.ToECDSA(serialized)
	if err != nil {
		return nil, fmt.Errorf("normalize recharge child private key: %w", err)
	}
	return result, nil
}

func externalBranchPrivateKey(role KeyRole, materialType MaterialType, material []byte) (*hdkeychain.ExtendedKey, error) {
	switch materialType {
	case MaterialBIP32Seed:
		if len(material) < 16 || len(material) > 64 {
			return nil, fmt.Errorf("BIP32 seed must contain between 16 and 64 bytes")
		}
		coinType := uint32(195)
		if role == KeyRoleEthereumRecharge {
			coinType = 60
		}
		key, err := hdkeychain.NewMaster(append([]byte(nil), material...), &chaincfg.MainNetParams)
		if err != nil {
			return nil, fmt.Errorf("derive recharge master key: %w", err)
		}
		for _, childIndex := range []uint32{
			hdkeychain.HardenedKeyStart + 44,
			hdkeychain.HardenedKeyStart + coinType,
			hdkeychain.HardenedKeyStart,
			0,
		} {
			child, deriveErr := key.Derive(childIndex)
			key.Zero()
			if deriveErr != nil {
				return nil, fmt.Errorf("derive recharge external branch: %w", deriveErr)
			}
			key = child
		}
		return key, nil
	case MaterialAccountExtendedKey:
		key, err := hdkeychain.NewKeyFromString(strings.TrimSpace(string(material)))
		if err != nil || !key.IsPrivate() {
			return nil, fmt.Errorf("account extended private key is invalid")
		}
		if key.Depth() != 4 {
			key.Zero()
			return nil, fmt.Errorf("account extended private key must represent the external branch at depth 4")
		}
		return key, nil
	default:
		return nil, fmt.Errorf("material type %q cannot derive recharge addresses", materialType)
	}
}
