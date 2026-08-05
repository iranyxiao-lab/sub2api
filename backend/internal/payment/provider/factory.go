package provider

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
)

type FactoryOption func(*factoryOptions)

type factoryOptions struct {
	tronHealthGate     *onchain.TRONHealthGate
	ethereumHealthGate *onchain.EthereumHealthGate
}

func WithTRONHealthGate(gate *onchain.TRONHealthGate) FactoryOption {
	return func(options *factoryOptions) { options.tronHealthGate = gate }
}

func WithEthereumHealthGate(gate *onchain.EthereumHealthGate) FactoryOption {
	return func(options *factoryOptions) { options.ethereumHealthGate = gate }
}

// CreateProvider creates a Provider from a provider key, instance ID and decrypted config.
func CreateProvider(providerKey string, instanceID string, config map[string]string, opts ...FactoryOption) (payment.Provider, error) {
	options := factoryOptions{}
	for _, option := range opts {
		option(&options)
	}
	switch providerKey {
	case payment.TypeEasyPay:
		return NewEasyPay(instanceID, config)
	case payment.TypeAlipay:
		return NewAlipay(instanceID, config)
	case payment.TypeWxpay:
		return NewWxpay(instanceID, config)
	case payment.TypeStripe:
		return NewStripe(instanceID, config)
	case payment.TypeAirwallex:
		return NewAirwallex(instanceID, config)
	case payment.TypeUSDTTRC20:
		return NewTRON(instanceID, config, options.tronHealthGate)
	case payment.TypeUSDTERC20:
		return NewERC20(instanceID, config, options.ethereumHealthGate)
	default:
		return nil, fmt.Errorf("unknown provider key: %s", providerKey)
	}
}
