package onchain

import (
	"testing"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

var tronDerivationTestSeed = []byte("0123456789abcdef0123456789abcdef")

func TestDeriveTRONAddressMatchesSignerPrivateDerivation(t *testing.T) {
	accountXPub, accountXPrv := tronDerivationTestAccountKeys(t)
	require.Equal(t, "xpub6EFn6HriJB8wve2KZNPPbwEbwHJ5fmRGBrum7xyjaxzHPvtnns3ab3fpecQF8ZWoy3PnazqxAq6JazJDnNYrkp61ZX94fayC3hU6bdhjS52", accountXPub)
	vectors := []struct {
		index   uint32
		address string
	}{
		{index: 0, address: "TD8icGtfdGTCyxRvsoszeaKbTdrFHFfkbk"},
		{index: 1, address: "TDv8nzHvwFETUhQktZqtxWeK8yqXGMg1dL"},
		{index: 7, address: "TA8oghgpPJHHQ2mu7ztDnWRQT3Cyu4rHjT"},
		{index: 1024, address: "TWSqcMAWLGAKbTkTtduw3kGFroKZKCqmWV"},
	}
	for _, vector := range vectors {
		got, err := DeriveTRONAddress(accountXPub, vector.index)
		require.NoError(t, err)
		require.Equal(t, vector.address, got)
		require.Equal(t, vector.address, deriveSignerTRONAddress(t, accountXPrv, vector.index))
		require.NoError(t, ValidateAddress(NetworkTronMainnet, got))
	}
}

func TestDeriveTRONAddressRejectsUnsafeKeysAndIndexes(t *testing.T) {
	accountXPub, accountXPrv := tronDerivationTestAccountKeys(t)

	_, err := DeriveTRONAddress(accountXPrv, 0)
	require.ErrorContains(t, err, "extended public key")
	_, err = DeriveTRONAddress(accountXPub, hdkeychain.HardenedKeyStart)
	require.ErrorContains(t, err, "non-hardened")
	_, err = DeriveTRONAddress("not-an-xpub", 0)
	require.ErrorContains(t, err, "parse TRON extended public key")

	master, err := hdkeychain.NewMaster(tronDerivationTestSeed, &chaincfg.MainNetParams)
	require.NoError(t, err)
	defer master.Zero()
	masterXPub, err := master.Neuter()
	require.NoError(t, err)
	defer masterXPub.Zero()
	_, err = DeriveTRONAddress(masterXPub.String(), 0)
	require.ErrorContains(t, err, "depth 4")
}

func tronDerivationTestAccountKeys(t *testing.T) (string, string) {
	t.Helper()
	key, err := hdkeychain.NewMaster(tronDerivationTestSeed, &chaincfg.MainNetParams)
	require.NoError(t, err)
	defer key.Zero()
	for _, index := range []uint32{
		hdkeychain.HardenedKeyStart + 44,
		hdkeychain.HardenedKeyStart + 195,
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

func deriveSignerTRONAddress(t *testing.T, accountXPrv string, index uint32) string {
	t.Helper()
	key, err := hdkeychain.NewKeyFromString(accountXPrv)
	require.NoError(t, err)
	defer key.Zero()
	child, err := key.Derive(index)
	require.NoError(t, err)
	defer child.Zero()
	privateKey, err := child.ECPrivKey()
	require.NoError(t, err)
	ethereumAddress := crypto.PubkeyToAddress(privateKey.ToECDSA().PublicKey)
	return base58.CheckEncode(ethereumAddress.Bytes(), 0x41)
}
