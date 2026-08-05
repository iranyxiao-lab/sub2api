package onchain

import (
	"context"
	"fmt"
	"math/big"
	"strings"
)

const (
	DefaultEthereumMinimumSweepRaw = "200000000"
	DefaultEthereumMaxGasBudgetWei = "6000000000000000"
)

type EthereumSweepPreflightSource interface {
	EthereumBalance(ctx context.Context, address string) (*big.Int, error)
	ERC20Balance(ctx context.Context, contract, owner string) (*big.Int, error)
	EstimateERC20TransferGas(ctx context.Context, contract, from, destination string, amount *big.Int) (uint64, error)
	SuggestedMaxFeePerGas(ctx context.Context) (*big.Int, error)
}

type EthereumSweepPreflightOptions struct {
	ContractAddress  string
	SourceAddress    string
	SweepAddress     string
	MinimumAmountRaw string
	MaxGasBudgetWei  string
}

type EthereumSweepPreflightResult struct {
	Eligible           bool
	Reason             string
	USDTBalanceRaw     string
	ETHBalanceWei      string
	GasLimit           uint64
	MaxFeePerGasWei    string
	BufferedGasCostWei string
	RequiredFundingWei string
}

func EvaluateEthereumSweep(ctx context.Context, source EthereumSweepPreflightSource, options EthereumSweepPreflightOptions) (EthereumSweepPreflightResult, error) {
	if source == nil {
		return EthereumSweepPreflightResult{}, fmt.Errorf("Ethereum sweep preflight source is required")
	}
	for name, address := range map[string]string{"contract": options.ContractAddress, "source": options.SourceAddress, "sweep": options.SweepAddress} {
		if err := ValidateAddress(NetworkEthereumMainnet, address); err != nil {
			return EthereumSweepPreflightResult{}, fmt.Errorf("invalid Ethereum %s address: %w", name, err)
		}
	}
	minimum, err := positiveCanonicalInteger(options.MinimumAmountRaw, DefaultEthereumMinimumSweepRaw)
	if err != nil {
		return EthereumSweepPreflightResult{}, fmt.Errorf("invalid Ethereum minimum sweep amount: %w", err)
	}
	budget, err := positiveCanonicalInteger(options.MaxGasBudgetWei, DefaultEthereumMaxGasBudgetWei)
	if err != nil {
		return EthereumSweepPreflightResult{}, fmt.Errorf("invalid Ethereum max gas budget: %w", err)
	}
	usdtBalance, err := source.ERC20Balance(ctx, options.ContractAddress, options.SourceAddress)
	if err != nil {
		return EthereumSweepPreflightResult{}, fmt.Errorf("query Ethereum USDT balance: %w", err)
	}
	ethBalance, err := source.EthereumBalance(ctx, options.SourceAddress)
	if err != nil {
		return EthereumSweepPreflightResult{}, fmt.Errorf("query Ethereum ETH balance: %w", err)
	}
	if usdtBalance == nil || usdtBalance.Sign() < 0 || usdtBalance.BitLen() > 256 || ethBalance == nil || ethBalance.Sign() < 0 {
		return EthereumSweepPreflightResult{}, fmt.Errorf("Ethereum sweep balance response is invalid")
	}
	result := EthereumSweepPreflightResult{
		USDTBalanceRaw: usdtBalance.String(), ETHBalanceWei: ethBalance.String(), RequiredFundingWei: "0",
	}
	if usdtBalance.Cmp(minimum) < 0 {
		result.Reason = "BELOW_MINIMUM_SWEEP"
		return result, nil
	}
	gasLimit, err := source.EstimateERC20TransferGas(ctx, options.ContractAddress, options.SourceAddress, options.SweepAddress, usdtBalance)
	if err != nil {
		return EthereumSweepPreflightResult{}, fmt.Errorf("estimate Ethereum ERC20 sweep gas: %w", err)
	}
	if gasLimit < 21_000 {
		return EthereumSweepPreflightResult{}, fmt.Errorf("estimated Ethereum ERC20 sweep gas is invalid")
	}
	maxFeePerGas, err := source.SuggestedMaxFeePerGas(ctx)
	if err != nil {
		return EthereumSweepPreflightResult{}, fmt.Errorf("query Ethereum max fee per gas: %w", err)
	}
	if maxFeePerGas == nil || maxFeePerGas.Sign() <= 0 {
		return EthereumSweepPreflightResult{}, fmt.Errorf("Ethereum max fee per gas is invalid")
	}
	baseCost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), maxFeePerGas)
	bufferedCost := new(big.Int).Div(new(big.Int).Add(new(big.Int).Mul(baseCost, big.NewInt(120)), big.NewInt(99)), big.NewInt(100))
	result.GasLimit = gasLimit
	result.MaxFeePerGasWei = maxFeePerGas.String()
	result.BufferedGasCostWei = bufferedCost.String()
	if bufferedCost.Cmp(budget) > 0 {
		result.Reason = "GAS_BUDGET_EXCEEDED"
		return result, nil
	}
	if ethBalance.Cmp(bufferedCost) < 0 {
		result.RequiredFundingWei = new(big.Int).Sub(bufferedCost, ethBalance).String()
	}
	result.Eligible = true
	result.Reason = "ELIGIBLE"
	return result, nil
}

func positiveCanonicalInteger(value, fallback string) (*big.Int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() <= 0 || parsed.String() != value {
		return nil, fmt.Errorf("value must be a positive canonical decimal integer")
	}
	return parsed, nil
}
