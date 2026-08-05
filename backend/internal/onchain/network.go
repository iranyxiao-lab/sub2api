package onchain

import (
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/ethereum/go-ethereum/common"
)

type Network string

const (
	NetworkTronMainnet     Network = "tron-mainnet"
	NetworkTronNile        Network = "tron-nile"
	NetworkEthereumMainnet Network = "ethereum-mainnet"
	NetworkEthereumSepolia Network = "ethereum-sepolia"
)

const (
	USDTDecimals                uint8  = 6
	EthereumMainnetChainID      uint64 = 1
	EthereumSepoliaChainID      uint64 = 11155111
	TronMainnetP2PVersion       int64  = 11111
	TronNileP2PVersion          int64  = 201910292
	TronMainnetUSDTContract            = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	EthereumMainnetUSDTContract        = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
)

// Definition is the immutable identity of a supported chain. TRON does not
// expose an Ethereum-style numeric chain ID, so NetworkID is authoritative for
// TRON while ChainID is authoritative for Ethereum.
type Definition struct {
	Network        Network
	NetworkID      string
	ChainID        uint64
	Production     bool
	USDTContract   string
	USDTDecimals   uint8
	CoinType       uint32
	DerivationPath string
}

var definitions = map[Network]Definition{
	NetworkTronMainnet: {
		Network: NetworkTronMainnet, NetworkID: "mainnet", Production: true,
		USDTContract: TronMainnetUSDTContract, USDTDecimals: USDTDecimals,
		CoinType: 195, DerivationPath: "m/44'/195'/0'/0/index",
	},
	NetworkTronNile: {
		Network: NetworkTronNile, NetworkID: "nile", Production: false,
		USDTDecimals: USDTDecimals, CoinType: 195,
		DerivationPath: "m/44'/195'/0'/0/index",
	},
	NetworkEthereumMainnet: {
		Network: NetworkEthereumMainnet, NetworkID: "mainnet",
		ChainID: EthereumMainnetChainID, Production: true,
		USDTContract: EthereumMainnetUSDTContract, USDTDecimals: USDTDecimals,
		CoinType: 60, DerivationPath: "m/44'/60'/0'/0/index",
	},
	NetworkEthereumSepolia: {
		Network: NetworkEthereumSepolia, NetworkID: "sepolia",
		ChainID: EthereumSepoliaChainID, Production: false,
		USDTDecimals: USDTDecimals, CoinType: 60,
		DerivationPath: "m/44'/60'/0'/0/index",
	},
}

func NetworkDefinition(network Network) (Definition, bool) {
	definition, ok := definitions[network]
	return definition, ok
}

// ValidateTokenIdentity fails closed for production and requires test-token
// contracts to be explicitly supplied by the deployment.
func ValidateTokenIdentity(network Network, chainID uint64, contract string, decimals uint8) error {
	definition, ok := NetworkDefinition(network)
	if !ok {
		return fmt.Errorf("unsupported network %q", network)
	}
	contract = strings.TrimSpace(contract)
	if contract == "" {
		return fmt.Errorf("USDT contract is required for %s", network)
	}
	if decimals != definition.USDTDecimals {
		return fmt.Errorf("USDT decimals for %s must be %d", network, definition.USDTDecimals)
	}
	if definition.ChainID != chainID {
		return fmt.Errorf("chain ID for %s must be %d", network, definition.ChainID)
	}
	if err := ValidateAddress(network, contract); err != nil {
		return fmt.Errorf("invalid USDT contract for %s: %w", network, err)
	}
	if definition.Production && !sameAddress(network, definition.USDTContract, contract) {
		return fmt.Errorf("USDT contract for %s is not in the production allowlist", network)
	}
	return nil
}

func ValidateAddress(network Network, address string) error {
	address = strings.TrimSpace(address)
	switch network {
	case NetworkTronMainnet, NetworkTronNile:
		payload, version, err := base58.CheckDecode(address)
		if err != nil {
			return fmt.Errorf("invalid TRON Base58Check address: %w", err)
		}
		if version != 0x41 || len(payload) != 20 {
			return fmt.Errorf("TRON address must contain version 0x41 and a 20-byte payload")
		}
		return nil
	case NetworkEthereumMainnet, NetworkEthereumSepolia:
		if !common.IsHexAddress(address) {
			return fmt.Errorf("invalid Ethereum address")
		}
		return nil
	default:
		return fmt.Errorf("unsupported network %q", network)
	}
}

func sameAddress(network Network, left, right string) bool {
	switch network {
	case NetworkEthereumMainnet, NetworkEthereumSepolia:
		return common.HexToAddress(left) == common.HexToAddress(right)
	default:
		return left == right
	}
}
