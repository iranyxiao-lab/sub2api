package usdtsigner

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/usdtsigner/custody"
	"github.com/ethereum/go-ethereum/common"
)

const (
	DeploymentModeStandalone = "standalone"
	EnvironmentProduction    = "production"
	RuntimeModeTPM           = "tpm"
	RuntimeModeMock          = "mock"
	RuntimeModeTestSeed      = "test_seed"
)

type Config struct {
	Environment          string
	DeploymentMode       string
	RuntimeMode          string
	ListenAddress        string
	TLSCertFile          string
	TLSKeyFile           string
	ClientCAFile         string
	AuthorizedClientURIs []string
	ReadHeaderTimeout    time.Duration
	IdleTimeout          time.Duration
	ShutdownTimeout      time.Duration
	TPMDevice            string
	TestSeed             []byte
	KeySet               custody.KeySetConfig
	TRONOperation        TRONOperationConfig
	EthereumOperation    EthereumOperationConfig
	Policy               PolicyConfig
	OperationJournalFile string
}

func LoadConfigFromEnv() (Config, error) {
	environment := envOrDefault("USDT_SIGNER_ENVIRONMENT", "development")
	runtimeMode := envOrDefault("USDT_SIGNER_RUNTIME_MODE", RuntimeModeTPM)
	if err := rejectPlaintextKeyEnvironment(environment, runtimeMode); err != nil {
		return Config{}, err
	}
	testSeed, err := testSeedFromEnv(environment, runtimeMode)
	if err != nil {
		return Config{}, err
	}
	keepTestSeed := false
	defer func() {
		if keepTestSeed {
			return
		}
		for index := range testSeed {
			testSeed[index] = 0
		}
	}()
	cfg := Config{
		Environment:          environment,
		DeploymentMode:       envOrDefault("USDT_SIGNER_DEPLOYMENT_MODE", DeploymentModeStandalone),
		RuntimeMode:          runtimeMode,
		ListenAddress:        envOrDefault("USDT_SIGNER_LISTEN_ADDRESS", "127.0.0.1:9443"),
		TLSCertFile:          strings.TrimSpace(os.Getenv("USDT_SIGNER_TLS_CERT_FILE")),
		TLSKeyFile:           strings.TrimSpace(os.Getenv("USDT_SIGNER_TLS_KEY_FILE")),
		ClientCAFile:         strings.TrimSpace(os.Getenv("USDT_SIGNER_CLIENT_CA_FILE")),
		AuthorizedClientURIs: splitNonEmpty(os.Getenv("USDT_SIGNER_AUTHORIZED_CLIENT_URIS")),
		ReadHeaderTimeout:    5 * time.Second,
		IdleTimeout:          30 * time.Second,
		ShutdownTimeout:      10 * time.Second,
		TPMDevice:            envOrDefault("USDT_SIGNER_TPM_DEVICE", defaultTPMDevice()),
		TestSeed:             testSeed,
		KeySet: custody.KeySetConfig{
			TRONRecharge:     carrierConfigFromEnv("USDT_SIGNER_TRON", custody.KeyRoleTRONRecharge),
			EthereumRecharge: carrierConfigFromEnv("USDT_SIGNER_ETHEREUM", custody.KeyRoleEthereumRecharge),
			EthereumGas:      carrierConfigFromEnv("USDT_SIGNER_ETHEREUM_GAS", custody.KeyRoleEthereumGas),
		},
		TRONOperation: TRONOperationConfig{
			Network:           onchain.Network(strings.TrimSpace(os.Getenv("USDT_SIGNER_TRON_NETWORK"))),
			FullNodeURL:       strings.TrimSpace(os.Getenv("USDT_SIGNER_TRON_FULL_NODE_URL")),
			USDTContract:      strings.TrimSpace(os.Getenv("USDT_SIGNER_TRON_USDT_CONTRACT")),
			CollectionAddress: strings.TrimSpace(os.Getenv("USDT_SIGNER_TRON_COLLECTION_ADDRESS")),
			Timeout:           10 * time.Second, ResponseMaxBytes: 1 << 20,
		},
		EthereumOperation: EthereumOperationConfig{
			Network:           onchain.Network(strings.TrimSpace(os.Getenv("USDT_SIGNER_ETHEREUM_NETWORK"))),
			RPCURL:            strings.TrimSpace(os.Getenv("USDT_SIGNER_ETHEREUM_RPC_URL")),
			USDTContract:      common.HexToAddress(strings.TrimSpace(os.Getenv("USDT_SIGNER_ETHEREUM_USDT_CONTRACT"))),
			CollectionAddress: common.HexToAddress(strings.TrimSpace(os.Getenv("USDT_SIGNER_ETHEREUM_COLLECTION_ADDRESS"))),
			Timeout:           10 * time.Second,
		},
		OperationJournalFile: strings.TrimSpace(os.Getenv("USDT_SIGNER_OPERATION_JOURNAL_FILE")),
	}

	err = nil
	if cfg.ReadHeaderTimeout, err = durationSecondsFromEnv("USDT_SIGNER_READ_HEADER_TIMEOUT_SECONDS", cfg.ReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if cfg.IdleTimeout, err = durationSecondsFromEnv("USDT_SIGNER_IDLE_TIMEOUT_SECONDS", cfg.IdleTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = durationSecondsFromEnv("USDT_SIGNER_SHUTDOWN_TIMEOUT_SECONDS", cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if err := loadFundsConfiguration(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if strings.EqualFold(strings.TrimSpace(cfg.RuntimeMode), RuntimeModeMock) {
		keepTestSeed = true
		return cfg, nil
	}
	if err := cfg.ValidateFundsOperations(); err != nil {
		return Config{}, err
	}
	keepTestSeed = true
	return cfg, nil
}

func (c Config) ValidateFundsOperations() error {
	if err := c.TRONOperation.Validate(); err != nil {
		return err
	}
	if err := c.EthereumOperation.Validate(); err != nil {
		return err
	}
	if err := c.Policy.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.OperationJournalFile) == "" {
		return fmt.Errorf("USDT signer operation journal file is required")
	}
	return nil
}

func loadFundsConfiguration(cfg *Config) error {
	var err error
	if cfg.TRONOperation.FeeLimitSun, err = int64FromEnv("USDT_SIGNER_TRON_FEE_LIMIT_SUN", 100_000_000); err != nil {
		return err
	}
	if cfg.TRONOperation.Timeout, err = durationSecondsFromEnv("USDT_SIGNER_TRON_TIMEOUT_SECONDS", cfg.TRONOperation.Timeout); err != nil {
		return err
	}
	if cfg.TRONOperation.ResponseMaxBytes, err = int64FromEnv("USDT_SIGNER_TRON_RESPONSE_MAX_BYTES", cfg.TRONOperation.ResponseMaxBytes); err != nil {
		return err
	}
	if cfg.EthereumOperation.ChainID, err = uint64FromEnv("USDT_SIGNER_ETHEREUM_CHAIN_ID", 0); err != nil {
		return err
	}
	if cfg.EthereumOperation.Timeout, err = durationSecondsFromEnv("USDT_SIGNER_ETHEREUM_TIMEOUT_SECONDS", cfg.EthereumOperation.Timeout); err != nil {
		return err
	}
	if cfg.EthereumOperation.MaxSweepGasLimit, err = uint64FromEnv("USDT_SIGNER_ETHEREUM_MAX_SWEEP_GAS", 100_000); err != nil {
		return err
	}
	if cfg.EthereumOperation.MaxFundingGasLimit, err = uint64FromEnv("USDT_SIGNER_ETHEREUM_MAX_FUNDING_GAS", 30_000); err != nil {
		return err
	}
	if cfg.EthereumOperation.MaxFeePerGasWei, err = bigIntFromEnv("USDT_SIGNER_ETHEREUM_MAX_FEE_PER_GAS_WEI", "100000000000"); err != nil {
		return err
	}
	if cfg.EthereumOperation.MaxPriorityFeePerGasWei, err = bigIntFromEnv("USDT_SIGNER_ETHEREUM_MAX_PRIORITY_FEE_PER_GAS_WEI", "5000000000"); err != nil {
		return err
	}
	if cfg.EthereumOperation.MaxFundingTotalFeeWei, err = bigIntFromEnv("USDT_SIGNER_ETHEREUM_MAX_FUNDING_TOTAL_FEE_WEI", "6000000000000000"); err != nil {
		return err
	}
	if cfg.EthereumOperation.MaxSweepTotalFeeWei, err = bigIntFromEnv("USDT_SIGNER_ETHEREUM_MAX_SWEEP_TOTAL_FEE_WEI", "6000000000000000"); err != nil {
		return err
	}
	if cfg.EthereumOperation.PendingThreshold, err = durationSecondsFromEnv("USDT_SIGNER_ETHEREUM_PENDING_THRESHOLD_SECONDS", 15*time.Minute); err != nil {
		return err
	}
	if cfg.EthereumOperation.ReplacementBumpPercent, err = uint64FromEnv("USDT_SIGNER_ETHEREUM_REPLACEMENT_BUMP_PERCENT", 15); err != nil {
		return err
	}
	if cfg.EthereumOperation.MaxReplacementAttempts, err = intFromEnv("USDT_SIGNER_ETHEREUM_MAX_REPLACEMENT_ATTEMPTS", 5); err != nil {
		return err
	}
	if cfg.Policy.SweepTRC20, err = operationLimitFromEnv("USDT_SIGNER_TRC20", "100000000000", "500000000000", 4); err != nil {
		return err
	}
	if cfg.Policy.SweepERC20, err = operationLimitFromEnv("USDT_SIGNER_ERC20", "100000000000", "500000000000", 4); err != nil {
		return err
	}
	if cfg.Policy.FundERC20Gas, err = operationLimitFromEnv("USDT_SIGNER_GAS_FUNDING", "6000000000000000", "1000000000000000000", 1); err != nil {
		return err
	}
	return nil
}

func intFromEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}

func operationLimitFromEnv(prefix, singleDefault, dailyDefault string, concurrencyDefault int) (OperationLimit, error) {
	single, err := bigIntFromEnv(prefix+"_SINGLE_LIMIT", singleDefault)
	if err != nil {
		return OperationLimit{}, err
	}
	daily, err := bigIntFromEnv(prefix+"_DAILY_LIMIT", dailyDefault)
	if err != nil {
		return OperationLimit{}, err
	}
	concurrency64, err := int64FromEnv(prefix+"_CONCURRENCY_LIMIT", int64(concurrencyDefault))
	if err != nil {
		return OperationLimit{}, err
	}
	return OperationLimit{SingleAmount: single, DailyAmount: daily, Concurrency: int(concurrency64)}, nil
}

func bigIntFromEnv(name, fallback string) (*big.Int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		raw = fallback
	}
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok || value.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be a positive base-10 integer", name)
	}
	return value, nil
}

