package onchain

import (
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/ethereum/go-ethereum/crypto"
)

func DeriveTRONAddress(accountXPub string, index uint32) (string, error) {
	if index >= hdkeychain.HardenedKeyStart {
		return "", fmt.Errorf("TRON address index must be non-hardened")
	}
	key, err := hdkeychain.NewKeyFromString(strings.TrimSpace(accountXPub))
	if err != nil {
		return "", fmt.Errorf("parse TRON extended public key: %w", err)
	}
	defer key.Zero()
	if key.IsPrivate() {
		return "", fmt.Errorf("TRON address derivation requires an extended public key")
	}
	if key.Depth() != 4 {
		return "", fmt.Errorf("TRON extended public key must represent m/44'/195'/0'/0 (depth 4)")
	}

	child, err := key.Derive(index)
	if err != nil {
		return "", fmt.Errorf("derive TRON child index %d: %w", index, err)
	}
	defer child.Zero()
	publicKey, err := child.ECPubKey()
	if err != nil {
		return "", fmt.Errorf("read TRON child public key: %w", err)
	}
	uncompressed := publicKey.SerializeUncompressed()
	if len(uncompressed) != 65 || uncompressed[0] != 0x04 {
		return "", fmt.Errorf("unexpected secp256k1 public key encoding")
	}
	hash := crypto.Keccak256(uncompressed[1:])
	address := base58.CheckEncode(hash[len(hash)-20:], 0x41)
	if err := ValidateAddress(NetworkTronMainnet, address); err != nil {
		return "", fmt.Errorf("validate derived TRON address: %w", err)
	}
	return address, nil
}
