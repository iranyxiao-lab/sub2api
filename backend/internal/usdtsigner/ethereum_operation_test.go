package usdtsigner

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

type fakeEthereumNode struct {
	chainID      *big.Int
	tokenBalance *big.Int
	ethBalance   *big.Int
	nonce        uint64
	gasLimit     uint64
	baseFee      *big.Int
	tip          *big.Int
	sent         []*types.Transaction
	transactions map[common.Hash]*types.Transaction
	receipts     map[common.Hash]*types.Receipt
}

func (f *fakeEthereumNode) ChainID(context.Context) (*big.Int, error) {
	return new(big.Int).Set(f.chainID), nil
}

func (f *fakeEthereumNode) PendingNonceAt(context.Context, common.Address) (uint64, error) {
	return f.nonce, nil
}

func (f *fakeEthereumNode) BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error) {
	return new(big.Int).Set(f.ethBalance), nil
}

func (f *fakeEthereumNode) CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
	return signerERC20ABI.Methods["balanceOf"].Outputs.Pack(f.tokenBalance)
}

func (f *fakeEthereumNode) EstimateGas(context.Context, ethereum.CallMsg) (uint64, error) {
	return f.gasLimit, nil
}

func (f *fakeEthereumNode) SuggestGasTipCap(context.Context) (*big.Int, error) {
	return new(big.Int).Set(f.tip), nil
}

func (f *fakeEthereumNode) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return &types.Header{BaseFee: new(big.Int).Set(f.baseFee)}, nil
}

func (f *fakeEthereumNode) TransactionByHash(_ context.Context, hash common.Hash) (*types.Transaction, bool, error) {
	if transaction := f.transactions[hash]; transaction != nil {
		return transaction, true, nil
	}
	return nil, false, ethereum.NotFound
}

func (f *fakeEthereumNode) TransactionReceipt(_ context.Context, hash common.Hash) (*types.Receipt, error) {
	if receipt := f.receipts[hash]; receipt != nil {
		return receipt, nil
	}
	return nil, ethereum.NotFound
}

func (f *fakeEthereumNode) SendTransaction(_ context.Context, transaction *types.Transaction) error {
	f.sent = append(f.sent, transaction)
	f.transactions[transaction.Hash()] = transaction
	return nil
}

func TestExecuteERC20SweepBuildsAndVerifiesFixedTransfer(t *testing.T) {
	config := testEthereumOperationConfig()
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	node := testEthereumNode()

	hash, err := ExecuteERC20Sweep(context.Background(), config, node, privateKey, big.NewInt(250_000_000))
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, hash)
	require.Len(t, node.sent, 1)
	transaction := node.sent[0]
	require.EqualValues(t, types.DynamicFeeTxType, transaction.Type())
	require.Equal(t, config.USDTContract, *transaction.To())
	require.NoError(t, validateSignedERC20Sweep(transaction, addressFromPrivateKey(privateKey), config, big.NewInt(250_000_000)))
}

func TestExecuteERC20GasFundingUsesDerivedTargetAndSponsor(t *testing.T) {
	config := testEthereumOperationConfig()
	sponsorKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	target := common.HexToAddress("0x3000000000000000000000000000000000000003")
	node := testEthereumNode()
	node.gasLimit = 21_000
	amount := big.NewInt(3_000_000_000_000_000)

	hash, err := ExecuteERC20GasFunding(context.Background(), config, node, sponsorKey, target, amount)
	require.NoError(t, err)
	require.NotEqual(t, common.Hash{}, hash)
	require.Len(t, node.sent, 1)
	require.Equal(t, target, *node.sent[0].To())
	require.NoError(t, validateSignedGasFunding(node.sent[0], addressFromPrivateKey(sponsorKey), target, amount, config))
}

func TestEthereumOperationsFailClosedOnChainFeeAndBalanceMismatch(t *testing.T) {
	config := testEthereumOperationConfig()
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	node := testEthereumNode()
	node.chainID = big.NewInt(1)
	_, err = ExecuteERC20Sweep(context.Background(), config, node, privateKey, big.NewInt(1))
	require.ErrorContains(t, err, "chain ID mismatch")
	require.Empty(t, node.sent)

	node = testEthereumNode()
	node.tip = new(big.Int).Add(config.MaxPriorityFeePerGasWei, big.NewInt(1))
	_, err = ExecuteERC20Sweep(context.Background(), config, node, privateKey, big.NewInt(1))
	require.ErrorContains(t, err, "fee estimate exceeds")
	require.Empty(t, node.sent)

	node = testEthereumNode()
	node.tokenBalance = big.NewInt(0)
	_, err = ExecuteERC20Sweep(context.Background(), config, node, privateKey, big.NewInt(1))
	require.ErrorContains(t, err, "source balance")
	require.Empty(t, node.sent)
}

