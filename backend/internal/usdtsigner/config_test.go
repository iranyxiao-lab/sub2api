package usdtsigner

import (
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/usdtsigner/custody"
	"github.com/stretchr/testify/require"
)

func validTestConfig() Config {
	return Config{
		Environment:          EnvironmentProduction,
		DeploymentMode:       DeploymentModeStandalone,
		RuntimeMode:          RuntimeModeTPM,
		ListenAddress:        "10.20.30.40:9443",
		TLSCertFile:          "server.crt",
		TLSKeyFile:           "server.key",
		ClientCAFile:         "client-ca.crt",
		AuthorizedClientURIs: []string{"spiffe://sub2api.internal/workload/sweep-worker"},
		ReadHeaderTimeout:    5 * time.Second,
		IdleTimeout:          30 * time.Second,
		ShutdownTimeout:      10 * time.Second,
		TPMDevice:            "windows-tbs",
		KeySet:               validTestKeySetConfig(),
	}
}

func validTestKeySetConfig() custody.KeySetConfig {
	return custody.KeySetConfig{
		TRONRecharge: custody.CarrierConfig{
			SealedKeyFile: "tron-sealed.json", EncryptedKeyFile: "tron-encrypted.json",
			KeyID: "00112233445566778899aabbccddeeff", Role: custody.KeyRoleTRONRecharge,
			Network: "tron-mainnet", MaterialType: custody.MaterialBIP32Seed,
			WalletFingerprint: "sha256:00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		},
		EthereumRecharge: custody.CarrierConfig{
			SealedKeyFile: "ethereum-sealed.json", EncryptedKeyFile: "ethereum-encrypted.json",
			KeyID: "11112233445566778899aabbccddeeff", Role: custody.KeyRoleEthereumRecharge,
			Network: "ethereum-mainnet", MaterialType: custody.MaterialBIP32Seed,
			WalletFingerprint: "sha256:11112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		},
		EthereumGas: custody.CarrierConfig{
			SealedKeyFile: "gas-sealed.json", EncryptedKeyFile: "gas-encrypted.json",
			KeyID: "22112233445566778899aabbccddeeff", Role: custody.KeyRoleEthereumGas,
			Network: "ethereum-mainnet", MaterialType: custody.MaterialSecp256k1PrivateKey,
			WalletFingerprint: "sha256:22112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		},
	}
}

func TestConfigRejectsEmbeddedDeploymentInProduction(t *testing.T) {
	cfg := validTestConfig()
	cfg.DeploymentMode = "embedded"
	require.ErrorContains(t, cfg.Validate(), "embedded loading is forbidden")
}

func TestConfigRequiresPrivateLiteralListener(t *testing.T) {
	for _, address := range []string{"0.0.0.0:9443", "203.0.113.10:9443", "signer.example.com:9443"} {
		t.Run(address, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.ListenAddress = address
			require.Error(t, cfg.Validate())
		})
	}

	cfg := validTestConfig()
	cfg.ListenAddress = "127.0.0.1:9443"
	require.NoError(t, cfg.Validate())
}

func TestConfigRequiresMTLSAndAuthorizedServiceIdentity(t *testing.T) {
	tests := []func(*Config){
		func(c *Config) { c.TLSCertFile = "" },
		func(c *Config) { c.TLSKeyFile = "" },
		func(c *Config) { c.ClientCAFile = "" },
		func(c *Config) { c.AuthorizedClientURIs = nil },
		func(c *Config) { c.AuthorizedClientURIs = []string{"https://example.com/worker"} },
	}
	for index, mutate := range tests {
		t.Run(time.Duration(index).String(), func(t *testing.T) {
			cfg := validTestConfig()
			mutate(&cfg)
			require.Error(t, cfg.Validate())
		})
	}
}

func TestProductionConfigRequiresThreeIndependentEncryptedCarriers(t *testing.T) {
	cfg := validTestConfig()
	cfg.KeySet.EthereumRecharge.KeyID = cfg.KeySet.TRONRecharge.KeyID
	require.ErrorContains(t, cfg.Validate(), "reuse TPM sealed key ID")

	cfg = validTestConfig()
	cfg.KeySet.EthereumGas.EncryptedKeyFile = cfg.KeySet.TRONRecharge.EncryptedKeyFile
	require.ErrorContains(t, cfg.Validate(), "reuse custody file")
}

func TestLoadConfigRejectsPlaintextKeyEnvironment(t *testing.T) {
	t.Setenv("USDT_SIGNER_TRON_SEED", "plaintext-is-forbidden")
	_, err := LoadConfigFromEnv()
	require.ErrorContains(t, err, "plaintext key environment variable")
}

func TestProductionConfigRejectsTestRuntimeModesAndSeedMaterial(t *testing.T) {
	for _, mode := range []string{RuntimeModeMock, RuntimeModeTestSeed} {
		t.Run(mode, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.RuntimeMode = mode
			if mode == RuntimeModeTestSeed {
				cfg.TestSeed = make([]byte, 32)
			}
			require.ErrorContains(t, cfg.Validate(), "test modes are forbidden")
		})
	}

	cfg := validTestConfig()
	cfg.TestSeed = make([]byte, 32)
	require.ErrorContains(t, cfg.Validate(), "plaintext test seed")
}

func TestNonProductionMockModeDoesNotRequireTPMKeyCarriers(t *testing.T) {
	cfg := validTestConfig()
	cfg.Environment = "test"
	cfg.RuntimeMode = RuntimeModeMock
	cfg.TPMDevice = ""
	cfg.KeySet = custody.KeySetConfig{}
	require.NoError(t, cfg.Validate())
}

func TestOneTimeTestSeedModeConsumesSeed(t *testing.T) {
	t.Setenv("USDT_SIGNER_TEST_SEED_HEX", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	seed, err := testSeedFromEnv("test", RuntimeModeTestSeed)
	require.NoError(t, err)
	require.Len(t, seed, 32)
	require.Empty(t, os.Getenv("USDT_SIGNER_TEST_SEED_HEX"))

	cfg := validTestConfig()
	cfg.Environment = "test"
	cfg.RuntimeMode = RuntimeModeTestSeed
	cfg.TestSeed = seed
	expectedSeed := append([]byte(nil), seed...)
	cfg.TPMDevice = ""
	cfg.KeySet = custody.KeySetConfig{}
	require.NoError(t, cfg.Validate())

	consumed, err := cfg.ConsumeTestSeed()
	require.NoError(t, err)
	require.Equal(t, expectedSeed, consumed)
	require.Nil(t, cfg.TestSeed)
	_, err = cfg.ConsumeTestSeed()
	require.ErrorContains(t, err, "unavailable")
}

func TestTestSeedEnvironmentIsRejectedOutsideExplicitMode(t *testing.T) {
	t.Setenv("USDT_SIGNER_TEST_SEED_HEX", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.ErrorContains(t, rejectPlaintextKeyEnvironment("development", RuntimeModeTPM), "only allowed")
	require.ErrorContains(t, rejectPlaintextKeyEnvironment(EnvironmentProduction, RuntimeModeTestSeed), "forbidden in production")
}
