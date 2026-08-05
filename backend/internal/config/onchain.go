package config

import (
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/spf13/viper"
)

const (
	DefaultTRONRequestTimeoutSeconds      = 10
	DefaultTRONResponseMaxBytes           = 8 * 1024 * 1024
	DefaultTRONMaxBlockLag                = 20
	DefaultTRONScanBatchSize              = 100
	DefaultTRONScanSafetyWindow           = 20
	DefaultTRONScanLeaseSeconds           = 30
	DefaultTRONScanStallAlertSeconds      = 120
	DefaultTRONSettlementBacklogAlert     = 100
	DefaultTRONResourceWaitAlert          = 1
	DefaultTRONReconciliationSeconds      = 300
	DefaultTRONConfigVersion              = "tron-config-v1"
	DefaultTRONSweepMinimumRaw            = "50000000"
	DefaultTRONSweepRequiredEnergy        = int64(130000)
	DefaultTRONSweepRequiredBandwidth     = int64(400)
	DefaultTRONSweepMinimumTRXSun         = int64(100000000)
	DefaultTRONSweepBatchSize             = 100
	DefaultTRONSignerTimeoutSeconds       = 15
	DefaultTRONSweepMaxFailures           = 3
	DefaultTRONSweepRetrySeconds          = 30
	DefaultTRONSweepConfirmSeconds        = 30
	DefaultTRONHotWalletWarningRaw        = "50000000000"
	DefaultTRONHotWalletApprovalRaw       = "100000000000"
	DefaultEthereumRequestTimeoutSeconds  = 10
	DefaultEthereumResponseMaxBytes       = 8 * 1024 * 1024
	DefaultEthereumRPCBatchLimit          = 100
	DefaultEthereumRPCMaxLogRange         = 2000
	DefaultEthereumMaxFinalizedLag        = 64
	DefaultEthereumScanBatchSize          = 1000
	DefaultEthereumScanSafetyWindow       = 64
	DefaultEthereumScanLeaseSeconds       = 30
	DefaultEthereumScanStallAlertSeconds  = 180
	DefaultEthereumSettlementBacklogAlert = 100
	DefaultEthereumPendingNonceAlert      = 1
	DefaultEthereumStuckAlertSeconds      = 900
	DefaultEthereumReplacementAlert       = 1
	DefaultEthereumUnsweptBalanceAlertRaw = "200000000"
	DefaultEthereumGasCostAlertWei        = "100000000000000000"
	DefaultEthereumGasSponsorMinWei       = "10000000000000000"
	DefaultEthereumConfigVersion          = "ethereum-config-v1"
)

type OnchainConfig struct {
	TRON     SelfHostedTRONConfig     `mapstructure:"tron"`
	Ethereum SelfHostedEthereumConfig `mapstructure:"ethereum"`
}

