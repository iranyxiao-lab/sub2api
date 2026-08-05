package provider

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func validERC20ProviderConfig() map[string]string {
	return map[string]string{
		"network": string(onchain.NetworkEthereumMainnet), "chainId": "1",
		"usdtContract": onchain.EthereumMainnetUSDTContract, "usdtDecimals": "6",
		"configVersion": "ethereum-config-v1",
	}
}

func TestCreateProviderRegistersUSDTERC20WithHealthGate(t *testing.T) {
	gate := onchain.NewEthereumHealthGate()
	gate.Record(onchain.EthereumStartupReport{}, nil)
	created, err := CreateProvider(payment.TypeUSDTERC20, "ethereum-1", validERC20ProviderConfig(), WithEthereumHealthGate(gate))
	require.NoError(t, err)
	require.Equal(t, payment.TypeUSDTERC20, created.ProviderKey())
	require.Equal(t, []payment.PaymentType{payment.TypeUSDTERC20}, created.SupportedTypes())
	require.Equal(t, "USDT (ERC20)", created.Name())
	require.NoError(t, created.(payment.AvailabilityProvider).Availability(context.Background()))
	_, err = created.CreatePayment(context.Background(), payment.CreatePaymentRequest{PaymentType: payment.TypeUSDTERC20})
	require.ErrorIs(t, err, ErrEthereumTransactionalCreation)
}

func TestERC20ProviderFailsClosedAndRejectsUnsafeConfig(t *testing.T) {
	created, err := NewERC20("ethereum-1", validERC20ProviderConfig(), nil)
	require.NoError(t, err)
	require.Error(t, created.Availability(context.Background()))

	wrongChain := validERC20ProviderConfig()
	wrongChain["chainId"] = "2"
	_, err = NewERC20("ethereum-1", wrongChain, onchain.NewEthereumHealthGate())
	require.ErrorContains(t, err, "chain ID")
	privateMaterial := validERC20ProviderConfig()
	privateMaterial["privateKey"] = "forbidden"
	_, err = NewERC20("ethereum-1", privateMaterial, onchain.NewEthereumHealthGate())
	require.ErrorContains(t, err, "private key material")
}
