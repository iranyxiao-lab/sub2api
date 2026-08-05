package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/stretchr/testify/require"
)

func TestLoadSelfHostedTRONDefaultsFailClosed(t *testing.T) {
	resetViperWithJWTSecret(t)

	cfg, err := Load()
	require.NoError(t, err)
	tron := cfg.Onchain.TRON
	require.False(t, tron.Enabled)
	require.False(t, tron.OrderCreationEnabled)
	require.False(t, tron.ScannerEnabled)
	require.False(t, tron.SettlementEnabled)
	require.False(t, tron.SweeperEnabled)
	require.Equal(t, string(onchain.NetworkTronMainnet), tron.Network)
	require.Equal(t, onchain.TronMainnetUSDTContract, tron.USDTContract)
	require.Equal(t, onchain.USDTDecimals, tron.USDTDecimals)
	require.Equal(t, DefaultTRONRequestTimeoutSeconds, tron.RequestTimeoutSeconds)
	require.Equal(t, int64(DefaultTRONResponseMaxBytes), tron.ResponseMaxBytes)
	require.Equal(t, int64(DefaultTRONMaxBlockLag), tron.MaxBlockLag)
	require.Equal(t, DefaultTRONScanBatchSize, tron.ScanBatchSize)
	require.Equal(t, DefaultTRONScanSafetyWindow, tron.ScanSafetyWindow)
	require.Equal(t, DefaultTRONScanLeaseSeconds, tron.ScanLeaseSeconds)
	require.Equal(t, DefaultTRONScanStallAlertSeconds, tron.ScanStallAlertSeconds)
	require.Equal(t, DefaultTRONSettlementBacklogAlert, tron.SettlementBacklogAlert)
	require.Equal(t, DefaultTRONResourceWaitAlert, tron.ResourceWaitAlert)
	require.Equal(t, DefaultTRONReconciliationSeconds, tron.ReconciliationIntervalSeconds)
	require.Equal(t, DefaultTRONSignerTimeoutSeconds, tron.SignerTimeoutSeconds)
	require.Equal(t, DefaultTRONSweepMaxFailures, tron.SweepMaxFailures)
	require.Equal(t, DefaultTRONSweepRetrySeconds, tron.SweepRetrySeconds)
	require.Equal(t, DefaultTRONSweepConfirmSeconds, tron.SweepConfirmSeconds)
	require.Equal(t, DefaultTRONHotWalletWarningRaw, tron.HotWalletWarningRaw)
	require.Equal(t, DefaultTRONHotWalletApprovalRaw, tron.HotWalletApprovalRaw)
	require.Empty(t, tron.XPub)
	require.Empty(t, tron.SweepAddress)
}

