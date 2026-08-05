package service

import (
	"context"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onchain"
)

var ErrOnchainAdminQueryUnavailable = errors.New("onchain admin query is unavailable")

type TRONHotWalletCheck func(context.Context) (onchain.TRONHotWalletStatus, error)
type TRONResourceCheck func(context.Context) (onchain.TRONAccountState, error)
type TRONSignerHealthCheck func(context.Context) error
type EthereumBalanceCheck func(context.Context, string) (*big.Int, error)

type AdminOnchainDepositQuery struct {
	Page           int
	PageSize       int
	Network        string
	Status         string
	TransactionID  string
	Address        string
	PaymentOrderID int64
	UserID         int64
	LogIndex       *int64
}

type AdminOnchainHealth struct {
	Network                           string              `json:"network"`
	ChainID                           int64               `json:"chain_id"`
	NodeHealthy                       bool                `json:"node_healthy"`
	NodeError                         string              `json:"node_error,omitempty"`
	NodeCheckedAt                     *time.Time          `json:"node_checked_at,omitempty"`
	PrimaryLatestHeight               int64               `json:"primary_latest_height"`
	BackupLatestHeight                int64               `json:"backup_latest_height"`
	PrimaryFinalizedHeight            int64               `json:"primary_finalized_height"`
	BackupFinalizedHeight             int64               `json:"backup_finalized_height"`
	CommonFinalizedHeight             int64               `json:"common_finalized_height"`
	FinalizedLagBlocks                int64               `json:"finalized_lag_blocks"`
	NodeConsistent                    bool                `json:"node_consistent"`
	FullNodeHeight                    int64               `json:"full_node_height"`
	SolidHeight                       int64               `json:"solid_height"`
	NodeBlockLag                      int64               `json:"node_block_lag"`
	CursorHealth                      string              `json:"cursor_health"`
	FinalizedHeight                   int64               `json:"finalized_height"`
	ScanLagBlocks                     int64               `json:"scan_lag_blocks"`
	ScanLagSeconds                    int64               `json:"scan_lag_seconds"`
	FinalizedHash                     string              `json:"finalized_hash,omitempty"`
	LeaseOwner                        *string             `json:"lease_owner,omitempty"`
	LeaseUntil                        *time.Time          `json:"lease_until,omitempty"`
	LastSuccessAt                     *time.Time          `json:"last_success_at,omitempty"`
	LastErrorCode                     *string             `json:"last_error_code,omitempty"`
	LastErrorMessage                  *string             `json:"last_error_message,omitempty"`
	PendingSettlements                int                 `json:"pending_settlements"`
	ReviewRequired                    int                 `json:"review_required"`
	ReconciliationMismatches          int                 `json:"reconciliation_mismatches"`
	UnsweptBalanceRaw                 string              `json:"unswept_balance_raw"`
	RPCErrorCount                     int                 `json:"rpc_error_count"`
	PendingNonceCount                 int                 `json:"pending_nonce_count"`
	NonceConflictCount                int                 `json:"nonce_conflict_count"`
	PendingTransactionCount           int                 `json:"pending_transaction_count"`
	StuckTransactionAgeSeconds        int64               `json:"stuck_transaction_age_seconds"`
	ReplacementTransactionCount       int                 `json:"replacement_transaction_count"`
	GasCostWei                        string              `json:"gas_cost_wei"`
	GasSponsorAddress                 string              `json:"gas_sponsor_address,omitempty"`
	GasSponsorBalanceWei              string              `json:"gas_sponsor_balance_wei,omitempty"`
	GasSponsorError                   string              `json:"gas_sponsor_error,omitempty"`
	GasSponsorCheckedAt               *time.Time          `json:"gas_sponsor_checked_at,omitempty"`
	ResourceWaitSweeps                int                 `json:"resource_wait_sweeps"`
	SweepRetryCount                   int                 `json:"sweep_retry_count"`
	SweepMaxRetryCount                int                 `json:"sweep_max_retry_count"`
	SweepFailureCount                 int                 `json:"sweep_failure_count"`
	TRXBalanceSun                     int64               `json:"trx_balance_sun"`
	AvailableEnergy                   int64               `json:"available_energy"`
	AvailableBandwidth                int64               `json:"available_bandwidth"`
	ResourceError                     string              `json:"resource_error,omitempty"`
	ResourceCheckedAt                 *time.Time          `json:"resource_checked_at,omitempty"`
	SignerHealthy                     bool                `json:"signer_healthy"`
	SignerError                       string              `json:"signer_error,omitempty"`
	SignerCheckedAt                   *time.Time          `json:"signer_checked_at,omitempty"`
	Alerts                            []AdminOnchainAlert `json:"alerts"`
	HotWalletAddress                  string              `json:"hot_wallet_address,omitempty"`
	HotWalletBalanceRaw               string              `json:"hot_wallet_balance_raw,omitempty"`
	HotWalletWarningThresholdRaw      string              `json:"hot_wallet_warning_threshold_raw,omitempty"`
	HotWalletColdApprovalThresholdRaw string              `json:"hot_wallet_cold_approval_threshold_raw,omitempty"`
	HotWalletWarning                  bool                `json:"hot_wallet_warning"`
	ColdTransferApprovalRequired      bool                `json:"cold_transfer_approval_required"`
	AutomaticSweepsPaused             bool                `json:"automatic_sweeps_paused"`
	HotWalletError                    string              `json:"hot_wallet_error,omitempty"`
	HotWalletCheckedAt                *time.Time          `json:"hot_wallet_checked_at,omitempty"`
	UpdatedAt                         time.Time           `json:"updated_at"`
}