func int64FromEnv(name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a base-10 integer", name)
	}
	return value, nil
}

func uint64FromEnv(name string, fallback uint64) (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned base-10 integer", name)
	}
	return value, nil
}

func (c Config) Validate() error {
	if strings.ToLower(strings.TrimSpace(c.DeploymentMode)) != DeploymentModeStandalone {
		return fmt.Errorf("USDT signer deployment mode must be standalone; embedded loading is forbidden")
	}
	if err := validatePrivateListenAddress(c.ListenAddress); err != nil {
		return err
	}
	if strings.TrimSpace(c.TLSCertFile) == "" || strings.TrimSpace(c.TLSKeyFile) == "" || strings.TrimSpace(c.ClientCAFile) == "" {
		return fmt.Errorf("USDT signer mTLS certificate, key, and client CA files are required")
	}
	if len(c.AuthorizedClientURIs) == 0 {
		return fmt.Errorf("USDT signer requires at least one authorized client identity URI")
	}
	seen := make(map[string]struct{}, len(c.AuthorizedClientURIs))
	for _, raw := range c.AuthorizedClientURIs {
		identity, err := parseSPIFFEIdentity(raw)
		if err != nil {
			return fmt.Errorf("authorized client identity %q: %w", raw, err)
		}
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("duplicate authorized client identity %q", identity)
		}
		seen[identity] = struct{}{}
	}
	if c.ReadHeaderTimeout < time.Second || c.ReadHeaderTimeout > 30*time.Second {
		return fmt.Errorf("read header timeout must be between 1 and 30 seconds")
	}
	if c.IdleTimeout < time.Second || c.IdleTimeout > 5*time.Minute {
		return fmt.Errorf("idle timeout must be between 1 and 300 seconds")
	}
	if c.ShutdownTimeout < time.Second || c.ShutdownTimeout > time.Minute {
		return fmt.Errorf("shutdown timeout must be between 1 and 60 seconds")
	}
	mode := strings.ToLower(strings.TrimSpace(c.RuntimeMode))
	if mode == "" {
		mode = RuntimeModeTPM
	}
	production := strings.EqualFold(strings.TrimSpace(c.Environment), EnvironmentProduction)
	if production && mode != RuntimeModeTPM {
		return fmt.Errorf("production USDT signer requires TPM runtime mode; test modes are forbidden")
	}
	if production && len(c.TestSeed) != 0 {
		return fmt.Errorf("production USDT signer rejects plaintext test seed material")
	}
	switch mode {
	case RuntimeModeTPM:
		if len(c.TestSeed) != 0 {
			return fmt.Errorf("test seed material is only valid in test_seed runtime mode")
		}
		if strings.TrimSpace(c.TPMDevice) == "" {
			return fmt.Errorf("USDT signer TPM device is required")
		}
		if err := c.KeySet.Validate(); err != nil {
			return fmt.Errorf("USDT signer key custody configuration: %w", err)
		}
	case RuntimeModeMock:
		if len(c.TestSeed) != 0 {
			return fmt.Errorf("mock runtime mode must not receive test seed material")
		}
	case RuntimeModeTestSeed:
		if production {
			return fmt.Errorf("production USDT signer rejects test seed runtime mode")
		}
		if len(c.TestSeed) != 32 {
			return fmt.Errorf("test_seed runtime mode requires a 256-bit one-time test seed")
		}
	default:
		return fmt.Errorf("USDT signer runtime mode must be one of %q, %q, or %q", RuntimeModeTPM, RuntimeModeMock, RuntimeModeTestSeed)
	}
	return nil
}