func TestLoadSelfHostedEthereumDefaultsFailClosed(t *testing.T) {
	resetViperWithJWTSecret(t)

	cfg, err := Load()
	require.NoError(t, err)
	ethereum := cfg.Onchain.Ethereum
	require.False(t, ethereum.Enabled)
	require.False(t, ethereum.OrderCreationEnabled)
	require.False(t, ethereum.ScannerEnabled)
	require.False(t, ethereum.SettlementEnabled)
	require.False(t, ethereum.SweeperEnabled)
	require.Equal(t, string(onchain.NetworkEthereumMainnet), ethereum.Network)
	require.Equal(t, onchain.EthereumMainnetChainID, ethereum.ChainID)
	require.Equal(t, onchain.EthereumMainnetUSDTContract, ethereum.USDTContract)
	require.Equal(t, onchain.USDTDecimals, ethereum.USDTDecimals)
	require.Equal(t, DefaultEthereumRequestTimeoutSeconds, ethereum.RequestTimeoutSeconds)
	require.Equal(t, int64(DefaultEthereumResponseMaxBytes), ethereum.ResponseMaxBytes)
	require.Equal(t, DefaultEthereumRPCBatchLimit, ethereum.RPCBatchLimit)
	require.Equal(t, uint64(DefaultEthereumRPCMaxLogRange), ethereum.RPCMaxLogRange)
	require.Equal(t, uint64(DefaultEthereumMaxFinalizedLag), ethereum.MaxFinalizedLag)
	require.Equal(t, uint64(DefaultEthereumScanBatchSize), ethereum.ScanBatchSize)
	require.Equal(t, uint64(DefaultEthereumScanSafetyWindow), ethereum.ScanSafetyWindow)
	require.Equal(t, DefaultEthereumScanLeaseSeconds, ethereum.ScanLeaseSeconds)
	require.Equal(t, DefaultEthereumScanStallAlertSeconds, ethereum.ScanStallAlertSeconds)
	require.Equal(t, DefaultEthereumSettlementBacklogAlert, ethereum.SettlementBacklogAlert)
	require.Equal(t, DefaultEthereumPendingNonceAlert, ethereum.PendingNonceAlert)
	require.Equal(t, DefaultEthereumStuckAlertSeconds, ethereum.StuckAlertSeconds)
	require.Equal(t, DefaultEthereumReplacementAlert, ethereum.ReplacementAlert)
	require.Equal(t, DefaultEthereumUnsweptBalanceAlertRaw, ethereum.UnsweptBalanceAlertRaw)
	require.Equal(t, DefaultEthereumGasCostAlertWei, ethereum.GasCostAlertWei)
	require.Equal(t, DefaultEthereumGasSponsorMinWei, ethereum.GasSponsorMinWei)
	require.Equal(t, DefaultEthereumConfigVersion, ethereum.ConfigVersion)
	require.Empty(t, ethereum.PrimaryRPCURL)
	require.Empty(t, ethereum.BackupRPCURL)
	require.Empty(t, ethereum.XPub)
	require.Empty(t, ethereum.SweepAddress)
}

func TestLoadSelfHostedEthereumConfigFromEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("ONCHAIN_ETHEREUM_ENABLED", "true")
	t.Setenv("ONCHAIN_ETHEREUM_ORDER_CREATION_ENABLED", "true")
	t.Setenv("ONCHAIN_ETHEREUM_SCANNER_ENABLED", "true")
	t.Setenv("ONCHAIN_ETHEREUM_SETTLEMENT_ENABLED", "true")
	t.Setenv("ONCHAIN_ETHEREUM_PRIMARY_RPC_URL", " https://geth.internal.example ")
	t.Setenv("ONCHAIN_ETHEREUM_BACKUP_RPC_URL", " https://nethermind.internal.example ")
	xpub, _ := ethereumTestExtendedKeys(t)
	t.Setenv("ONCHAIN_ETHEREUM_XPUB", " "+xpub+" ")
	t.Setenv("ONCHAIN_ETHEREUM_SWEEP_ADDRESS", " 0x0000000000000000000000000000000000000001 ")
	t.Setenv("ONCHAIN_ETHEREUM_RPC_BATCH_LIMIT", "50")
	t.Setenv("ONCHAIN_ETHEREUM_RPC_MAX_LOG_RANGE", "1500")

	cfg, err := Load()
	require.NoError(t, err)
	ethereum := cfg.Onchain.Ethereum
	require.True(t, ethereum.Enabled)
	require.True(t, ethereum.OrderCreationEnabled)
	require.True(t, ethereum.ScannerEnabled)
	require.True(t, ethereum.SettlementEnabled)
	require.Equal(t, "https://geth.internal.example", ethereum.PrimaryRPCURL)
	require.Equal(t, "https://nethermind.internal.example", ethereum.BackupRPCURL)
	require.Equal(t, xpub, ethereum.XPub)
	require.Equal(t, "0x0000000000000000000000000000000000000001", ethereum.SweepAddress)
	require.Equal(t, 50, ethereum.RPCBatchLimit)
	require.Equal(t, uint64(1500), ethereum.RPCMaxLogRange)
}