// SelfHostedTRONConfig contains only public wallet material and operational
// controls. Spending keys belong exclusively to the separately deployed signer.
type SelfHostedTRONConfig struct {
	Enabled                       bool   `mapstructure:"enabled"`
	OrderCreationEnabled          bool   `mapstructure:"order_creation_enabled"`
	ScannerEnabled                bool   `mapstructure:"scanner_enabled"`
	SettlementEnabled             bool   `mapstructure:"settlement_enabled"`
	SweeperEnabled                bool   `mapstructure:"sweeper_enabled"`
	Network                       string `mapstructure:"network"`
	FullNodeURL                   string `mapstructure:"full_node_url"`
	SolidityNodeURL               string `mapstructure:"solidity_node_url"`
	SolidityNodeFallbackURL       string `mapstructure:"solidity_node_fallback_url"`
	USDTContract                  string `mapstructure:"usdt_contract"`
	USDTDecimals                  uint8  `mapstructure:"usdt_decimals"`
	XPub                          string `mapstructure:"xpub"`
	SweepAddress                  string `mapstructure:"sweep_address"`
	RequestTimeoutSeconds         int    `mapstructure:"request_timeout_seconds"`
	ResponseMaxBytes              int64  `mapstructure:"response_max_bytes"`
	MaxBlockLag                   int64  `mapstructure:"max_block_lag"`
	ScanBatchSize                 int    `mapstructure:"scan_batch_size"`
	ScanSafetyWindow              int    `mapstructure:"scan_safety_window"`
	ScanLeaseSeconds              int    `mapstructure:"scan_lease_seconds"`
	ScanStallAlertSeconds         int    `mapstructure:"scan_stall_alert_seconds"`
	SettlementBacklogAlert        int    `mapstructure:"settlement_backlog_alert_count"`
	ResourceWaitAlert             int    `mapstructure:"resource_wait_alert_count"`
	ReconciliationIntervalSeconds int    `mapstructure:"reconciliation_interval_seconds"`
	SweepMinimumAmountRaw         string `mapstructure:"sweep_minimum_amount_raw"`
	SweepRequiredEnergy           int64  `mapstructure:"sweep_required_energy"`
	SweepRequiredBandwidth        int64  `mapstructure:"sweep_required_bandwidth"`
	SweepMinimumTRXSun            int64  `mapstructure:"sweep_minimum_trx_sun"`
	SweepBatchSize                int    `mapstructure:"sweep_batch_size"`
	SignerURL                     string `mapstructure:"signer_url"`
	SignerClientCertFile          string `mapstructure:"signer_client_cert_file"`
	SignerClientKeyFile           string `mapstructure:"signer_client_key_file"`
	SignerServerCAFile            string `mapstructure:"signer_server_ca_file"`
	SignerServerIdentityURI       string `mapstructure:"signer_server_identity_uri"`
	SignerTimeoutSeconds          int    `mapstructure:"signer_timeout_seconds"`
	SweepMaxFailures              int    `mapstructure:"sweep_max_consecutive_failures"`
	SweepRetrySeconds             int    `mapstructure:"sweep_retry_backoff_seconds"`
	SweepConfirmSeconds           int    `mapstructure:"sweep_confirmation_poll_seconds"`
	HotWalletWarningRaw           string `mapstructure:"hot_wallet_warning_amount_raw"`
	HotWalletApprovalRaw          string `mapstructure:"hot_wallet_cold_approval_amount_raw"`
	ConfigVersion                 string `mapstructure:"config_version"`
}

// SelfHostedEthereumConfig contains only public wallet material and read-only
// execution-node settings. Spending keys belong exclusively to usdt-signer.
type SelfHostedEthereumConfig struct {
	Enabled                bool   `mapstructure:"enabled"`
	OrderCreationEnabled   bool   `mapstructure:"order_creation_enabled"`
	ScannerEnabled         bool   `mapstructure:"scanner_enabled"`
	SettlementEnabled      bool   `mapstructure:"settlement_enabled"`
	SweeperEnabled         bool   `mapstructure:"sweeper_enabled"`
	Network                string `mapstructure:"network"`
	ChainID                uint64 `mapstructure:"chain_id"`
	PrimaryRPCURL          string `mapstructure:"primary_rpc_url"`
	BackupRPCURL           string `mapstructure:"backup_rpc_url"`
	USDTContract           string `mapstructure:"usdt_contract"`
	USDTDecimals           uint8  `mapstructure:"usdt_decimals"`
	XPub                   string `mapstructure:"xpub"`
	SweepAddress           string `mapstructure:"sweep_address"`
	RequestTimeoutSeconds  int    `mapstructure:"request_timeout_seconds"`
	ResponseMaxBytes       int64  `mapstructure:"response_max_bytes"`
	RPCBatchLimit          int    `mapstructure:"rpc_batch_limit"`
	RPCMaxLogRange         uint64 `mapstructure:"rpc_max_log_range"`
	MaxFinalizedLag        uint64 `mapstructure:"max_finalized_lag"`
	ScanBatchSize          uint64 `mapstructure:"scan_batch_size"`
	ScanSafetyWindow       uint64 `mapstructure:"scan_safety_window"`
	ScanLeaseSeconds       int    `mapstructure:"scan_lease_seconds"`
	ScanStallAlertSeconds  int    `mapstructure:"scan_stall_alert_seconds"`
	SettlementBacklogAlert int    `mapstructure:"settlement_backlog_alert_count"`
	PendingNonceAlert      int    `mapstructure:"pending_nonce_alert_count"`
	StuckAlertSeconds      int    `mapstructure:"stuck_transaction_alert_seconds"`
	ReplacementAlert       int    `mapstructure:"replacement_transaction_alert_count"`
	UnsweptBalanceAlertRaw string `mapstructure:"unswept_balance_alert_raw"`
	GasCostAlertWei        string `mapstructure:"gas_cost_alert_wei"`
	GasSponsorMinWei       string `mapstructure:"gas_sponsor_min_balance_wei"`
	ConfigVersion          string `mapstructure:"config_version"`
}