func (c *Config) ConsumeTestSeed() ([]byte, error) {
	if c == nil || !strings.EqualFold(strings.TrimSpace(c.RuntimeMode), RuntimeModeTestSeed) || len(c.TestSeed) != 32 {
		return nil, fmt.Errorf("one-time test seed is unavailable")
	}
	seed := append([]byte(nil), c.TestSeed...)
	for index := range c.TestSeed {
		c.TestSeed[index] = 0
	}
	c.TestSeed = nil
	return seed, nil
}

func carrierConfigFromEnv(prefix string, role custody.KeyRole) custody.CarrierConfig {
	return custody.CarrierConfig{
		SealedKeyFile:     strings.TrimSpace(os.Getenv(prefix + "_SEALED_KEY_FILE")),
		EncryptedKeyFile:  strings.TrimSpace(os.Getenv(prefix + "_ENCRYPTED_KEY_FILE")),
		KeyID:             strings.TrimSpace(os.Getenv(prefix + "_KEY_ID")),
		Role:              role,
		Network:           strings.TrimSpace(os.Getenv(prefix + "_NETWORK")),
		MaterialType:      custody.MaterialType(strings.TrimSpace(os.Getenv(prefix + "_MATERIAL_TYPE"))),
		WalletFingerprint: strings.TrimSpace(os.Getenv(prefix + "_WALLET_FINGERPRINT")),
	}
}