func TestSelfHostedEthereumConfigValidation(t *testing.T) {
	xpub, xprv := ethereumTestExtendedKeys(t)
	valid := SelfHostedEthereumConfig{
		Enabled: true, OrderCreationEnabled: true, ScannerEnabled: true, SettlementEnabled: true,
		Network: string(onchain.NetworkEthereumMainnet), ChainID: onchain.EthereumMainnetChainID,
		PrimaryRPCURL: "https://geth.internal.example", BackupRPCURL: "https://nethermind.internal.example",
		USDTContract: onchain.EthereumMainnetUSDTContract, USDTDecimals: onchain.USDTDecimals,
		XPub: xpub, SweepAddress: "0x0000000000000000000000000000000000000001",
		RequestTimeoutSeconds:  DefaultEthereumRequestTimeoutSeconds,
		ResponseMaxBytes:       DefaultEthereumResponseMaxBytes,
		RPCBatchLimit:          DefaultEthereumRPCBatchLimit,
		RPCMaxLogRange:         DefaultEthereumRPCMaxLogRange,
		MaxFinalizedLag:        DefaultEthereumMaxFinalizedLag,
		ScanBatchSize:          DefaultEthereumScanBatchSize,
		ScanSafetyWindow:       DefaultEthereumScanSafetyWindow,
		ScanLeaseSeconds:       DefaultEthereumScanLeaseSeconds,
		ScanStallAlertSeconds:  DefaultEthereumScanStallAlertSeconds,
		SettlementBacklogAlert: DefaultEthereumSettlementBacklogAlert,
		PendingNonceAlert:      DefaultEthereumPendingNonceAlert,
		StuckAlertSeconds:      DefaultEthereumStuckAlertSeconds,
		ReplacementAlert:       DefaultEthereumReplacementAlert,
		UnsweptBalanceAlertRaw: DefaultEthereumUnsweptBalanceAlertRaw,
		GasCostAlertWei:        DefaultEthereumGasCostAlertWei,
		GasSponsorMinWei:       DefaultEthereumGasSponsorMinWei,
		ConfigVersion:          DefaultEthereumConfigVersion,
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name    string
		mutate  func(*SelfHostedEthereumConfig)
		message string
	}{
		{name: "operation without master switch", mutate: func(c *SelfHostedEthereumConfig) { c.Enabled = false }, message: "enabled must be true"},
		{name: "orders without scanner", mutate: func(c *SelfHostedEthereumConfig) { c.ScannerEnabled = false }, message: "requires scanner and settlement"},
		{name: "wrong chain ID", mutate: func(c *SelfHostedEthereumConfig) { c.ChainID = 2 }, message: "chain ID"},
		{name: "wrong contract", mutate: func(c *SelfHostedEthereumConfig) { c.USDTContract = c.SweepAddress }, message: "allowlist"},
		{name: "insecure primary", mutate: func(c *SelfHostedEthereumConfig) { c.PrimaryRPCURL = "http://geth.internal" }, message: "must use HTTPS"},
		{name: "URL credentials", mutate: func(c *SelfHostedEthereumConfig) { c.PrimaryRPCURL = "https://user:pass@geth.internal" }, message: "credentials"},
		{name: "duplicate endpoints", mutate: func(c *SelfHostedEthereumConfig) { c.BackupRPCURL = c.PrimaryRPCURL }, message: "must differ"},
		{name: "private extended key", mutate: func(c *SelfHostedEthereumConfig) { c.XPub = xprv }, message: "private extended keys"},
		{name: "bad sweep address", mutate: func(c *SelfHostedEthereumConfig) { c.SweepAddress = "not-an-address" }, message: "sweep_address"},
		{name: "timeout too small", mutate: func(c *SelfHostedEthereumConfig) { c.RequestTimeoutSeconds = 0 }, message: "request_timeout_seconds"},
		{name: "response too large", mutate: func(c *SelfHostedEthereumConfig) { c.ResponseMaxBytes = 65 * 1024 * 1024 }, message: "response_max_bytes"},
		{name: "zero batch limit", mutate: func(c *SelfHostedEthereumConfig) { c.RPCBatchLimit = 0 }, message: "rpc_batch_limit"},
		{name: "zero log range", mutate: func(c *SelfHostedEthereumConfig) { c.RPCMaxLogRange = 0 }, message: "rpc_max_log_range"},
		{name: "batch exceeds log range", mutate: func(c *SelfHostedEthereumConfig) { c.ScanBatchSize = c.RPCMaxLogRange + 1 }, message: "scan_batch_size"},
		{name: "zero safety window", mutate: func(c *SelfHostedEthereumConfig) { c.ScanSafetyWindow = 0 }, message: "scan_safety_window"},
		{name: "short lease", mutate: func(c *SelfHostedEthereumConfig) { c.ScanLeaseSeconds = 9 }, message: "scan_lease_seconds"},
		{name: "short stall alert", mutate: func(c *SelfHostedEthereumConfig) { c.ScanStallAlertSeconds = 29 }, message: "scan_stall_alert_seconds"},
		{name: "zero settlement backlog alert", mutate: func(c *SelfHostedEthereumConfig) { c.SettlementBacklogAlert = 0 }, message: "settlement_backlog_alert_count"},
		{name: "zero pending nonce alert", mutate: func(c *SelfHostedEthereumConfig) { c.PendingNonceAlert = 0 }, message: "pending_nonce_alert_count"},
		{name: "short stuck alert", mutate: func(c *SelfHostedEthereumConfig) { c.StuckAlertSeconds = 59 }, message: "stuck_transaction_alert_seconds"},
		{name: "zero replacement alert", mutate: func(c *SelfHostedEthereumConfig) { c.ReplacementAlert = 0 }, message: "replacement_transaction_alert_count"},
		{name: "invalid unswept alert", mutate: func(c *SelfHostedEthereumConfig) { c.UnsweptBalanceAlertRaw = "0200000000" }, message: "unswept_balance_alert_raw"},
		{name: "invalid gas cost alert", mutate: func(c *SelfHostedEthereumConfig) { c.GasCostAlertWei = "-1" }, message: "gas_cost_alert_wei"},
		{name: "invalid sponsor balance alert", mutate: func(c *SelfHostedEthereumConfig) { c.GasSponsorMinWei = "" }, message: "gas_sponsor_min_balance_wei"},
		{name: "missing config version", mutate: func(c *SelfHostedEthereumConfig) { c.ConfigVersion = "" }, message: "config_version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := valid
			tt.mutate(&candidate)
			require.ErrorContains(t, candidate.Validate(), tt.message)
		})
	}

	testnet := valid
	testnet.Network = string(onchain.NetworkEthereumSepolia)
	testnet.ChainID = onchain.EthereumSepoliaChainID
	testnet.PrimaryRPCURL = "http://127.0.0.1:8545"
	testnet.BackupRPCURL = "http://127.0.0.1:9545"
	testnet.USDTContract = "0x0000000000000000000000000000000000000002"
	require.NoError(t, testnet.Validate())
}

func TestLoadSelfHostedTRONConfigFromEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("ONCHAIN_TRON_ENABLED", "true")
	t.Setenv("ONCHAIN_TRON_ORDER_CREATION_ENABLED", "true")
	t.Setenv("ONCHAIN_TRON_SCANNER_ENABLED", "true")
	t.Setenv("ONCHAIN_TRON_SETTLEMENT_ENABLED", "true")
	t.Setenv("ONCHAIN_TRON_FULL_NODE_URL", " https://fullnode.internal.example ")
	t.Setenv("ONCHAIN_TRON_SOLIDITY_NODE_URL", " https://solidity.internal.example ")
	t.Setenv("ONCHAIN_TRON_SOLIDITY_NODE_FALLBACK_URL", " https://solidity-backup.internal.example ")
	xpub, _ := tronTestExtendedKeys(t)
	t.Setenv("ONCHAIN_TRON_XPUB", " "+xpub+" ")
	t.Setenv("ONCHAIN_TRON_SWEEP_ADDRESS", " "+onchain.TronMainnetUSDTContract+" ")
	t.Setenv("ONCHAIN_TRON_REQUEST_TIMEOUT_SECONDS", "15")
	t.Setenv("ONCHAIN_TRON_MAX_BLOCK_LAG", "25")

	cfg, err := Load()
	require.NoError(t, err)
	tron := cfg.Onchain.TRON
	require.True(t, tron.Enabled)
	require.True(t, tron.OrderCreationEnabled)
	require.True(t, tron.ScannerEnabled)
	require.Equal(t, "https://fullnode.internal.example", tron.FullNodeURL)
	require.Equal(t, "https://solidity.internal.example", tron.SolidityNodeURL)
	require.Equal(t, "https://solidity-backup.internal.example", tron.SolidityNodeFallbackURL)
	require.Equal(t, xpub, tron.XPub)
	require.Equal(t, onchain.TronMainnetUSDTContract, tron.SweepAddress)
	require.Equal(t, 15, tron.RequestTimeoutSeconds)
	require.Equal(t, int64(25), tron.MaxBlockLag)
}