func setOnchainDefaults() {
	viper.SetDefault("onchain.tron.enabled", false)
	viper.SetDefault("onchain.tron.order_creation_enabled", false)
	viper.SetDefault("onchain.tron.scanner_enabled", false)
	viper.SetDefault("onchain.tron.settlement_enabled", false)
	viper.SetDefault("onchain.tron.sweeper_enabled", false)
	viper.SetDefault("onchain.tron.network", string(onchain.NetworkTronMainnet))
	viper.SetDefault("onchain.tron.full_node_url", "")
	viper.SetDefault("onchain.tron.solidity_node_url", "")
	viper.SetDefault("onchain.tron.solidity_node_fallback_url", "")
	viper.SetDefault("onchain.tron.usdt_contract", onchain.TronMainnetUSDTContract)
	viper.SetDefault("onchain.tron.usdt_decimals", onchain.USDTDecimals)
	viper.SetDefault("onchain.tron.xpub", "")
	viper.SetDefault("onchain.tron.sweep_address", "")
	viper.SetDefault("onchain.tron.request_timeout_seconds", DefaultTRONRequestTimeoutSeconds)
	viper.SetDefault("onchain.tron.response_max_bytes", int64(DefaultTRONResponseMaxBytes))
	viper.SetDefault("onchain.tron.max_block_lag", int64(DefaultTRONMaxBlockLag))
	viper.SetDefault("onchain.tron.scan_batch_size", DefaultTRONScanBatchSize)
	viper.SetDefault("onchain.tron.scan_safety_window", DefaultTRONScanSafetyWindow)
	viper.SetDefault("onchain.tron.scan_lease_seconds", DefaultTRONScanLeaseSeconds)
	viper.SetDefault("onchain.tron.scan_stall_alert_seconds", DefaultTRONScanStallAlertSeconds)
	viper.SetDefault("onchain.tron.settlement_backlog_alert_count", DefaultTRONSettlementBacklogAlert)
	viper.SetDefault("onchain.tron.resource_wait_alert_count", DefaultTRONResourceWaitAlert)
	viper.SetDefault("onchain.tron.reconciliation_interval_seconds", DefaultTRONReconciliationSeconds)
	viper.SetDefault("onchain.tron.sweep_minimum_amount_raw", DefaultTRONSweepMinimumRaw)
	viper.SetDefault("onchain.tron.sweep_required_energy", DefaultTRONSweepRequiredEnergy)
	viper.SetDefault("onchain.tron.sweep_required_bandwidth", DefaultTRONSweepRequiredBandwidth)
	viper.SetDefault("onchain.tron.sweep_minimum_trx_sun", DefaultTRONSweepMinimumTRXSun)
	viper.SetDefault("onchain.tron.sweep_batch_size", DefaultTRONSweepBatchSize)
	viper.SetDefault("onchain.tron.signer_url", "")
	viper.SetDefault("onchain.tron.signer_client_cert_file", "")
	viper.SetDefault("onchain.tron.signer_client_key_file", "")
	viper.SetDefault("onchain.tron.signer_server_ca_file", "")
	viper.SetDefault("onchain.tron.signer_server_identity_uri", "")
	viper.SetDefault("onchain.tron.signer_timeout_seconds", DefaultTRONSignerTimeoutSeconds)
	viper.SetDefault("onchain.tron.sweep_max_consecutive_failures", DefaultTRONSweepMaxFailures)
	viper.SetDefault("onchain.tron.sweep_retry_backoff_seconds", DefaultTRONSweepRetrySeconds)
	viper.SetDefault("onchain.tron.sweep_confirmation_poll_seconds", DefaultTRONSweepConfirmSeconds)
	viper.SetDefault("onchain.tron.hot_wallet_warning_amount_raw", DefaultTRONHotWalletWarningRaw)
	viper.SetDefault("onchain.tron.hot_wallet_cold_approval_amount_raw", DefaultTRONHotWalletApprovalRaw)
	viper.SetDefault("onchain.tron.config_version", DefaultTRONConfigVersion)

	viper.SetDefault("onchain.ethereum.enabled", false)
	viper.SetDefault("onchain.ethereum.order_creation_enabled", false)
	viper.SetDefault("onchain.ethereum.scanner_enabled", false)
	viper.SetDefault("onchain.ethereum.settlement_enabled", false)
	viper.SetDefault("onchain.ethereum.sweeper_enabled", false)
	viper.SetDefault("onchain.ethereum.network", string(onchain.NetworkEthereumMainnet))
	viper.SetDefault("onchain.ethereum.chain_id", onchain.EthereumMainnetChainID)
	viper.SetDefault("onchain.ethereum.primary_rpc_url", "")
	viper.SetDefault("onchain.ethereum.backup_rpc_url", "")
	viper.SetDefault("onchain.ethereum.usdt_contract", onchain.EthereumMainnetUSDTContract)
	viper.SetDefault("onchain.ethereum.usdt_decimals", onchain.USDTDecimals)
	viper.SetDefault("onchain.ethereum.xpub", "")
	viper.SetDefault("onchain.ethereum.sweep_address", "")
	viper.SetDefault("onchain.ethereum.request_timeout_seconds", DefaultEthereumRequestTimeoutSeconds)
	viper.SetDefault("onchain.ethereum.response_max_bytes", int64(DefaultEthereumResponseMaxBytes))
	viper.SetDefault("onchain.ethereum.rpc_batch_limit", DefaultEthereumRPCBatchLimit)
	viper.SetDefault("onchain.ethereum.rpc_max_log_range", uint64(DefaultEthereumRPCMaxLogRange))
	viper.SetDefault("onchain.ethereum.max_finalized_lag", uint64(DefaultEthereumMaxFinalizedLag))
	viper.SetDefault("onchain.ethereum.scan_batch_size", uint64(DefaultEthereumScanBatchSize))
	viper.SetDefault("onchain.ethereum.scan_safety_window", uint64(DefaultEthereumScanSafetyWindow))
	viper.SetDefault("onchain.ethereum.scan_lease_seconds", DefaultEthereumScanLeaseSeconds)
	viper.SetDefault("onchain.ethereum.scan_stall_alert_seconds", DefaultEthereumScanStallAlertSeconds)
	viper.SetDefault("onchain.ethereum.settlement_backlog_alert_count", DefaultEthereumSettlementBacklogAlert)
	viper.SetDefault("onchain.ethereum.pending_nonce_alert_count", DefaultEthereumPendingNonceAlert)
	viper.SetDefault("onchain.ethereum.stuck_transaction_alert_seconds", DefaultEthereumStuckAlertSeconds)
	viper.SetDefault("onchain.ethereum.replacement_transaction_alert_count", DefaultEthereumReplacementAlert)
	viper.SetDefault("onchain.ethereum.unswept_balance_alert_raw", DefaultEthereumUnsweptBalanceAlertRaw)
	viper.SetDefault("onchain.ethereum.gas_cost_alert_wei", DefaultEthereumGasCostAlertWei)
	viper.SetDefault("onchain.ethereum.gas_sponsor_min_balance_wei", DefaultEthereumGasSponsorMinWei)
	viper.SetDefault("onchain.ethereum.config_version", DefaultEthereumConfigVersion)
}

