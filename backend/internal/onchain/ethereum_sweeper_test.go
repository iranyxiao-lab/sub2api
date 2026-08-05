package onchain

import (
	"context"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeEthereumSweepPreflightSource struct {
	usdtBalance  *big.Int
	ethBalance   *big.Int
	gasLimit     uint64
	maxFee       *big.Int
	estimates    int
	receipt      EthereumTransactionReceipt
	receiptErr   error
	transactions map[string]EthereumTransaction
	finalized    EthereumBlockRef
	block        EthereumBlockRef
}

func (s *fakeEthereumSweepPreflightSource) TransactionReceipt(context.Context, string) (EthereumTransactionReceipt, error) {
	return s.receipt, s.receiptErr
}

func (s *fakeEthereumSweepPreflightSource) TransactionByHash(_ context.Context, hash string) (EthereumTransaction, error) {
	if value, ok := s.transactions[hash]; ok {
		return value, nil
	}
	return EthereumTransaction{}, ErrEthereumTransactionNotFound
}
func (s *fakeEthereumSweepPreflightSource) FinalizedBlock(context.Context) (EthereumBlockRef, error) {
	return s.finalized, nil
}
func (s *fakeEthereumSweepPreflightSource) BlockByNumber(context.Context, uint64) (EthereumBlockRef, error) {
	return s.block, nil
}

func (s *fakeEthereumSweepPreflightSource) EthereumBalance(context.Context, string) (*big.Int, error) {
	return new(big.Int).Set(s.ethBalance), nil
}

func (s *fakeEthereumSweepPreflightSource) ERC20Balance(context.Context, string, string) (*big.Int, error) {
	return new(big.Int).Set(s.usdtBalance), nil
}

func (s *fakeEthereumSweepPreflightSource) EstimateERC20TransferGas(context.Context, string, string, string, *big.Int) (uint64, error) {
	s.estimates++
	return s.gasLimit, nil
}

func (s *fakeEthereumSweepPreflightSource) SuggestedMaxFeePerGas(context.Context) (*big.Int, error) {
	return new(big.Int).Set(s.maxFee), nil
}

func TestEvaluateEthereumSweepRequiresEconomicThresholdAndBoundedGas(t *testing.T) {
	options := EthereumSweepPreflightOptions{
		ContractAddress: EthereumMainnetUSDTContract,
		SourceAddress:   "0x0000000000000000000000000000000000000002",
		SweepAddress:    "0x0000000000000000000000000000000000000003",
	}

	belowMinimum := &fakeEthereumSweepPreflightSource{
		usdtBalance: big.NewInt(199_999_999), ethBalance: new(big.Int), gasLimit: 100_000, maxFee: big.NewInt(50_000_000_000),
	}
	result, err := EvaluateEthereumSweep(context.Background(), belowMinimum, options)
	require.NoError(t, err)
	require.False(t, result.Eligible)
	require.Equal(t, "BELOW_MINIMUM_SWEEP", result.Reason)
	require.Zero(t, belowMinimum.estimates, "sub-threshold balances must not create gas work")

	overBudget := &fakeEthereumSweepPreflightSource{
		usdtBalance: big.NewInt(200_000_000), ethBalance: new(big.Int), gasLimit: 100_000, maxFee: big.NewInt(50_000_000_001),
	}
	result, err = EvaluateEthereumSweep(context.Background(), overBudget, options)
	require.NoError(t, err)
	require.False(t, result.Eligible)
	require.Equal(t, "GAS_BUDGET_EXCEEDED", result.Reason)
	require.Equal(t, "6000000000120000", result.BufferedGasCostWei)
	require.Equal(t, "0", result.RequiredFundingWei, "over-budget work must not request funding")

	eligible := &fakeEthereumSweepPreflightSource{
		usdtBalance: big.NewInt(250_000_000), ethBalance: big.NewInt(1_000_000_000_000_000),
		gasLimit: 100_000, maxFee: big.NewInt(50_000_000_000),
	}
	result, err = EvaluateEthereumSweep(context.Background(), eligible, options)
	require.NoError(t, err)
	require.True(t, result.Eligible)
	require.Equal(t, "ELIGIBLE", result.Reason)
	require.Equal(t, DefaultEthereumMaxGasBudgetWei, result.BufferedGasCostWei)
	require.Equal(t, "5000000000000000", result.RequiredFundingWei)
}