type AdminOnchainAlert struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Value     string `json:"value,omitempty"`
	Threshold string `json:"threshold,omitempty"`
}

type AdminOnchainDeposit struct {
	ID               int64      `json:"id"`
	IntentID         int64      `json:"intent_id"`
	PaymentOrderID   int64      `json:"payment_order_id"`
	UserID           int64      `json:"user_id"`
	Network          string     `json:"network"`
	ChainID          int64      `json:"chain_id"`
	TransactionID    string     `json:"transaction_id"`
	LogIndex         int64      `json:"log_index"`
	TransactionIndex int64      `json:"transaction_index"`
	BlockHeight      int64      `json:"block_height"`
	BlockHash        string     `json:"block_hash"`
	TokenContract    string     `json:"token_contract"`
	FromAddress      string     `json:"from_address"`
	ToAddress        string     `json:"to_address"`
	AmountRaw        string     `json:"amount_raw"`
	TransactionTime  time.Time  `json:"transaction_time"`
	ReceiptSuccess   bool       `json:"receipt_success"`
	Finalized        bool       `json:"finalized"`
	Status           string     `json:"status"`
	ValidationError  *string    `json:"validation_error,omitempty"`
	CreditAuditRef   *string    `json:"credit_audit_ref,omitempty"`
	CreditedAt       *time.Time `json:"credited_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type AdminOnchainIntent struct {
	ID                       int64      `json:"id"`
	PaymentOrderID           int64      `json:"payment_order_id"`
	UserID                   int64      `json:"user_id"`
	Network                  string     `json:"network"`
	ChainID                  int64      `json:"chain_id"`
	TokenContract            string     `json:"token_contract"`
	DepositAddress           string     `json:"deposit_address"`
	DerivationIndex          int64      `json:"derivation_index"`
	ExpectedAmountRaw        string     `json:"expected_amount_raw"`
	ReceivedAmountRaw        string     `json:"received_amount_raw"`
	CreditedAmountRaw        string     `json:"credited_amount_raw"`
	OverpaidAmountRaw        string     `json:"overpaid_amount_raw"`
	ConfigVersion            string     `json:"config_version"`
	Status                   string     `json:"status"`
	SettlementIdempotencyKey *string    `json:"settlement_idempotency_key,omitempty"`
	SettlementAttempts       int        `json:"settlement_attempts"`
	NextSettlementAt         *time.Time `json:"next_settlement_at,omitempty"`
	LastErrorCode            *string    `json:"last_error_code,omitempty"`
	LastErrorMessage         *string    `json:"last_error_message,omitempty"`
	SettledAt                *time.Time `json:"settled_at,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
}

