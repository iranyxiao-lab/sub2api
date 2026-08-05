package onchain

import (
	"context"
	"fmt"
	"math/big"
	"strings"
)

type TRONHotWalletBalanceSource interface {
	TRC20Balance(ctx context.Context, contract, address string) (*big.Int, error)
}

type TRONHotWalletStatus struct {
	Address                      string
	BalanceRaw                   string
	WarningThresholdRaw          string
	ColdApprovalThresholdRaw     string
	Warning                      bool
	ColdTransferApprovalRequired bool
	AutomaticSweepsPaused        bool
}

type TRONHotWalletMonitor struct {
	source                TRONHotWalletBalanceSource
	contract              string
	address               string
	warningThreshold      *big.Int
	coldApprovalThreshold *big.Int
}

func NewTRONHotWalletMonitor(source TRONHotWalletBalanceSource, network Network, contract, address, warningThresholdRaw, coldApprovalThresholdRaw string) (*TRONHotWalletMonitor, error) {
	if source == nil {
		return nil, fmt.Errorf("TRON hot wallet balance source is required")
	}
	if err := ValidateAddress(network, contract); err != nil {
		return nil, fmt.Errorf("invalid TRON hot wallet token contract: %w", err)
	}
	if err := ValidateAddress(network, address); err != nil {
		return nil, fmt.Errorf("invalid TRON hot wallet address: %w", err)
	}
	warning, err := canonicalPositiveUint256(warningThresholdRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid TRON hot wallet warning threshold: %w", err)
	}
	coldApproval, err := canonicalPositiveUint256(coldApprovalThresholdRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid TRON hot wallet cold approval threshold: %w", err)
	}
	if warning.Cmp(coldApproval) >= 0 {
		return nil, fmt.Errorf("TRON hot wallet warning threshold must be below the cold approval threshold")
	}
	return &TRONHotWalletMonitor{
		source: source, contract: strings.TrimSpace(contract), address: strings.TrimSpace(address),
		warningThreshold: warning, coldApprovalThreshold: coldApproval,
	}, nil
}

func (m *TRONHotWalletMonitor) Check(ctx context.Context) (TRONHotWalletStatus, error) {
	balance, err := m.source.TRC20Balance(ctx, m.contract, m.address)
	if err != nil {
		return TRONHotWalletStatus{}, fmt.Errorf("query TRON hot wallet USDT balance: %w", err)
	}
	if balance == nil || balance.Sign() < 0 || balance.BitLen() > 256 {
		return TRONHotWalletStatus{}, fmt.Errorf("TRON hot wallet returned an invalid USDT balance")
	}
	requiresApproval := balance.Cmp(m.coldApprovalThreshold) >= 0
	return TRONHotWalletStatus{
		Address: m.address, BalanceRaw: balance.String(),
		WarningThresholdRaw:          m.warningThreshold.String(),
		ColdApprovalThresholdRaw:     m.coldApprovalThreshold.String(),
		Warning:                      balance.Cmp(m.warningThreshold) >= 0,
		ColdTransferApprovalRequired: requiresApproval,
		AutomaticSweepsPaused:        requiresApproval,
	}, nil
}

func canonicalPositiveUint256(raw string) (*big.Int, error) {
	trimmed := strings.TrimSpace(raw)
	value, ok := new(big.Int).SetString(trimmed, 10)
	if !ok || value.Sign() <= 0 || value.BitLen() > 256 || value.String() != trimmed {
		return nil, fmt.Errorf("must be a canonical positive uint256")
	}
	return value, nil
}