func TestSelfHostedTRONConfigValidation(t *testing.T) {
	xpub, xprv := tronTestExtendedKeys(t)
	valid := SelfHostedTRONConfig{
		Enabled:                       true,
		OrderCreationEnabled:          true,
		ScannerEnabled:                true,
		SettlementEnabled:             true,
		SweeperEnabled:                true,
		Network:                       string(onchain.NetworkTronMainnet),
		FullNodeURL:                   "https://fullnode.internal.example",
		SolidityNodeURL:               "https://solidity.internal.example",
		USDTContract:                  onchain.TronMainnetUSDTContract,
		USDTDecimals:                  onchain.USDTDecimals,
		XPub:                          xpub,
		SweepAddress:                  onchain.TronMainnetUSDTContract,
		RequestTimeoutSeconds:         DefaultTRONRequestTimeoutSeconds,
		ResponseMaxBytes:              DefaultTRONResponseMaxBytes,
		MaxBlockLag:                   DefaultTRONMaxBlockLag,
		ScanBatchSize:                 DefaultTRONScanBatchSize,
		ScanSafetyWindow:              DefaultTRONScanSafetyWindow,
		ScanLeaseSeconds:              DefaultTRONScanLeaseSeconds,
		ScanStallAlertSeconds:         DefaultTRONScanStallAlertSeconds,
		SettlementBacklogAlert:        DefaultTRONSettlementBacklogAlert,
		ResourceWaitAlert:             DefaultTRONResourceWaitAlert,
		ReconciliationIntervalSeconds: DefaultTRONReconciliationSeconds,
		SweepMinimumAmountRaw:         DefaultTRONSweepMinimumRaw,
		SweepRequiredEnergy:           DefaultTRONSweepRequiredEnergy,
		SweepRequiredBandwidth:        DefaultTRONSweepRequiredBandwidth,
		SweepMinimumTRXSun:            DefaultTRONSweepMinimumTRXSun,
		SweepBatchSize:                DefaultTRONSweepBatchSize,
		SignerURL:                     "https://usdt-signer.internal.example",
		SignerClientCertFile:          "/run/secrets/sweeper-client.crt",
		SignerClientKeyFile:           "/run/secrets/sweeper-client.key",
		SignerServerCAFile:            "/run/secrets/signer-ca.crt",
		SignerServerIdentityURI:       "spiffe://sub2api.internal/usdt-signer",
		SignerTimeoutSeconds:          DefaultTRONSignerTimeoutSeconds,
		SweepMaxFailures:              DefaultTRONSweepMaxFailures,
		SweepRetrySeconds:             DefaultTRONSweepRetrySeconds,
		SweepConfirmSeconds:           DefaultTRONSweepConfirmSeconds,
		HotWalletWarningRaw:           DefaultTRONHotWalletWarningRaw,
		HotWalletApprovalRaw:          DefaultTRONHotWalletApprovalRaw,
		ConfigVersion:                 DefaultTRONConfigVersion,
	}
	require.NoError(t, valid.Validate())

	safeRollback := valid
	safeRollback.OrderCreationEnabled = false
	safeRollback.SweeperEnabled = false
	safeRollback.SignerURL = ""
	safeRollback.SignerClientCertFile = ""
	safeRollback.SignerClientKeyFile = ""
	safeRollback.SignerServerCAFile = ""
	safeRollback.SignerServerIdentityURI = ""
	require.True(t, safeRollback.ScannerEnabled)
	require.True(t, safeRollback.SettlementEnabled)
	require.NoError(t, safeRollback.Validate(), "safe rollback keeps intake processing active without requiring signer access")

	tests := []struct {
		name    string
		mutate  func(*SelfHostedTRONConfig)
		message string
	}{
		{name: "operation without master switch", mutate: func(c *SelfHostedTRONConfig) { c.Enabled = false }, message: "enabled must be true"},
		{name: "orders without scanner", mutate: func(c *SelfHostedTRONConfig) { c.ScannerEnabled = false }, message: "requires scanner and settlement"},
		{name: "orders without settlement", mutate: func(c *SelfHostedTRONConfig) { c.SettlementEnabled = false }, message: "requires scanner and settlement"},
		{name: "unsupported network", mutate: func(c *SelfHostedTRONConfig) { c.Network = string(onchain.NetworkEthereumMainnet) }, message: "tron-mainnet or tron-nile"},
		{name: "insecure mainnet node", mutate: func(c *SelfHostedTRONConfig) { c.FullNodeURL = "http://fullnode.internal" }, message: "must use HTTPS"},
		{name: "url credentials", mutate: func(c *SelfHostedTRONConfig) { c.FullNodeURL = "https://user:pass@fullnode.internal" }, message: "must not contain URL credentials"},
		{name: "insecure fallback node", mutate: func(c *SelfHostedTRONConfig) { c.SolidityNodeFallbackURL = "http://fallback.internal" }, message: "must use HTTPS"},
		{name: "duplicate fallback node", mutate: func(c *SelfHostedTRONConfig) { c.SolidityNodeFallbackURL = c.SolidityNodeURL }, message: "must differ"},
		{name: "wrong contract", mutate: func(c *SelfHostedTRONConfig) { c.USDTContract = "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8" }, message: "allowlist"},
		{name: "private extended key", mutate: func(c *SelfHostedTRONConfig) { c.XPub = xprv }, message: "private extended keys are forbidden"},
		{name: "bad sweep address", mutate: func(c *SelfHostedTRONConfig) { c.SweepAddress = "not-an-address" }, message: "sweep_address"},
		{name: "timeout too small", mutate: func(c *SelfHostedTRONConfig) { c.RequestTimeoutSeconds = 0 }, message: "between 1 and 60"},
		{name: "response too large", mutate: func(c *SelfHostedTRONConfig) { c.ResponseMaxBytes = 65 * 1024 * 1024 }, message: "between 1024"},
		{name: "negative block lag", mutate: func(c *SelfHostedTRONConfig) { c.MaxBlockLag = -1 }, message: "between 0 and 10000"},
		{name: "zero scan batch", mutate: func(c *SelfHostedTRONConfig) { c.ScanBatchSize = 0 }, message: "scan_batch_size"},
		{name: "zero safety window", mutate: func(c *SelfHostedTRONConfig) { c.ScanSafetyWindow = 0 }, message: "scan_safety_window"},
		{name: "short scan lease", mutate: func(c *SelfHostedTRONConfig) { c.ScanLeaseSeconds = 9 }, message: "scan_lease_seconds"},
		{name: "short scan stall alert", mutate: func(c *SelfHostedTRONConfig) { c.ScanStallAlertSeconds = 29 }, message: "scan_stall_alert_seconds"},
		{name: "zero settlement backlog alert", mutate: func(c *SelfHostedTRONConfig) { c.SettlementBacklogAlert = 0 }, message: "settlement_backlog_alert_count"},
		{name: "zero resource wait alert", mutate: func(c *SelfHostedTRONConfig) { c.ResourceWaitAlert = 0 }, message: "resource_wait_alert_count"},
		{name: "short reconciliation interval", mutate: func(c *SelfHostedTRONConfig) { c.ReconciliationIntervalSeconds = 9 }, message: "reconciliation_interval_seconds"},
		{name: "invalid minimum sweep", mutate: func(c *SelfHostedTRONConfig) { c.SweepMinimumAmountRaw = "050000000" }, message: "sweep_minimum_amount_raw"},
		{name: "negative required energy", mutate: func(c *SelfHostedTRONConfig) { c.SweepRequiredEnergy = -1 }, message: "resource thresholds"},
		{name: "zero sweep batch", mutate: func(c *SelfHostedTRONConfig) { c.SweepBatchSize = 0 }, message: "sweep_batch_size"},
		{name: "insecure signer URL", mutate: func(c *SelfHostedTRONConfig) { c.SignerURL = "http://usdt-signer.internal" }, message: "signer_url"},
		{name: "missing signer client certificate", mutate: func(c *SelfHostedTRONConfig) { c.SignerClientCertFile = "" }, message: "certificate"},
		{name: "invalid signer identity", mutate: func(c *SelfHostedTRONConfig) { c.SignerServerIdentityURI = "https://usdt-signer.internal" }, message: "SPIFFE"},
		{name: "zero signer timeout", mutate: func(c *SelfHostedTRONConfig) { c.SignerTimeoutSeconds = 0 }, message: "signer_timeout_seconds"},
		{name: "zero max sweep failures", mutate: func(c *SelfHostedTRONConfig) { c.SweepMaxFailures = 0 }, message: "sweep_max_consecutive_failures"},
		{name: "zero sweep retry backoff", mutate: func(c *SelfHostedTRONConfig) { c.SweepRetrySeconds = 0 }, message: "sweep_retry_backoff_seconds"},
		{name: "zero sweep confirmation poll", mutate: func(c *SelfHostedTRONConfig) { c.SweepConfirmSeconds = 0 }, message: "sweep_confirmation_poll_seconds"},
		{name: "invalid hot wallet warning", mutate: func(c *SelfHostedTRONConfig) { c.HotWalletWarningRaw = "050000000000" }, message: "hot_wallet_warning_amount_raw"},
		{name: "hot wallet thresholds reversed", mutate: func(c *SelfHostedTRONConfig) { c.HotWalletWarningRaw = c.HotWalletApprovalRaw }, message: "must be below"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := valid
			tt.mutate(&candidate)
			require.ErrorContains(t, candidate.Validate(), tt.message)
		})
	}

	testnet := valid
	testnet.Network = string(onchain.NetworkTronNile)
	testnet.FullNodeURL = "http://127.0.0.1:8090"
	testnet.SolidityNodeURL = "http://127.0.0.1:8091"
	testnet.USDTContract = "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"
	testnet.SweepAddress = "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"
	require.NoError(t, testnet.Validate())
}