func normalizeOnchainConfig(cfg *OnchainConfig) {
	tron := &cfg.TRON
	tron.Network = strings.ToLower(strings.TrimSpace(tron.Network))
	tron.FullNodeURL = strings.TrimSpace(tron.FullNodeURL)
	tron.SolidityNodeURL = strings.TrimSpace(tron.SolidityNodeURL)
	tron.SolidityNodeFallbackURL = strings.TrimSpace(tron.SolidityNodeFallbackURL)
	tron.USDTContract = strings.TrimSpace(tron.USDTContract)
	tron.XPub = strings.TrimSpace(tron.XPub)
	tron.SweepAddress = strings.TrimSpace(tron.SweepAddress)
	tron.SweepMinimumAmountRaw = strings.TrimSpace(tron.SweepMinimumAmountRaw)
	tron.SignerURL = strings.TrimSpace(tron.SignerURL)
	tron.SignerClientCertFile = strings.TrimSpace(tron.SignerClientCertFile)
	tron.SignerClientKeyFile = strings.TrimSpace(tron.SignerClientKeyFile)
	tron.SignerServerCAFile = strings.TrimSpace(tron.SignerServerCAFile)
	tron.SignerServerIdentityURI = strings.TrimSpace(tron.SignerServerIdentityURI)
	tron.HotWalletWarningRaw = strings.TrimSpace(tron.HotWalletWarningRaw)
	tron.HotWalletApprovalRaw = strings.TrimSpace(tron.HotWalletApprovalRaw)
	tron.ConfigVersion = strings.TrimSpace(tron.ConfigVersion)

	ethereum := &cfg.Ethereum
	ethereum.Network = strings.ToLower(strings.TrimSpace(ethereum.Network))
	ethereum.PrimaryRPCURL = strings.TrimSpace(ethereum.PrimaryRPCURL)
	ethereum.BackupRPCURL = strings.TrimSpace(ethereum.BackupRPCURL)
	ethereum.USDTContract = strings.TrimSpace(ethereum.USDTContract)
	ethereum.XPub = strings.TrimSpace(ethereum.XPub)
	ethereum.SweepAddress = strings.TrimSpace(ethereum.SweepAddress)
	ethereum.UnsweptBalanceAlertRaw = strings.TrimSpace(ethereum.UnsweptBalanceAlertRaw)
	ethereum.GasCostAlertWei = strings.TrimSpace(ethereum.GasCostAlertWei)
	ethereum.GasSponsorMinWei = strings.TrimSpace(ethereum.GasSponsorMinWei)
	ethereum.ConfigVersion = strings.TrimSpace(ethereum.ConfigVersion)
}

