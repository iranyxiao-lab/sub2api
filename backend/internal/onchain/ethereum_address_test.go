package onchain

import (
	"testing"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

var ethereumDerivationTestSeed = []byte("0123456789abcdef0123456789abcdef")

func TestDeriveEthereumAddressMatchesSignerPrivateDerivation(t *testing.T) {
	accountXPub, accountXPrv := ethereumDerivationTestAccountKeys(t)
	vectors := []struct {
		index   uint32
		address string
	}{
		{index: 0, address: "0xcc0a50F5A968c28A4eBa361863a1068CEfC84680"},
		{index: 1, address: "0xa3672F50E4BCddF73c30F53c021E5F060fb16fd3"},
		{index: 7, address: "0xD3E42Ef1Ab99f1aa9f308168Ae3A952cEea3FBff"},
		{index: 1024, address: "0xadA8F56A6BCf0075bE608bA59bdf3ce82bEC7762"},
	}
	for _, vector := range vectors {
		got, err := DeriveEthereumAddress(accountXPub, vector.index)
		require.NoError(t, err)
		require.Equal(t, vector.address, got)
		require.Equal(t, vector.address, deriveSignerEthereumAddress(t, accountXPrv, vector.index))
		require.NoError(t, ValidateAddress(NetworkEthereumMainnet, got))
	}
}

func TestDeriveEthereumAddressRejectsUnsafeKeysAndIndexes(t *testing.T) {
	accountXPub, accountXPrv := ethereumDerivationTestAccountKeys(t)
	_, err := DeriveEthereumAddress(accountXPrv, 0)
	require.ErrorContains(t, err, "extended public key")
	_, err = DeriveEthereumAddress(accountXPub, hdkeychain.HardenedKeyStart)
	require.ErrorContains(t, err, "non-hardened")
	_, err = DeriveEthereumAddress("not-an-xpub", 0)
	require.ErrorContains(t, err, "parse Ethereum extended public key")

	master, err := hdkeychain.NewMaster(ethereumDerivationTestSeed, &chaincfg.MainNetParams)
	require.NoError(t, err)
	defer master.Zero()
	masterXPub, err := master.Neuter()
	require.NoError(t, err)
	defer masterXPub.Zero()
	_, err = DeriveEthereumAddress(masterXPub.String(), 0)
	require.ErrorContains(t, err, "depth 4")
}

func ethereumDerivationTestAccountKeys(t *testing.T) (string, string) {
	t.Helper()
	key, err := hdkeychain.NewMaster(ethereumDerivationTestSeed, &chaincfg.MainNetParams)
	require.NoError(t, err)
	defer key.Zero()
	for _, index := range []uint32{
		hdkeychain.HardenedKeyStart + 44,
		hdkeychain.HardenedKeyStart + 60,
		hdkeychain.HardenedKeyStart,
		0,
	} {
		child, deriveErr := key.Derive(index)
		require.NoError(t, deriveErr)
		key.Zero()
		key = child
	}
	publicKey, err := key.Neuter()
	require.NoError(t, err)
	defer publicKey.Zero()
	return publicKey.String(), key.String()
}

func deriveSignerEthereumAddress(t *testing.T, accountXPrv string, index uint32) string {
	t.Helper()
	key, err := hdkeychain.NewKeyFromString(accountXPrv)
	require.NoError(t, err)
	defer key.Zero()
	child, err := key.Derive(index)
	require.NoError(t, err)
	defer child.Zero()
	privateKey, err := child.ECPrivKey()
	require.NoError(t, err)
	return crypto.PubkeyToAddress(privateKey.ToECDSA().PublicKey).Hex()
}