type AdminWalletSweep struct {
	ID                   int64      `json:"id"`
	IntentID             int64      `json:"intent_id"`
	Network              string     `json:"network"`
	ChainID              int64      `json:"chain_id"`
	SourceAddress        string     `json:"source_address"`
	DestinationAddress   string     `json:"destination_address"`
	BalanceSnapshotRaw   string     `json:"balance_snapshot_raw"`
	AmountRaw            string     `json:"amount_raw"`
	IdempotencyKey       string     `json:"idempotency_key"`
	SignerAuditID        *string    `json:"signer_audit_id,omitempty"`
	TransactionID        *string    `json:"transaction_id,omitempty"`
	Nonce                *int64     `json:"nonce,omitempty"`
	ReplacementOfID      *int64     `json:"replacement_of_id,omitempty"`
	FeeRaw               string     `json:"fee_raw"`
	EnergyUsed           int64      `json:"energy_used"`
	BandwidthUsed        int64      `json:"bandwidth_used"`
	FinalizedBlockHeight *int64     `json:"finalized_block_height,omitempty"`
	FinalizedBlockHash   *string    `json:"finalized_block_hash,omitempty"`
	Status               string     `json:"status"`
	RetryCount           int        `json:"retry_count"`
	FailureCode          *string    `json:"failure_code,omitempty"`
	FailureReason        *string    `json:"failure_reason,omitempty"`
	NextAttemptAt        *time.Time `json:"next_attempt_at,omitempty"`
	FinalizedAt          *time.Time `json:"finalized_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type AdminEthereumGasFunding struct {
	ID                      int64      `json:"id"`
	IntentID                int64      `json:"intent_id"`
	ChainID                 int64      `json:"chain_id"`
	SponsorAddress          string     `json:"sponsor_address"`
	TargetAddress           string     `json:"target_address"`
	DerivationIndex         int64      `json:"derivation_index"`
	AmountWei               string     `json:"amount_wei"`
	IdempotencyKey          string     `json:"idempotency_key"`
	Nonce                   int64      `json:"nonce"`
	GasLimit                int64      `json:"gas_limit"`
	MaxFeePerGasWei         string     `json:"max_fee_per_gas_wei"`
	MaxPriorityFeePerGasWei string     `json:"max_priority_fee_per_gas_wei"`
	TransactionHash         *string    `json:"transaction_hash,omitempty"`
	ReplacementOfID         *int64     `json:"replacement_of_id,omitempty"`
	Status                  string     `json:"status"`
	Finalized               bool       `json:"finalized"`
	FinalizedBlockHeight    *int64     `json:"finalized_block_height,omitempty"`
	FinalizedBlockHash      *string    `json:"finalized_block_hash,omitempty"`
	ActualFeeWei            *string    `json:"actual_fee_wei,omitempty"`
	RetryCount              int        `json:"retry_count"`
	FailureCode             *string    `json:"failure_code,omitempty"`
	FailureReason           *string    `json:"failure_reason,omitempty"`
	NextAttemptAt           *time.Time `json:"next_attempt_at,omitempty"`
	FinalizedAt             *time.Time `json:"finalized_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

type AdminOnchainOrderTrace struct {
	Intent        *AdminOnchainIntent        `json:"intent"`
	Deposits      []AdminOnchainDeposit      `json:"deposits"`
	BalanceAudits []AdminOnchainBalanceAudit `json:"balance_audits"`
	Sweeps        []AdminWalletSweep         `json:"sweeps"`
	GasFundings   []AdminEthereumGasFunding  `json:"gas_fundings"`
	NonceStates   []AdminEthereumNonceState  `json:"nonce_states"`
}

type AdminEthereumNonceState struct {
	ID                   int64      `json:"id"`
	ChainID              int64      `json:"chain_id"`
	SenderAddress        string     `json:"sender_address"`
	NextNonce            int64      `json:"next_nonce"`
	ObservedPendingNonce int64      `json:"observed_pending_nonce"`
	Status               string     `json:"status"`
	LeaseOwner           *string    `json:"lease_owner,omitempty"`
	LeaseUntil           *time.Time `json:"lease_until,omitempty"`
	LastReconciledAt     *time.Time `json:"last_reconciled_at,omitempty"`
	LastErrorCode        *string    `json:"last_error_code,omitempty"`
	LastErrorMessage     *string    `json:"last_error_message,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type AdminOnchainBalanceAudit struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	Operator  string    `json:"operator"`
	CreatedAt time.Time `json:"created_at"`
}

type OnchainAdminRepository interface {
	ListAdminOnchainHealth(context.Context) ([]AdminOnchainHealth, error)
	ListAdminOnchainDeposits(context.Context, AdminOnchainDepositQuery) ([]AdminOnchainDeposit, int, error)
	GetAdminOnchainOrderTrace(context.Context, int64) (*AdminOnchainOrderTrace, error)
}

func (s *PaymentService) GetAdminOnchainHealth(ctx context.Context) ([]AdminOnchainHealth, error) {
	if s.onchainAdminRepo == nil {
		return nil, ErrOnchainAdminQueryUnavailable
	}
	items, err := s.onchainAdminRepo.ListAdminOnchainHealth(ctx)
	if err != nil {
		return nil, err
	}
	if s.ethereumConfig != nil {
		items = s.enrichEthereumHealth(ctx, items)
	}
	if s.tronConfig == nil {
		return items, nil
	}

	network := strings.TrimSpace(s.tronConfig.Network)
	index := -1
	for i := range items {
		if items[i].Network == network {
			index = i
			break
		}
	}
	if index < 0 {
		definition, _ := onchain.NetworkDefinition(onchain.Network(network))
		items = append(items, AdminOnchainHealth{
			Network: network, ChainID: int64(definition.ChainID), CursorHealth: "MISSING",
		})
		index = len(items) - 1
	}
	checkedAt := time.Now().UTC()
	items[index].NodeCheckedAt = &checkedAt
	if s.tronOrderHealthCheck == nil {
		items[index].NodeError = "node health check is unavailable"
	} else {
		report, err := s.tronOrderHealthCheck(ctx)
		items[index].NodeHealthy = report.Healthy
		items[index].FullNodeHeight = report.FullNodeHeight
		items[index].SolidHeight = report.SolidHeight
		items[index].NodeBlockLag = report.BlockLag
		if !report.CheckedAt.IsZero() {
			reportCheckedAt := report.CheckedAt.UTC()
			items[index].NodeCheckedAt = &reportCheckedAt
		}
		if err != nil {
			items[index].NodeError = err.Error()
		}
	}
	items[index].ScanLagBlocks = nonNegativeMetricDifference(items[index].SolidHeight, items[index].FinalizedHeight)
	if items[index].LastSuccessAt != nil {
		items[index].ScanLagSeconds = nonNegativeMetricDurationSeconds(checkedAt.Sub(items[index].LastSuccessAt.UTC()))
	}
	items[index].ResourceCheckedAt = &checkedAt
	if s.tronResourceCheck == nil {
		items[index].ResourceError = "TRON resource check is unavailable"
	} else if resources, err := s.tronResourceCheck(ctx); err != nil {
		items[index].ResourceError = err.Error()
	} else {
		items[index].TRXBalanceSun = resources.TRXBalanceSun
		items[index].AvailableEnergy = resources.AvailableEnergy()
		items[index].AvailableBandwidth = resources.AvailableBandwidth()
	}
	items[index].SignerCheckedAt = &checkedAt
	if s.tronSignerHealthCheck == nil {
		items[index].SignerError = "signer health check is unavailable"
	} else if err := s.tronSignerHealthCheck(ctx); err != nil {
		items[index].SignerError = err.Error()
	} else {
		items[index].SignerHealthy = true
	}
	items[index].HotWalletAddress = s.tronConfig.SweepAddress
	items[index].HotWalletWarningThresholdRaw = s.tronConfig.HotWalletWarningRaw
	items[index].HotWalletColdApprovalThresholdRaw = s.tronConfig.HotWalletApprovalRaw
	if s.tronHotWalletCheck != nil {
		items[index].HotWalletCheckedAt = &checkedAt
		status, err := s.tronHotWalletCheck(ctx)
		if err != nil {
			items[index].HotWalletError = err.Error()
			items[index].AutomaticSweepsPaused = true
		} else {
			items[index].HotWalletAddress = status.Address
			items[index].HotWalletBalanceRaw = status.BalanceRaw
			items[index].HotWalletWarningThresholdRaw = status.WarningThresholdRaw
			items[index].HotWalletColdApprovalThresholdRaw = status.ColdApprovalThresholdRaw
			items[index].HotWalletWarning = status.Warning
			items[index].ColdTransferApprovalRequired = status.ColdTransferApprovalRequired
			items[index].AutomaticSweepsPaused = status.AutomaticSweepsPaused
		}
	}
	items[index].Alerts = evaluateTRONAlerts(items[index], *s.tronConfig)
	return items, nil
}

func (s *PaymentService) enrichEthereumHealth(ctx context.Context, items []AdminOnchainHealth) []AdminOnchainHealth {
	cfg := *s.ethereumConfig
	network := strings.TrimSpace(cfg.Network)
	index := -1
	for i := range items {
		if items[i].Network == network {
			index = i
			break
		}
	}
	if index < 0 {
		items = append(items, AdminOnchainHealth{Network: network, ChainID: int64(cfg.ChainID), CursorHealth: "MISSING"})
		index = len(items) - 1
	}
	checkedAt := time.Now().UTC()
	item := &items[index]
	item.NodeCheckedAt = &checkedAt
	item.NodeConsistent = true
	if s.ethereumOrderHealthCheck == nil {
		item.NodeError = "Ethereum node health check is unavailable"
	} else if report, err := s.ethereumOrderHealthCheck(ctx); err != nil {
		item.NodeError = err.Error()
		item.NodeConsistent = !errors.Is(err, onchain.ErrEthereumFinalizedDivergence)
	} else {
		item.NodeHealthy = true
		item.NodeConsistent = true
		item.PrimaryLatestHeight = int64(report.Primary.Latest)
		item.BackupLatestHeight = int64(report.Backup.Latest)
		item.PrimaryFinalizedHeight = int64(report.Primary.Finalized.Number)
		item.BackupFinalizedHeight = int64(report.Backup.Finalized.Number)
		item.CommonFinalizedHeight = int64(report.CommonFinalized.Number)
		latest := item.PrimaryLatestHeight
		if item.BackupLatestHeight > latest {
			latest = item.BackupLatestHeight
		}
		item.FinalizedLagBlocks = nonNegativeMetricDifference(latest, item.CommonFinalizedHeight)
	}
	item.ScanLagBlocks = nonNegativeMetricDifference(item.CommonFinalizedHeight, item.FinalizedHeight)
	if item.LastSuccessAt != nil {
		item.ScanLagSeconds = nonNegativeMetricDurationSeconds(checkedAt.Sub(item.LastSuccessAt.UTC()))
	}
	if item.GasSponsorAddress != "" {
		item.GasSponsorCheckedAt = &checkedAt
		if s.ethereumBalanceCheck == nil {
			item.GasSponsorError = "Ethereum balance check is unavailable"
		} else if balance, err := s.ethereumBalanceCheck(ctx, item.GasSponsorAddress); err != nil {
			item.GasSponsorError = err.Error()
		} else if balance == nil || balance.Sign() < 0 {
			item.GasSponsorError = "Ethereum balance check returned an invalid balance"
		} else {
			item.GasSponsorBalanceWei = balance.String()
		}
	}
	item.Alerts = evaluateEthereumAlerts(*item, cfg)
	return items
}

func evaluateTRONAlerts(item AdminOnchainHealth, cfg config.SelfHostedTRONConfig) []AdminOnchainAlert {
	alerts := make([]AdminOnchainAlert, 0, 8)
	if !cfg.Enabled {
		return alerts
	}
	maxBlockLag := cfg.MaxBlockLag
	if maxBlockLag < 0 {
		maxBlockLag = config.DefaultTRONMaxBlockLag
	}
	if !item.NodeHealthy || item.NodeBlockLag > maxBlockLag {
		alerts = append(alerts, metricAlert("NODE_UNSYNCED", "P1", item.NodeBlockLag, maxBlockLag))
	}
	if item.CursorHealth == string(onchain.CursorHashConflict) || stringPointerEquals(item.LastErrorCode, "SOLIDIFIED_HASH_CONFLICT") {
		alerts = append(alerts, AdminOnchainAlert{Code: "HASH_CONFLICT", Severity: "P0"})
	}
	scanStallSeconds := cfg.ScanStallAlertSeconds
	if scanStallSeconds <= 0 {
		scanStallSeconds = config.DefaultTRONScanStallAlertSeconds
	}
	if item.LastSuccessAt == nil || item.ScanLagBlocks > maxBlockLag || item.ScanLagSeconds >= int64(scanStallSeconds) {
		alerts = append(alerts, metricAlert("SCAN_STALLED", "P1", item.ScanLagSeconds, int64(scanStallSeconds)))
	}
	settlementBacklog := cfg.SettlementBacklogAlert
	if settlementBacklog <= 0 {
		settlementBacklog = config.DefaultTRONSettlementBacklogAlert
	}
	if item.PendingSettlements >= settlementBacklog {
		alerts = append(alerts, metricAlert("SETTLEMENT_BACKLOG", "P1", item.PendingSettlements, settlementBacklog))
	}
	resourceWaitAlert := cfg.ResourceWaitAlert
	if resourceWaitAlert <= 0 {
		resourceWaitAlert = config.DefaultTRONResourceWaitAlert
	}
	resourceLow := (item.AvailableEnergy < cfg.SweepRequiredEnergy || item.AvailableBandwidth < cfg.SweepRequiredBandwidth) &&
		item.TRXBalanceSun < cfg.SweepMinimumTRXSun
	if item.ResourceError != "" || item.ResourceWaitSweeps >= resourceWaitAlert || resourceLow {
		alerts = append(alerts, metricAlert("RESOURCE_INSUFFICIENT", "P1", item.ResourceWaitSweeps, resourceWaitAlert))
	}
	maxFailures := cfg.SweepMaxFailures
	if maxFailures <= 0 {
		maxFailures = config.DefaultTRONSweepMaxFailures
	}
	if item.SweepMaxRetryCount >= maxFailures {
		alerts = append(alerts, metricAlert("SWEEP_CONSECUTIVE_FAILURES", "P1", item.SweepMaxRetryCount, maxFailures))
	}
	if item.ReconciliationMismatches > 0 {
		alerts = append(alerts, AdminOnchainAlert{
			Code: "RECONCILIATION_MISMATCH", Severity: "P1",
			Value: strconv.Itoa(item.ReconciliationMismatches),
		})
	}
	if !item.SignerHealthy {
		alerts = append(alerts, AdminOnchainAlert{Code: "SIGNER_UNAVAILABLE", Severity: "P1"})
	}
	if item.HotWalletWarning || item.ColdTransferApprovalRequired {
		severity := "P2"
		threshold := cfg.HotWalletWarningRaw
		if item.ColdTransferApprovalRequired {
			severity = "P1"
			threshold = cfg.HotWalletApprovalRaw
		}
		alerts = append(alerts, AdminOnchainAlert{
			Code: "HOT_WALLET_LIMIT", Severity: severity,
			Value: item.HotWalletBalanceRaw, Threshold: threshold,
		})
	}
	return alerts
}

func evaluateEthereumAlerts(item AdminOnchainHealth, cfg config.SelfHostedEthereumConfig) []AdminOnchainAlert {
	alerts := make([]AdminOnchainAlert, 0, 12)
	if !cfg.Enabled {
		return alerts
	}
	if !item.NodeConsistent || strings.Contains(strings.ToLower(item.NodeError), "divergence") {
		alerts = append(alerts, AdminOnchainAlert{Code: "NODE_DIVERGENCE", Severity: "P0"})
	} else if !item.NodeHealthy || item.NodeError != "" || item.RPCErrorCount > 0 {
		alerts = append(alerts, metricAlert("RPC_ERRORS", "P1", item.RPCErrorCount, 0))
	}
	if item.FinalizedLagBlocks > int64(cfg.MaxFinalizedLag) {
		alerts = append(alerts, metricAlert("FINALIZED_DELAY", "P1", item.FinalizedLagBlocks, int64(cfg.MaxFinalizedLag)))
	}
	if item.CursorHealth == string(onchain.CursorHashConflict) || stringPointerEquals(item.LastErrorCode, "FINALIZED_HASH_CONFLICT") {
		alerts = append(alerts, AdminOnchainAlert{Code: "HASH_CONFLICT", Severity: "P0"})
	}
	if item.LastSuccessAt == nil || item.ScanLagSeconds >= int64(cfg.ScanStallAlertSeconds) {
		alerts = append(alerts, metricAlert("SCAN_STALLED", "P1", item.ScanLagSeconds, int64(cfg.ScanStallAlertSeconds)))
	}
	if item.PendingSettlements >= cfg.SettlementBacklogAlert {
		alerts = append(alerts, metricAlert("SETTLEMENT_BACKLOG", "P1", item.PendingSettlements, cfg.SettlementBacklogAlert))
	}
	if item.GasSponsorError != "" || rawMetricAtLeast(item.GasSponsorBalanceWei, cfg.GasSponsorMinWei, false) {
		alerts = append(alerts, AdminOnchainAlert{Code: "ETH_LOW", Severity: "P1", Value: item.GasSponsorBalanceWei, Threshold: cfg.GasSponsorMinWei})
	}
	if item.PendingNonceCount >= cfg.PendingNonceAlert {
		alerts = append(alerts, metricAlert("PENDING_NONCE", "P2", item.PendingNonceCount, cfg.PendingNonceAlert))
	}
	if item.NonceConflictCount > 0 {
		alerts = append(alerts, metricAlert("NONCE_CONFLICT", "P0", item.NonceConflictCount, 0))
	}
	if item.StuckTransactionAgeSeconds >= int64(cfg.StuckAlertSeconds) {
		alerts = append(alerts, metricAlert("TRANSACTION_STUCK", "P1", item.StuckTransactionAgeSeconds, int64(cfg.StuckAlertSeconds)))
	}
	if item.ReplacementTransactionCount >= cfg.ReplacementAlert {
		alerts = append(alerts, metricAlert("TRANSACTION_REPLACEMENTS", "P2", item.ReplacementTransactionCount, cfg.ReplacementAlert))
	}
	if rawMetricAtLeast(item.UnsweptBalanceRaw, cfg.UnsweptBalanceAlertRaw, true) {
		alerts = append(alerts, AdminOnchainAlert{Code: "UNSWEPT_USDT", Severity: "P2", Value: item.UnsweptBalanceRaw, Threshold: cfg.UnsweptBalanceAlertRaw})
	}
	if rawMetricAtLeast(item.GasCostWei, cfg.GasCostAlertWei, true) {
		alerts = append(alerts, AdminOnchainAlert{Code: "GAS_COST_HIGH", Severity: "P2", Value: item.GasCostWei, Threshold: cfg.GasCostAlertWei})
	}
	return alerts
}

func rawMetricAtLeast(value, threshold string, alertWhenAtLeast bool) bool {
	actual, okActual := new(big.Int).SetString(strings.TrimSpace(value), 10)
	limit, okLimit := new(big.Int).SetString(strings.TrimSpace(threshold), 10)
	if !okActual || !okLimit {
		return false
	}
	if alertWhenAtLeast {
		return actual.Cmp(limit) >= 0
	}
	return actual.Cmp(limit) < 0
}

func metricAlert[T ~int | ~int64](code, severity string, value, threshold T) AdminOnchainAlert {
	return AdminOnchainAlert{
		Code: code, Severity: severity,
		Value: strconv.FormatInt(int64(value), 10), Threshold: strconv.FormatInt(int64(threshold), 10),
	}
}

func stringPointerEquals(value *string, expected string) bool {
	return value != nil && strings.EqualFold(strings.TrimSpace(*value), expected)
}

func nonNegativeMetricDifference(latest, current int64) int64 {
	if latest <= current {
		return 0
	}
	return latest - current
}

func nonNegativeMetricDurationSeconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64(duration / time.Second)
}

func (s *PaymentService) ListAdminOnchainDeposits(ctx context.Context, query AdminOnchainDepositQuery) ([]AdminOnchainDeposit, int, error) {
	if s.onchainAdminRepo == nil {
		return nil, 0, ErrOnchainAdminQueryUnavailable
	}
	query.PageSize, query.Page = applyPagination(query.PageSize, query.Page)
	return s.onchainAdminRepo.ListAdminOnchainDeposits(ctx, query)
}

func (s *PaymentService) ListAdminOnchainReviews(ctx context.Context, query AdminOnchainDepositQuery) ([]AdminOnchainDeposit, int, error) {
	query.Status = "REVIEW_REQUIRED"
	return s.ListAdminOnchainDeposits(ctx, query)
}

func (s *PaymentService) GetAdminOnchainOrderTrace(ctx context.Context, orderID int64) (*AdminOnchainOrderTrace, error) {
	if s.onchainAdminRepo == nil {
		return nil, nil
	}
	return s.onchainAdminRepo.GetAdminOnchainOrderTrace(ctx, orderID)
}