func (c OnchainConfig) Validate() error {
	if c.TRON.Enabled && c.Ethereum.Enabled && strings.TrimSpace(c.TRON.XPub) != "" && strings.TrimSpace(c.TRON.XPub) == strings.TrimSpace(c.Ethereum.XPub) {
		return fmt.Errorf("onchain.tron.xpub and onchain.ethereum.xpub must use different extended public keys")
	}
	if err := c.TRON.Validate(); err != nil {
		return err
	}
	return c.Ethereum.Validate()
}

func (c SelfHostedEthereumConfig) Validate() error {
	anyOperationEnabled := c.OrderCreationEnabled || c.ScannerEnabled || c.SettlementEnabled || c.SweeperEnabled
	if anyOperationEnabled && !c.Enabled {
		return fmt.Errorf("onchain.ethereum.enabled must be true when an operation is enabled")
	}
	if c.OrderCreationEnabled && (!c.ScannerEnabled || !c.SettlementEnabled) {
		return fmt.Errorf("onchain.ethereum order creation requires scanner and settlement to be enabled")
	}
	if !c.Enabled {
		return nil
	}

	network := onchain.Network(c.Network)
	definition, ok := onchain.NetworkDefinition(network)
	if !ok || (network != onchain.NetworkEthereumMainnet && network != onchain.NetworkEthereumSepolia) {
		return fmt.Errorf("onchain.ethereum.network must be ethereum-mainnet or ethereum-sepolia")
	}
	if err := onchain.ValidateTokenIdentity(network, c.ChainID, c.USDTContract, c.USDTDecimals); err != nil {
		return fmt.Errorf("onchain.ethereum token identity: %w", err)
	}
	if err := validateEthereumNodeURL("primary_rpc_url", c.PrimaryRPCURL, definition.Production); err != nil {
		return err
	}
	if err := validateEthereumNodeURL("backup_rpc_url", c.BackupRPCURL, definition.Production); err != nil {
		return err
	}
	if c.PrimaryRPCURL == c.BackupRPCURL {
		return fmt.Errorf("onchain.ethereum.backup_rpc_url must differ from primary_rpc_url")
	}
	if err := validateEthereumXPub(c.XPub); err != nil {
		return fmt.Errorf("onchain.ethereum.xpub: %w", err)
	}
	if err := onchain.ValidateAddress(network, c.SweepAddress); err != nil {
		return fmt.Errorf("onchain.ethereum.sweep_address: %w", err)
	}
	if c.RequestTimeoutSeconds < 1 || c.RequestTimeoutSeconds > 60 {
		return fmt.Errorf("onchain.ethereum.request_timeout_seconds must be between 1 and 60")
	}
	if c.ResponseMaxBytes < 1024 || c.ResponseMaxBytes > 64*1024*1024 {
		return fmt.Errorf("onchain.ethereum.response_max_bytes must be between 1024 and 67108864")
	}
	if c.RPCBatchLimit < 1 || c.RPCBatchLimit > 1000 {
		return fmt.Errorf("onchain.ethereum.rpc_batch_limit must be between 1 and 1000")
	}
	if c.RPCMaxLogRange < 1 || c.RPCMaxLogRange > 100000 {
		return fmt.Errorf("onchain.ethereum.rpc_max_log_range must be between 1 and 100000")
	}
	if c.MaxFinalizedLag > 100000 {
		return fmt.Errorf("onchain.ethereum.max_finalized_lag must be between 0 and 100000")
	}
	if c.ScanBatchSize < 1 || c.ScanBatchSize > c.RPCMaxLogRange {
		return fmt.Errorf("onchain.ethereum.scan_batch_size must be between 1 and rpc_max_log_range")
	}
	if c.ScanSafetyWindow < 1 || c.ScanSafetyWindow > 100000 {
		return fmt.Errorf("onchain.ethereum.scan_safety_window must be between 1 and 100000")
	}
	if c.ScanLeaseSeconds < 10 || c.ScanLeaseSeconds > 300 {
		return fmt.Errorf("onchain.ethereum.scan_lease_seconds must be between 10 and 300")
	}
	if c.ScanStallAlertSeconds < 30 || c.ScanStallAlertSeconds > 86400 {
		return fmt.Errorf("onchain.ethereum.scan_stall_alert_seconds must be between 30 and 86400")
	}
	if c.SettlementBacklogAlert < 1 || c.SettlementBacklogAlert > 1000000 {
		return fmt.Errorf("onchain.ethereum.settlement_backlog_alert_count must be between 1 and 1000000")
	}
	if c.PendingNonceAlert < 1 || c.PendingNonceAlert > 1000000 {
		return fmt.Errorf("onchain.ethereum.pending_nonce_alert_count must be between 1 and 1000000")
	}
	if c.StuckAlertSeconds < 60 || c.StuckAlertSeconds > 86400 {
		return fmt.Errorf("onchain.ethereum.stuck_transaction_alert_seconds must be between 60 and 86400")
	}
	if c.ReplacementAlert < 1 || c.ReplacementAlert > 1000000 {
		return fmt.Errorf("onchain.ethereum.replacement_transaction_alert_count must be between 1 and 1000000")
	}
	for name, value := range map[string]string{
		"unswept_balance_alert_raw":   c.UnsweptBalanceAlertRaw,
		"gas_cost_alert_wei":          c.GasCostAlertWei,
		"gas_sponsor_min_balance_wei": c.GasSponsorMinWei,
	} {
		amount, ok := new(big.Int).SetString(value, 10)
		if !ok || amount.Sign() <= 0 || amount.BitLen() > 256 || amount.String() != value {
			return fmt.Errorf("onchain.ethereum.%s must be a canonical positive integer", name)
		}
	}
	if c.ConfigVersion == "" || len(c.ConfigVersion) > 64 {
		return fmt.Errorf("onchain.ethereum.config_version must contain 1 to 64 characters")
	}
	return nil
}