func TestSignedEthereumOperationsRejectTargetAndAmountTampering(t *testing.T) {
	config := testEthereumOperationConfig()
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	source := addressFromPrivateKey(privateKey)
	amount := big.NewInt(250_000_000)
	wrongDestination := common.HexToAddress("0x7000000000000000000000000000000000000007")
	data, err := signerERC20ABI.Pack("transfer", wrongDestination, amount)
	require.NoError(t, err)
	unsignedSweep := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(config.ChainID), Nonce: 1,
		GasTipCap: big.NewInt(1_000_000_000), GasFeeCap: big.NewInt(21_000_000_000), Gas: 65_000,
		To: &config.USDTContract, Value: new(big.Int), Data: data,
	})
	tamperedSweep, err := types.SignTx(unsignedSweep, types.LatestSignerForChainID(unsignedSweep.ChainId()), privateKey)
	require.NoError(t, err)
	require.ErrorContains(t, validateSignedERC20Sweep(tamperedSweep, source, config, amount), "destination or amount")

	expectedTarget := common.HexToAddress("0x3000000000000000000000000000000000000003")
	wrongTarget := common.HexToAddress("0x4000000000000000000000000000000000000004")
	fundingAmount := big.NewInt(3_000_000_000_000_000)
	unsignedFunding := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(config.ChainID), Nonce: 2,
		GasTipCap: big.NewInt(1_000_000_000), GasFeeCap: big.NewInt(21_000_000_000), Gas: 21_000,
		To: &wrongTarget, Value: new(big.Int).Set(fundingAmount),
	})
	tamperedFunding, err := types.SignTx(unsignedFunding, types.LatestSignerForChainID(unsignedFunding.ChainId()), privateKey)
	require.NoError(t, err)
	require.ErrorContains(t, validateSignedGasFunding(tamperedFunding, source, expectedTarget, fundingAmount, config), "target, amount, or calldata")
}

func TestSignedEthereumOperationsEnforceDynamicFeeEnvelopeLimits(t *testing.T) {
	config := testEthereumOperationConfig()
	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	source := addressFromPrivateKey(privateKey)
	target := common.HexToAddress("0x3000000000000000000000000000000000000003")
	amount := big.NewInt(1_000_000_000_000_000)

	tests := []struct {
		name     string
		chainID  uint64
		gas      uint64
		feeCap   *big.Int
		tipCap   *big.Int
		contains string
	}{
		{name: "wrong chain", chainID: 1, gas: 21_000, feeCap: big.NewInt(21_000_000_000), tipCap: big.NewInt(1_000_000_000), contains: "type or chain ID"},
		{name: "gas below intrinsic", chainID: config.ChainID, gas: 20_999, feeCap: big.NewInt(21_000_000_000), tipCap: big.NewInt(1_000_000_000), contains: "gas limit"},
		{name: "gas above funding cap", chainID: config.ChainID, gas: config.MaxFundingGasLimit + 1, feeCap: big.NewInt(21_000_000_000), tipCap: big.NewInt(1_000_000_000), contains: "gas limit"},
		{name: "fee above cap", chainID: config.ChainID, gas: 21_000, feeCap: new(big.Int).Add(config.MaxFeePerGasWei, big.NewInt(1)), tipCap: big.NewInt(1_000_000_000), contains: "fee caps"},
		{name: "priority above fee", chainID: config.ChainID, gas: 21_000, feeCap: big.NewInt(1_000_000_000), tipCap: big.NewInt(2_000_000_000), contains: "fee caps"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			unsigned := types.NewTx(&types.DynamicFeeTx{
				ChainID: new(big.Int).SetUint64(test.chainID), Nonce: 2,
				GasTipCap: test.tipCap, GasFeeCap: test.feeCap, Gas: test.gas,
				To: &target, Value: new(big.Int).Set(amount),
			})
			signed, signErr := types.SignTx(unsigned, types.LatestSignerForChainID(unsigned.ChainId()), privateKey)
			require.NoError(t, signErr)
			require.ErrorContains(t, validateSignedGasFunding(signed, source, target, amount, config), test.contains)
		})
	}

	data, err := signerERC20ABI.Pack("transfer", config.CollectionAddress, big.NewInt(250_000_000))
	require.NoError(t, err)
	unsignedSweep := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(config.ChainID), Nonce: 3,
		GasTipCap: big.NewInt(1_000_000_000), GasFeeCap: big.NewInt(70_000_000_000), Gas: 100_000,
		To: &config.USDTContract, Value: new(big.Int), Data: data,
	})
	signedSweep, err := types.SignTx(unsignedSweep, types.LatestSignerForChainID(unsignedSweep.ChainId()), privateKey)
	require.NoError(t, err)
	require.ErrorContains(t, validateSignedERC20Sweep(signedSweep, source, config, big.NewInt(250_000_000)), "total fee")
}

func testEthereumOperationConfig() EthereumOperationConfig {
	return EthereumOperationConfig{
		Network: onchain.NetworkEthereumSepolia, ChainID: onchain.EthereumSepoliaChainID,
		RPCURL:            "https://ethereum-node.internal",
		USDTContract:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		CollectionAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Timeout:           5 * time.Second, MaxSweepGasLimit: 100_000, MaxFundingGasLimit: 30_000,
		MaxFeePerGasWei: big.NewInt(100_000_000_000), MaxPriorityFeePerGasWei: big.NewInt(5_000_000_000),
		MaxFundingTotalFeeWei: big.NewInt(6_000_000_000_000_000), MaxSweepTotalFeeWei: big.NewInt(6_000_000_000_000_000),
		PendingThreshold: 15 * time.Minute, ReplacementBumpPercent: 15, MaxReplacementAttempts: 5,
	}
}

func testEthereumNode() *fakeEthereumNode {
	return &fakeEthereumNode{
		chainID: new(big.Int).SetUint64(onchain.EthereumSepoliaChainID), tokenBalance: big.NewInt(1_000_000_000),
		ethBalance: big.NewInt(2_000_000_000_000_000_000), nonce: 7, gasLimit: 65_000,
		baseFee: big.NewInt(10_000_000_000), tip: big.NewInt(1_000_000_000),
		transactions: make(map[common.Hash]*types.Transaction), receipts: make(map[common.Hash]*types.Receipt),
	}
}
