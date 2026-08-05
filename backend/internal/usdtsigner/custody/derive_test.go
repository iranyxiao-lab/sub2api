package custody

import (
	"crypto/ecdsa"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestDeriveRechargePrivateKeyMatchesBusinessTRONXPub(t *testing.T) {
	seed := []byte("0123456789abcdef0123456789abcdef")
	branch, err := externalBranchPrivateKey(KeyRoleTRONRecharge, MaterialBIP32Seed, seed)
	require.NoError(t, err)
	defer branch.Zero()
	public, err := branch.Neuter()
	require.NoError(t, err)
	defer public.Zero()

	for _, index := range []uint32{0, 1, 1024} {
		privateKey, err := DeriveRechargePrivateKey(KeyRoleTRONRecharge, MaterialBIP32Seed, seed, index)
		require.NoError(t, err)
		address, err := onchain.DeriveTRONAddress(public.String(), index)
		require.NoError(t, err)
		require.Equal(t, address, tronAddressFromPublicKey(&privateKey.PublicKey))
	}
	_, err = DeriveRechargePrivateKey(KeyRoleTRONRecharge, MaterialBIP32Seed, seed, hdkeychain.HardenedKeyStart)
	require.Error(t, err)
}

func TestDeriveRechargePrivateKeyMatchesEthereumBIP44(t *testing.T) {
	seed := []byte("abcdef0123456789abcdef0123456789")
	privateKey, err := DeriveRechargePrivateKey(KeyRoleEthereumRecharge, MaterialBIP32Seed, seed, 7)
	require.NoError(t, err)
	masterKey := mustMasterKey(t, seed)
	require.NotEqual(t, crypto.PubkeyToAddress(privateKey.PublicKey), crypto.PubkeyToAddress(masterKey.PublicKey))
}

func mustMasterKey(t *testing.T, seed []byte) *ecdsa.PrivateKey {
	t.Helper()
	master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	require.NoError(t, err)
	defer master.Zero()
	key, err := master.ECPrivKey()
	require.NoError(t, err)
	return key.ToECDSA()
}

func tronAddressFromPublicKey(publicKey *ecdsa.PublicKey) string {
	return base58.CheckEncode(crypto.PubkeyToAddress(*publicKey).Bytes(), 0x41)
}
