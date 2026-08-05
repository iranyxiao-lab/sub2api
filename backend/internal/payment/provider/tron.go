package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrTRONTransactionalCreation = infraerrors.BadRequest("ONCHAIN_TRANSACTION_REQUIRED", "TRON payments must be created through the transactional on-chain order flow")
	ErrTRONManualRefundOnly      = infraerrors.BadRequest("ONCHAIN_REFUND_MANUAL_ONLY", "TRON refunds require manual review and controlled execution")
)

type TRON struct {
	instanceID    string
	network       onchain.Network
	contract      string
	decimals      uint8
	configVersion string
	healthGate    *onchain.TRONHealthGate
}

func NewTRON(instanceID string, config map[string]string, healthGate *onchain.TRONHealthGate) (*TRON, error) {
	for key := range config {
		lower := strings.ToLower(strings.TrimSpace(key))
		for _, forbidden := range []string{"private", "mnemonic", "seed", "xprv", "secret"} {
			if strings.Contains(lower, forbidden) {
				return nil, fmt.Errorf("TRON provider config must not contain private key material: %s", key)
			}
		}
	}
	network := onchain.Network(strings.TrimSpace(config["network"]))
	decimalsValue, err := strconv.ParseUint(strings.TrimSpace(config["usdtDecimals"]), 10, 8)
	if err != nil {
		return nil, fmt.Errorf("TRON provider usdtDecimals must be an integer: %w", err)
	}
	contract := strings.TrimSpace(config["usdtContract"])
	if err := onchain.ValidateTokenIdentity(network, 0, contract, uint8(decimalsValue)); err != nil {
		return nil, fmt.Errorf("TRON provider token identity: %w", err)
	}
	configVersion := strings.TrimSpace(config["configVersion"])
	if configVersion == "" || len(configVersion) > 64 {
		return nil, fmt.Errorf("TRON provider configVersion must contain 1 to 64 characters")
	}
	return &TRON{
		instanceID: instanceID, network: network, contract: contract,
		decimals: uint8(decimalsValue), configVersion: configVersion, healthGate: healthGate,
	}, nil
}

func (t *TRON) Name() string { return "USDT (TRC20)" }

func (t *TRON) ProviderKey() string { return payment.TypeUSDTTRC20 }

func (t *TRON) Network() onchain.Network { return t.network }

func (t *TRON) TokenContract() string { return t.contract }

func (t *TRON) Decimals() uint8 { return t.decimals }

func (t *TRON) ConfigVersion() string { return t.configVersion }

func (t *TRON) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeUSDTTRC20}
}

func (t *TRON) Availability(context.Context) error {
	return t.healthGate.RequireNewOrder()
}

func (t *TRON) CreatePayment(ctx context.Context, _ payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	if err := t.Availability(ctx); err != nil {
		return nil, err
	}
	return nil, ErrTRONTransactionalCreation
}

func (t *TRON) QueryOrder(context.Context, string) (*payment.QueryOrderResponse, error) {
	return nil, ErrTRONTransactionalCreation
}

func (t *TRON) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	return nil, nil
}

func (t *TRON) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, ErrTRONManualRefundOnly
}
