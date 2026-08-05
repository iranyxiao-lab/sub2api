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
	ErrEthereumTransactionalCreation = infraerrors.BadRequest("ONCHAIN_TRANSACTION_REQUIRED", "Ethereum payments must be created through the transactional on-chain order flow")
	ErrEthereumManualRefundOnly      = infraerrors.BadRequest("ONCHAIN_REFUND_MANUAL_ONLY", "Ethereum refunds require manual review and controlled execution")
)

type ERC20 struct {
	instanceID    string
	network       onchain.Network
	chainID       uint64
	contract      string
	decimals      uint8
	configVersion string
	healthGate    *onchain.EthereumHealthGate
}

func NewERC20(instanceID string, config map[string]string, healthGate *onchain.EthereumHealthGate) (*ERC20, error) {
	for key := range config {
		lower := strings.ToLower(strings.TrimSpace(key))
		for _, forbidden := range []string{"private", "mnemonic", "seed", "xprv", "secret"} {
			if strings.Contains(lower, forbidden) {
				return nil, fmt.Errorf("Ethereum provider config must not contain private key material: %s", key)
			}
		}
	}
	network := onchain.Network(strings.TrimSpace(config["network"]))
	chainID, err := strconv.ParseUint(strings.TrimSpace(config["chainId"]), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("Ethereum provider chainId must be an integer: %w", err)
	}
	decimalsValue, err := strconv.ParseUint(strings.TrimSpace(config["usdtDecimals"]), 10, 8)
	if err != nil {
		return nil, fmt.Errorf("Ethereum provider usdtDecimals must be an integer: %w", err)
	}
	contract := strings.TrimSpace(config["usdtContract"])
	if err := onchain.ValidateTokenIdentity(network, chainID, contract, uint8(decimalsValue)); err != nil {
		return nil, fmt.Errorf("Ethereum provider token identity: %w", err)
	}
	configVersion := strings.TrimSpace(config["configVersion"])
	if configVersion == "" || len(configVersion) > 64 {
		return nil, fmt.Errorf("Ethereum provider configVersion must contain 1 to 64 characters")
	}
	return &ERC20{
		instanceID: instanceID, network: network, chainID: chainID, contract: contract,
		decimals: uint8(decimalsValue), configVersion: configVersion, healthGate: healthGate,
	}, nil
}

func (e *ERC20) Name() string             { return "USDT (ERC20)" }
func (e *ERC20) ProviderKey() string      { return payment.TypeUSDTERC20 }
func (e *ERC20) Network() onchain.Network { return e.network }
func (e *ERC20) ChainID() uint64          { return e.chainID }
func (e *ERC20) TokenContract() string    { return e.contract }
func (e *ERC20) Decimals() uint8          { return e.decimals }
func (e *ERC20) ConfigVersion() string    { return e.configVersion }

func (e *ERC20) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeUSDTERC20}
}

func (e *ERC20) Availability(context.Context) error { return e.healthGate.RequireNewOrder() }

func (e *ERC20) CreatePayment(ctx context.Context, _ payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	if err := e.Availability(ctx); err != nil {
		return nil, err
	}
	return nil, ErrEthereumTransactionalCreation
}

func (e *ERC20) QueryOrder(context.Context, string) (*payment.QueryOrderResponse, error) {
	return nil, ErrEthereumTransactionalCreation
}

func (e *ERC20) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	return nil, nil
}

func (e *ERC20) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, ErrEthereumManualRefundOnly
}