func rejectPlaintextKeyEnvironment(environment, runtimeMode string) error {
	for _, name := range []string{
		"USDT_SIGNER_TRON_SEED", "USDT_SIGNER_TRON_MNEMONIC", "USDT_SIGNER_TRON_XPRV",
		"USDT_SIGNER_ETHEREUM_SEED", "USDT_SIGNER_ETHEREUM_MNEMONIC", "USDT_SIGNER_ETHEREUM_XPRV",
		"USDT_SIGNER_ETHEREUM_GAS_PRIVATE_KEY", "USDT_SIGNER_PRIVATE_KEY",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return fmt.Errorf("plaintext key environment variable %s is forbidden", name)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("USDT_SIGNER_TEST_SEED_HEX")); raw != "" {
		if strings.EqualFold(strings.TrimSpace(environment), EnvironmentProduction) {
			return fmt.Errorf("plaintext test seed environment variable USDT_SIGNER_TEST_SEED_HEX is forbidden in production")
		}
		if !strings.EqualFold(strings.TrimSpace(runtimeMode), RuntimeModeTestSeed) {
			return fmt.Errorf("USDT_SIGNER_TEST_SEED_HEX is only allowed in test_seed runtime mode")
		}
	}
	return nil
}

func testSeedFromEnv(environment, runtimeMode string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("USDT_SIGNER_TEST_SEED_HEX"))
	if raw == "" {
		return nil, nil
	}
	defer os.Unsetenv("USDT_SIGNER_TEST_SEED_HEX")
	if strings.EqualFold(strings.TrimSpace(environment), EnvironmentProduction) ||
		!strings.EqualFold(strings.TrimSpace(runtimeMode), RuntimeModeTestSeed) {
		return nil, fmt.Errorf("one-time test seed is not allowed by the selected environment and runtime mode")
	}
	seed, err := hex.DecodeString(raw)
	if err != nil || len(seed) != 32 {
		return nil, fmt.Errorf("USDT_SIGNER_TEST_SEED_HEX must contain exactly 64 hexadecimal characters")
	}
	return seed, nil
}

func defaultTPMDevice() string {
	if runtime.GOOS == "windows" {
		return "windows-tbs"
	}
	return "/dev/tpmrm0"
}

func validatePrivateListenAddress(address string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return fmt.Errorf("USDT signer listen address must be an IP host:port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("USDT signer listen host must be a literal private or loopback IP")
	}
	if !ip.IsPrivate() && !ip.IsLoopback() {
		return fmt.Errorf("USDT signer listen host must be a private or loopback IP")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return fmt.Errorf("USDT signer listen port must be between 1 and 65535")
	}
	return nil
}

func parseSPIFFEIdentity(raw string) (string, error) {
	identity, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || identity.Scheme != "spiffe" || identity.Host == "" || identity.Path == "" {
		return "", fmt.Errorf("must be an absolute spiffe URI")
	}
	if identity.User != nil || identity.RawQuery != "" || identity.Fragment != "" {
		return "", fmt.Errorf("must not contain credentials, query, or fragment")
	}
	return identity.String(), nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func splitNonEmpty(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func durationSecondsFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer number of seconds", name)
	}
	return time.Duration(seconds) * time.Second, nil
}