func (c SelfHostedTRONConfig) Validate() error {
	anyOperationEnabled := c.OrderCreationEnabled || c.ScannerEnabled || c.SettlementEnabled || c.SweeperEnabled
	if anyOperationEnabled && !c.Enabled {
		return fmt.Errorf("onchain.tron.enabled must be true when an operation is enabled")
	}
	if c.OrderCreationEnabled && (!c.ScannerEnabled || !c.SettlementEnabled) {
		return fmt.Errorf("onchain.tron order creation requires scanner and settlement to be enabled")
	}
	if !c.Enabled {
		return nil
	}

	network := onchain.Network(c.Network)
	definition, ok := onchain.NetworkDefinition(network)
	if !ok || (network != onchain.NetworkTronMainnet && network != onchain.NetworkTronNile) {
		return fmt.Errorf("onchain.tron.network must be tron-mainnet or tron-nile")
	}
	if err := validateTRONNodeURL("full_node_url", c.FullNodeURL, definition.Production); err != nil {
		return err
	}
	if err := validateTRONNodeURL("solidity_node_url", c.SolidityNodeURL, definition.Production); err != nil {
		return err
	}
	if c.SolidityNodeFallbackURL != "" {
		if err := validateTRONNodeURL("solidity_node_fallback_url", c.SolidityNodeFallbackURL, definition.Production); err != nil {
			return err
		}
		if c.SolidityNodeFallbackURL == c.SolidityNodeURL {
			return fmt.Errorf("onchain.tron.solidity_node_fallback_url must differ from solidity_node_url")
		}
	}
	if err := onchain.ValidateTokenIdentity(network, 0, c.USDTContract, c.USDTDecimals); err != nil {
		return fmt.Errorf("onchain.tron token identity: %w", err)
	}
	if err := validateTRONXPub(c.XPub); err != nil {
		return fmt.Errorf("onchain.tron.xpub: %w", err)
	}
	if err := onchain.ValidateAddress(network, c.SweepAddress); err != nil {
		return fmt.Errorf("onchain.tron.sweep_address: %w", err)
	}
	if c.RequestTimeoutSeconds < 1 || c.RequestTimeoutSeconds > 60 {
		return fmt.Errorf("onchain.tron.request_timeout_seconds must be between 1 and 60")
	}
	if c.ResponseMaxBytes < 1024 || c.ResponseMaxBytes > 64*1024*1024 {
		return fmt.Errorf("onchain.tron.response_max_bytes must be between 1024 and 67108864")
	}
	if c.MaxBlockLag < 0 || c.MaxBlockLag > 10000 {
		return fmt.Errorf("onchain.tron.max_block_lag must be between 0 and 10000")
	}
	if c.ScanBatchSize < 1 || c.ScanBatchSize > 1000 {
		return fmt.Errorf("onchain.tron.scan_batch_size must be between 1 and 1000")
	}
	if c.ScanSafetyWindow < 1 || c.ScanSafetyWindow > 1000 {
		return fmt.Errorf("onchain.tron.scan_safety_window must be between 1 and 1000")
	}
	if c.ScanLeaseSeconds < 10 || c.ScanLeaseSeconds > 300 {
		return fmt.Errorf("onchain.tron.scan_lease_seconds must be between 10 and 300")
	}
	if c.ScanStallAlertSeconds < 30 || c.ScanStallAlertSeconds > 86400 {
		return fmt.Errorf("onchain.tron.scan_stall_alert_seconds must be between 30 and 86400")
	}
	if c.SettlementBacklogAlert < 1 || c.SettlementBacklogAlert > 1000000 {
		return fmt.Errorf("onchain.tron.settlement_backlog_alert_count must be between 1 and 1000000")
	}
	if c.ResourceWaitAlert < 1 || c.ResourceWaitAlert > 1000000 {
		return fmt.Errorf("onchain.tron.resource_wait_alert_count must be between 1 and 1000000")
	}
	if c.ReconciliationIntervalSeconds < 10 || c.ReconciliationIntervalSeconds > 86400 {
		return fmt.Errorf("onchain.tron.reconciliation_interval_seconds must be between 10 and 86400")
	}
	minimumSweep, ok := new(big.Int).SetString(c.SweepMinimumAmountRaw, 10)
	if !ok || minimumSweep.Sign() <= 0 || minimumSweep.BitLen() > 256 || minimumSweep.String() != c.SweepMinimumAmountRaw {
		return fmt.Errorf("onchain.tron.sweep_minimum_amount_raw must be a canonical positive uint256")
	}
	if c.SweepRequiredEnergy < 0 || c.SweepRequiredBandwidth < 0 || c.SweepMinimumTRXSun < 0 {
		return fmt.Errorf("onchain.tron sweep resource thresholds must be non-negative")
	}
	if c.SweepBatchSize < 1 || c.SweepBatchSize > 1000 {
		return fmt.Errorf("onchain.tron.sweep_batch_size must be between 1 and 1000")
	}
	if c.SignerTimeoutSeconds < 1 || c.SignerTimeoutSeconds > 60 {
		return fmt.Errorf("onchain.tron.signer_timeout_seconds must be between 1 and 60")
	}
	if c.SweepMaxFailures < 1 || c.SweepMaxFailures > 100 {
		return fmt.Errorf("onchain.tron.sweep_max_consecutive_failures must be between 1 and 100")
	}
	if c.SweepRetrySeconds < 1 || c.SweepRetrySeconds > 3600 {
		return fmt.Errorf("onchain.tron.sweep_retry_backoff_seconds must be between 1 and 3600")
	}
	if c.SweepConfirmSeconds < 1 || c.SweepConfirmSeconds > 3600 {
		return fmt.Errorf("onchain.tron.sweep_confirmation_poll_seconds must be between 1 and 3600")
	}
	hotWalletWarning, ok := new(big.Int).SetString(c.HotWalletWarningRaw, 10)
	if !ok || hotWalletWarning.Sign() <= 0 || hotWalletWarning.BitLen() > 256 || hotWalletWarning.String() != c.HotWalletWarningRaw {
		return fmt.Errorf("onchain.tron.hot_wallet_warning_amount_raw must be a canonical positive uint256")
	}
	hotWalletApproval, ok := new(big.Int).SetString(c.HotWalletApprovalRaw, 10)
	if !ok || hotWalletApproval.Sign() <= 0 || hotWalletApproval.BitLen() > 256 || hotWalletApproval.String() != c.HotWalletApprovalRaw {
		return fmt.Errorf("onchain.tron.hot_wallet_cold_approval_amount_raw must be a canonical positive uint256")
	}
	if hotWalletWarning.Cmp(hotWalletApproval) >= 0 {
		return fmt.Errorf("onchain.tron hot wallet warning amount must be below cold approval amount")
	}
	if c.SweeperEnabled {
		if err := validateTRONSignerConfig(c); err != nil {
			return err
		}
	}
	if c.ConfigVersion == "" || len(c.ConfigVersion) > 64 {
		return fmt.Errorf("onchain.tron.config_version must contain 1 to 64 characters")
	}
	return nil
}