func TestSelfHostedTRONConfigCannotCarryPrivateKeyMaterial(t *testing.T) {
	forbidden := []string{"private", "mnemonic", "seed", "xprv", "secret"}
	typ := reflect.TypeOf(SelfHostedTRONConfig{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.ToLower(field.Name + " " + field.Tag.Get("mapstructure"))
		for _, token := range forbidden {
			require.NotContainsf(t, name, token, "spending-key field %q must not exist in business-process config", field.Name)
		}
	}
}

func TestSelfHostedEthereumConfigCannotCarryPrivateKeyMaterial(t *testing.T) {
	forbidden := []string{"private", "mnemonic", "seed", "xprv", "secret"}
	typ := reflect.TypeOf(SelfHostedEthereumConfig{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.ToLower(field.Name + " " + field.Tag.Get("mapstructure"))
		for _, token := range forbidden {
			require.NotContainsf(t, name, token, "spending-key field %q must not exist in business-process config", field.Name)
		}
	}
}

func TestOnchainConfigRejectsTRONAndEthereumXPubReuse(t *testing.T) {
	const reused = "same-public-wallet-material"
	err := (OnchainConfig{
		TRON:     SelfHostedTRONConfig{Enabled: true, XPub: reused},
		Ethereum: SelfHostedEthereumConfig{Enabled: true, XPub: reused},
	}).Validate()
	require.ErrorContains(t, err, "must use different extended public keys")
}

func tronTestExtendedKeys(t *testing.T) (xpub, xprv string) {
	t.Helper()
	key, err := hdkeychain.NewMaster([]byte("0123456789abcdef0123456789abcdef"), &chaincfg.MainNetParams)
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
	xprv = key.String()
	publicKey, err := key.Neuter()
	require.NoError(t, err)
	defer publicKey.Zero()
	return publicKey.String(), xprv
}

func ethereumTestExtendedKeys(t *testing.T) (xpub, xprv string) {
	t.Helper()
	key, err := hdkeychain.NewMaster([]byte("ethereum-config-test-seed-32b!!"), &chaincfg.MainNetParams)
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
	xprv = key.String()
	publicKey, err := key.Neuter()
	require.NoError(t, err)
	defer publicKey.Zero()
	return publicKey.String(), xprv
}
