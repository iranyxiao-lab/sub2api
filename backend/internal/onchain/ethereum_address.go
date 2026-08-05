package onchain

import (
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/ethereum/go-ethereum/crypto"
)

func DeriveEthereumAddress(accountXPub string, index uint32) (string, error) {
	if index >= hdkeychain.HardenedKeyStart {
		return "", fmt.Errorf("Ethereum address index must be non-hardened")
	}
	key, err := hdkeychain.NewKeyFromString(strings.TrimSpace(accountXPub))
	if err != nil {
		return "", fmt.Errorf("parse Ethereum extended public key: %w", err)
	}
	defer key.Zero()
	if key.IsPrivate() {
		return "", fmt.Errorf("Ethereum address derivation requires an extended public key")
	}
	if key.Depth() != 4 {
		return "", fmt.Errorf("Ethereum extended public key must represent m/44'/60'/0'/0 (depth 4)")
	}
	child, err := key.Derive(index)
	if err != nil {
		return "", fmt.Errorf("derive Ethereum child index %d: %w", index, err)
	}
	defer child.Zero()
	publicKey, err := child.ECPubKey()
	if err != nil {
		return "", fmt.Errorf("read Ethereum child public key: %w", err)
	}
	publicECDSA, err := crypto.DecompressPubkey(publicKey.SerializeCompressed())
	if err != nil {
		return "", fmt.Errorf("decode Ethereum child public key: %w", err)
	}
	address := crypto.PubkeyToAddress(*publicECDSA).Hex()
	if err := ValidateAddress(NetworkEthereumMainnet, address); err != nil {
		return "", fmt.Errorf("validate derived Ethereum address: %w", err)
	}
	return address, nil
}