func validateTRONSignerConfig(c SelfHostedTRONConfig) error {
	parsed, err := url.Parse(c.SignerURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("onchain.tron.signer_url must be an absolute HTTPS URL without a path")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("onchain.tron.signer_url must not contain credentials, query, or fragment")
	}
	if c.SignerClientCertFile == "" || c.SignerClientKeyFile == "" || c.SignerServerCAFile == "" {
		return fmt.Errorf("onchain.tron signer client certificate, key, and server CA files are required")
	}
	identity, err := url.Parse(c.SignerServerIdentityURI)
	if err != nil || identity.Scheme != "spiffe" || identity.Host == "" || identity.Path == "" || identity.User != nil || identity.RawQuery != "" || identity.Fragment != "" {
		return fmt.Errorf("onchain.tron.signer_server_identity_uri must be an absolute SPIFFE URI")
	}
	return nil
}

func validateTRONNodeURL(fieldName, raw string, production bool) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("onchain.tron.%s must be an absolute HTTP(S) URL", fieldName)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("onchain.tron.%s must use HTTP or HTTPS", fieldName)
	}
	if production && parsed.Scheme != "https" {
		return fmt.Errorf("onchain.tron.%s must use HTTPS on tron-mainnet", fieldName)
	}
	if parsed.User != nil {
		return fmt.Errorf("onchain.tron.%s must not contain URL credentials", fieldName)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("onchain.tron.%s must not contain a query or fragment", fieldName)
	}
	return nil
}

