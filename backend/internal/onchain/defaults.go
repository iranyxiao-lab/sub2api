package onchain

import "math/big"

const (
	DefaultMaxPendingOrders = 3
	DefaultDailyLimitRaw    = "0"
	DefaultMinRechargeRaw   = "10000000"
	DefaultMaxRechargeRaw   = "100000000000"
	DefaultTronSweepRaw     = "50000000"
	DefaultEthereumSweepRaw = "200000000"
	DefaultMaxGasWei        = "6000000000000000"
	DefaultHotWalletWarnRaw = "50000000000"
	DefaultHotWalletStopRaw = "100000000000"
)

type ProductionDefaults struct {
	MinRechargeRaw       *big.Int
	MaxRechargeRaw       *big.Int
	MaxPendingOrders     int
	DailyLimitRaw        *big.Int
	TronMinSweepRaw      *big.Int
	EthereumMinSweepRaw  *big.Int
	EthereumMaxGasWei    *big.Int
	HotWalletWarnRaw     *big.Int
	HotWalletColdStopRaw *big.Int
}

func DefaultProductionPolicy() ProductionDefaults {
	return ProductionDefaults{
		MinRechargeRaw:       mustDecimalInteger(DefaultMinRechargeRaw),
		MaxRechargeRaw:       mustDecimalInteger(DefaultMaxRechargeRaw),
		MaxPendingOrders:     DefaultMaxPendingOrders,
		DailyLimitRaw:        mustDecimalInteger(DefaultDailyLimitRaw),
		TronMinSweepRaw:      mustDecimalInteger(DefaultTronSweepRaw),
		EthereumMinSweepRaw:  mustDecimalInteger(DefaultEthereumSweepRaw),
		EthereumMaxGasWei:    mustDecimalInteger(DefaultMaxGasWei),
		HotWalletWarnRaw:     mustDecimalInteger(DefaultHotWalletWarnRaw),
		HotWalletColdStopRaw: mustDecimalInteger(DefaultHotWalletStopRaw),
	}
}

func mustDecimalInteger(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		panic("invalid on-chain production default")
	}
	return result
}
