package provider

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func validTRONProviderConfig() map[string]string {
	return map[string]string{
		"network":       string(onchain.NetworkTronMainnet),
		"usdtContract":  onchain.TronMainnetUSDTContract,
		"usdtDecimals":  "6",
		"configVersion": "tron-config-v1",
	}
}

func TestCreateProviderRegistersUSDTTRC20WithHealthGate(t *testing.T) {
	gate := onchain.NewTRONHealthGate()
	gate.Record(onchain.TRONHealthReport{Healthy: true, Network: onchain.NetworkTronMainnet}, nil)

	created, err := CreateProvider(payment.TypeUSDTTRC20, "tron-1", validTRONProviderConfig(), WithTRONHealthGate(gate))
	require.NoError(t, err)
	require.Equal(t, payment.TypeUSDTTRC20, created.ProviderKey())
	require.Equal(t, []payment.PaymentType{payment.TypeUSDTTRC20}, created.SupportedTypes())
	require.Equal(t, "USDT (TRC20)", created.Name())
	availability, ok := created.(payment.AvailabilityProvider)
	require.True(t, ok)
	require.NoError(t, availability.Availability(context.Background()))

	_, err = created.CreatePayment(context.Background(), payment.CreatePaymentRequest{PaymentType: payment.TypeUSDTTRC20})
	require.ErrorIs(t, err, ErrTRONTransactionalCreation)
}

func TestTRONProviderFailsClosedWithoutHealthyGate(t *testing.T) {
	created, err := NewTRON("tron-1", validTRONProviderConfig(), nil)
	require.NoError(t, err)
	require.Error(t, created.Availability(context.Background()))
}

func TestTRONProviderRejectsWrongContractAndPrivateMaterial(t *testing.T) {
	wrongContract := validTRONProviderConfig()
	wrongContract["usdtContract"] = "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"
	_, err := NewTRON("tron-1", wrongContract, onchain.NewTRONHealthGate())
	require.ErrorContains(t, err, "allowlist")

	privateMaterial := validTRONProviderConfig()
	privateMaterial["xprv"] = "forbidden"
	_, err = NewTRON("tron-1", privateMaterial, onchain.NewTRONHealthGate())
	require.ErrorContains(t, err, "private key material")
}