func validateTRONXPub(raw string) error {
	key, err := hdkeychain.NewKeyFromString(raw)
	if err != nil {
		return fmt.Errorf("parse extended key: %w", err)
	}
	defer key.Zero()
	if key.IsPrivate() {
		return fmt.Errorf("private extended keys are forbidden")
	}
	if key.Depth() != 4 {
		return fmt.Errorf("extended public key must represent m/44'/195'/0'/0 (depth 4)")
	}
	child, err := key.Derive(0)
	if err != nil {
		return fmt.Errorf("derive non-hardened child: %w", err)
	}
	child.Zero()
	return nil
}

func validateEthereumNodeURL(fieldName, raw string, production bool) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("onchain.ethereum.%s must be an absolute HTTP(S) URL", fieldName)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("onchain.ethereum.%s must use HTTP or HTTPS", fieldName)
	}
	if production && parsed.Scheme != "https" {
		return fmt.Errorf("onchain.ethereum.%s must use HTTPS on ethereum-mainnet", fieldName)
	}
	if parsed.User != nil {
		return fmt.Errorf("onchain.ethereum.%s must not contain URL credentials", fieldName)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("onchain.ethereum.%s must not contain a query or fragment", fieldName)
	}
	return nil
}

func validateEthereumXPub(raw string) error {
	key, err := hdkeychain.NewKeyFromString(raw)
	if err != nil {
		return fmt.Errorf("parse extended key: %w", err)
	}
	defer key.Zero()
	if key.IsPrivate() {
		return fmt.Errorf("private extended keys are forbidden")
	}
	if key.Depth() != 4 {
		return fmt.Errorf("extended public key must represent m/44'/60'/0'/0 (depth 4)")
	}
	child, err := key.Derive(0)
	if err != nil {
		return fmt.Errorf("derive non-hardened child: %w", err)
	}
	child.Zero()
	return nil
}
