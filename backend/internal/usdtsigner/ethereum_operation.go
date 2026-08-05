package usdtsigner

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

var signerERC20ABI = mustSignerERC20ABI()

type EthereumOperationConfig struct {
	Network                 onchain.Network
	ChainID                 uint64
	RPCURL                  string
	USDTContract            common.Address
	CollectionAddress       common.Address
	Timeout                 time.Duration
	MaxSweepGasLimit        uint64
	MaxFundingGasLimit      uint64
	MaxFeePerGasWei         *big.Int
	MaxPriorityFeePerGasWei *big.Int
	MaxFundingTotalFeeWei   *big.Int
	MaxSweepTotalFeeWei     *big.Int
	PendingThreshold        time.Duration
	ReplacementBumpPercent  uint64
	MaxReplacementAttempts  int
}

func (c EthereumOperationConfig) Validate() error {
	if c.Network != onchain.NetworkEthereumMainnet && c.Network != onchain.NetworkEthereumSepolia {
		return fmt.Errorf("signer Ethereum network is unsupported")
	}
	if err := onchain.ValidateTokenIdentity(c.Network, c.ChainID, c.USDTContract.Hex(), onchain.USDTDecimals); err != nil {
		return fmt.Errorf("signer Ethereum token identity: %w", err)
	}
	if c.CollectionAddress == (common.Address{}) {
		return fmt.Errorf("signer Ethereum collection address is required")
	}
	endpoint, err := url.Parse(strings.TrimSpace(c.RPCURL))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return fmt.Errorf("signer Ethereum RPC URL must be absolute HTTP(S)")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("signer Ethereum RPC URL must not contain credentials, query, or fragment")
	}
	if endpoint.Scheme != "https" {
		host := endpoint.Hostname()
		ip := net.ParseIP(host)
		if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
			return fmt.Errorf("plain HTTP Ethereum RPC must use a private or loopback literal IP")
		}
	}
	if c.Timeout <= 0 || c.Timeout > 30*time.Second {
		return fmt.Errorf("signer Ethereum timeout must be between 1ns and 30s")
	}
	if c.MaxSweepGasLimit < 21_000 || c.MaxFundingGasLimit < 21_000 {
		return fmt.Errorf("signer Ethereum gas limits must be at least 21000")
	}
	for name, value := range map[string]*big.Int{
		"max fee per gas": c.MaxFeePerGasWei, "max priority fee per gas": c.MaxPriorityFeePerGasWei,
		"max funding total fee": c.MaxFundingTotalFeeWei, "max sweep total fee": c.MaxSweepTotalFeeWei,
	} {
		if value == nil || value.Sign() <= 0 {
			return fmt.Errorf("signer Ethereum %s must be positive", name)
		}
	}
	if c.MaxPriorityFeePerGasWei.Cmp(c.MaxFeePerGasWei) > 0 {
		return fmt.Errorf("signer Ethereum priority fee cap exceeds max fee cap")
	}
	if c.PendingThreshold < time.Minute || c.PendingThreshold > 24*time.Hour {
		return fmt.Errorf("signer Ethereum pending threshold must be between one minute and 24 hours")
	}
	if c.ReplacementBumpPercent < 10 || c.ReplacementBumpPercent > 100 || c.MaxReplacementAttempts < 1 || c.MaxReplacementAttempts > 20 {
		return fmt.Errorf("signer Ethereum replacement policy is invalid")
	}
	return nil
}

type EthereumNode interface {
	ChainID(context.Context) (*big.Int, error)
	PendingNonceAt(context.Context, common.Address) (uint64, error)
	BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error)
	CallContract(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error)
	EstimateGas(context.Context, ethereum.CallMsg) (uint64, error)
	SuggestGasTipCap(context.Context) (*big.Int, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
	TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error)
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
	SendTransaction(context.Context, *types.Transaction) error
}

type EthereumBroadcast struct {
	TransactionID  string
	SemanticDigest string
	Nonce          uint64
	GasLimit       uint64
	FeeCap         *big.Int
	TipCap         *big.Int
}

func DialEthereumSignerNode(ctx context.Context, config EthereumOperationConfig, httpClient *http.Client) (*ethclient.Client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: config.Timeout}
	}
	client, err := rpc.DialOptions(ctx, config.RPCURL, rpc.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("connect signer Ethereum RPC: %w", err)
	}
	return ethclient.NewClient(client), nil
}

func ExecuteERC20Sweep(ctx context.Context, config EthereumOperationConfig, node EthereumNode, privateKey *ecdsa.PrivateKey, amount *big.Int) (common.Hash, error) {
	broadcast, err := executeERC20Sweep(ctx, config, node, privateKey, amount, nil)
	if err != nil {
		return common.Hash{}, err
	}
	return common.HexToHash(broadcast.TransactionID), nil
}

func executeERC20Sweep(ctx context.Context, config EthereumOperationConfig, node EthereumNode, privateKey *ecdsa.PrivateKey, amount *big.Int, replacement *EthereumTransactionVersion) (EthereumBroadcast, error) {
	if err := config.Validate(); err != nil {
		return EthereumBroadcast{}, err
	}
	if node == nil || privateKey == nil || amount == nil || amount.Sign() <= 0 || amount.BitLen() > 256 {
		return EthereumBroadcast{}, fmt.Errorf("ERC20 sweep requires node, private key, and positive uint256 amount")
	}
	if err := verifyEthereumNodeChain(ctx, node, config.ChainID); err != nil {
		return EthereumBroadcast{}, err
	}
	source := addressFromPrivateKey(privateKey)
	balance, err := queryERC20Balance(ctx, node, config.USDTContract, source)
	if err != nil {
		return EthereumBroadcast{}, fmt.Errorf("query signer ERC20 balance: %w", err)
	}
	if balance.Cmp(amount) < 0 {
		return EthereumBroadcast{}, fmt.Errorf("ERC20 source balance is lower than requested sweep amount")
	}
	data, err := signerERC20ABI.Pack("transfer", config.CollectionAddress, amount)
	if err != nil {
		return EthereumBroadcast{}, fmt.Errorf("encode signer ERC20 transfer: %w", err)
	}
	call := ethereum.CallMsg{From: source, To: &config.USDTContract, Value: new(big.Int), Data: data}
	gasLimit, feeCap, tipCap, err := ethereumTransactionParameters(ctx, node, call, config.MaxSweepGasLimit, config)
	if err != nil {
		return EthereumBroadcast{}, err
	}
	nonce, err := node.PendingNonceAt(ctx, source)
	if replacement != nil {
		if err == nil && replacement.Nonce != ^uint64(0) && nonce > replacement.Nonce+1 {
			return EthereumBroadcast{}, fmt.Errorf("Ethereum pending nonce has advanced beyond the replacement operation")
		}
		nonce = replacement.Nonce
		gasLimit = replacement.GasLimit
		feeCap, tipCap, err = replacementFees(config, replacement, feeCap, tipCap)
	}
	if err != nil {
		return EthereumBroadcast{}, err
	}
	maximumFee := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), feeCap)
	if maximumFee.Cmp(config.MaxSweepTotalFeeWei) > 0 {
		return EthereumBroadcast{}, fmt.Errorf("ERC20 sweep total fee exceeds signer policy")
	}
	ethBalance, err := node.BalanceAt(ctx, source, nil)
	if err != nil {
		return EthereumBroadcast{}, fmt.Errorf("query signer Ethereum source balance: %w", err)
	}
	if ethBalance.Cmp(maximumFee) < 0 {
		return EthereumBroadcast{}, fmt.Errorf("Ethereum source balance cannot cover maximum sweep fee")
	}
	if err != nil {
		return EthereumBroadcast{}, fmt.Errorf("query signer Ethereum nonce: %w", err)
	}
	transaction := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(config.ChainID), Nonce: nonce,
		GasTipCap: tipCap, GasFeeCap: feeCap, Gas: gasLimit,
		To: &config.USDTContract, Value: new(big.Int), Data: data,
	})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(transaction.ChainId()), privateKey)
	if err != nil {
		return EthereumBroadcast{}, fmt.Errorf("sign ERC20 sweep transaction: %w", err)
	}
	if err := validateSignedERC20Sweep(signed, source, config, amount); err != nil {
		return EthereumBroadcast{}, err
	}
	semanticDigest, err := ethereumSemanticDigest(signed)
	if err != nil || (replacement != nil && semanticDigest != replacement.SemanticDigest) {
		return EthereumBroadcast{}, fmt.Errorf("replacement ERC20 sweep semantic digest mismatch")
	}
	if err := node.SendTransaction(ctx, signed); err != nil {
		return EthereumBroadcast{}, fmt.Errorf("broadcast signer ERC20 sweep: %w", err)
	}
	return ethereumBroadcast(signed, semanticDigest), nil
}

func ExecuteERC20GasFunding(ctx context.Context, config EthereumOperationConfig, node EthereumNode, sponsorKey *ecdsa.PrivateKey, target common.Address, amount *big.Int) (common.Hash, error) {
	broadcast, err := executeERC20GasFunding(ctx, config, node, sponsorKey, target, amount, nil)
	if err != nil {
		return common.Hash{}, err
	}
	return common.HexToHash(broadcast.TransactionID), nil
}

func executeERC20GasFunding(ctx context.Context, config EthereumOperationConfig, node EthereumNode, sponsorKey *ecdsa.PrivateKey, target common.Address, amount *big.Int, replacement *EthereumTransactionVersion) (EthereumBroadcast, error) {
	if err := config.Validate(); err != nil {
		return EthereumBroadcast{}, err
	}
	if node == nil || sponsorKey == nil || target == (common.Address{}) || amount == nil || amount.Sign() <= 0 || amount.BitLen() > 256 {
		return EthereumBroadcast{}, fmt.Errorf("Ethereum gas funding requires node, sponsor key, derived target, and positive amount")
	}
	if err := verifyEthereumNodeChain(ctx, node, config.ChainID); err != nil {
		return EthereumBroadcast{}, err
	}
	sponsor := addressFromPrivateKey(sponsorKey)
	call := ethereum.CallMsg{From: sponsor, To: &target, Value: new(big.Int).Set(amount)}
	gasLimit, feeCap, tipCap, err := ethereumTransactionParameters(ctx, node, call, config.MaxFundingGasLimit, config)
	if err != nil {
		return EthereumBroadcast{}, err
	}
	nonce, err := node.PendingNonceAt(ctx, sponsor)
	if replacement != nil {
		if err == nil && replacement.Nonce != ^uint64(0) && nonce > replacement.Nonce+1 {
			return EthereumBroadcast{}, fmt.Errorf("Ethereum pending nonce has advanced beyond the replacement operation")
		}
		nonce = replacement.Nonce
		gasLimit = replacement.GasLimit
		feeCap, tipCap, err = replacementFees(config, replacement, feeCap, tipCap)
	}
	if err != nil {
		return EthereumBroadcast{}, fmt.Errorf("query signer gas sponsor nonce: %w", err)
	}
	maximumFee := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), feeCap)
	if maximumFee.Cmp(config.MaxFundingTotalFeeWei) > 0 {
		return EthereumBroadcast{}, fmt.Errorf("Ethereum gas funding total fee exceeds signer policy")
	}
	balance, err := node.BalanceAt(ctx, sponsor, nil)
	if err != nil {
		return EthereumBroadcast{}, fmt.Errorf("query signer gas sponsor balance: %w", err)
	}
	required := new(big.Int).Add(new(big.Int).Set(amount), maximumFee)
	if balance.Cmp(required) < 0 {
		return EthereumBroadcast{}, fmt.Errorf("Ethereum gas sponsor balance is insufficient")
	}
	transaction := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).SetUint64(config.ChainID), Nonce: nonce,
		GasTipCap: tipCap, GasFeeCap: feeCap, Gas: gasLimit,
		To: &target, Value: new(big.Int).Set(amount),
	})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(transaction.ChainId()), sponsorKey)
	if err != nil {
		return EthereumBroadcast{}, fmt.Errorf("sign Ethereum gas funding transaction: %w", err)
	}
	if err := validateSignedGasFunding(signed, sponsor, target, amount, config); err != nil {
		return EthereumBroadcast{}, err
	}
	semanticDigest, err := ethereumSemanticDigest(signed)
	if err != nil || (replacement != nil && semanticDigest != replacement.SemanticDigest) {
		return EthereumBroadcast{}, fmt.Errorf("replacement gas funding semantic digest mismatch")
	}
	if err := node.SendTransaction(ctx, signed); err != nil {
		return EthereumBroadcast{}, fmt.Errorf("broadcast signer Ethereum gas funding: %w", err)
	}
	return ethereumBroadcast(signed, semanticDigest), nil
}

func replacementFees(config EthereumOperationConfig, previous *EthereumTransactionVersion, suggestedFee, suggestedTip *big.Int) (*big.Int, *big.Int, error) {
	oldFee, feeOK := new(big.Int).SetString(previous.MaxFeePerGasWei, 10)
	oldTip, tipOK := new(big.Int).SetString(previous.PriorityFeeWei, 10)
	if !feeOK || !tipOK || oldFee.Sign() <= 0 || oldTip.Sign() <= 0 {
		return nil, nil, fmt.Errorf("persisted replacement fees are invalid")
	}
	bump := new(big.Int).SetUint64(100 + config.ReplacementBumpPercent)
	fee := new(big.Int).Div(new(big.Int).Add(new(big.Int).Mul(oldFee, bump), big.NewInt(99)), big.NewInt(100))
	tip := new(big.Int).Div(new(big.Int).Add(new(big.Int).Mul(oldTip, bump), big.NewInt(99)), big.NewInt(100))
	if fee.Cmp(suggestedFee) < 0 {
		fee.Set(suggestedFee)
	}
	if tip.Cmp(suggestedTip) < 0 {
		tip.Set(suggestedTip)
	}
	if fee.Cmp(config.MaxFeePerGasWei) > 0 || tip.Cmp(config.MaxPriorityFeePerGasWei) > 0 || tip.Cmp(fee) > 0 {
		return nil, nil, fmt.Errorf("replacement Ethereum fees exceed signer policy")
	}
	return fee, tip, nil
}

func ethereumSemanticDigest(transaction *types.Transaction) (string, error) {
	if transaction == nil || transaction.To() == nil {
		return "", fmt.Errorf("Ethereum transaction semantics are invalid")
	}
	payload := fmt.Sprintf("v1|%s|%d|%s|%s|%x", transaction.ChainId().String(), transaction.Nonce(), transaction.To().Hex(), transaction.Value().String(), transaction.Data())
	digest := crypto.Keccak256Hash([]byte(payload))
	return digest.Hex(), nil
}

func ethereumBroadcast(transaction *types.Transaction, semanticDigest string) EthereumBroadcast {
	return EthereumBroadcast{TransactionID: transaction.Hash().Hex(), SemanticDigest: semanticDigest, Nonce: transaction.Nonce(), GasLimit: transaction.Gas(), FeeCap: transaction.GasFeeCap(), TipCap: transaction.GasTipCap()}
}

func ethereumTransactionParameters(ctx context.Context, node EthereumNode, call ethereum.CallMsg, maxGas uint64, config EthereumOperationConfig) (uint64, *big.Int, *big.Int, error) {
	gasLimit, err := node.EstimateGas(ctx, call)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("estimate signer Ethereum gas: %w", err)
	}
	if gasLimit < 21_000 || gasLimit > maxGas {
		return 0, nil, nil, fmt.Errorf("estimated Ethereum gas violates signer policy")
	}
	header, err := node.HeaderByNumber(ctx, nil)
	if err != nil || header == nil || header.BaseFee == nil || header.BaseFee.Sign() <= 0 {
		return 0, nil, nil, fmt.Errorf("query signer Ethereum base fee: %w", err)
	}
	tipCap, err := node.SuggestGasTipCap(ctx)
	if err != nil || tipCap == nil || tipCap.Sign() <= 0 {
		return 0, nil, nil, fmt.Errorf("query signer Ethereum priority fee: %w", err)
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), tipCap)
	if tipCap.Cmp(config.MaxPriorityFeePerGasWei) > 0 || feeCap.Cmp(config.MaxFeePerGasWei) > 0 {
		return 0, nil, nil, fmt.Errorf("Ethereum fee estimate exceeds signer policy")
	}
	return gasLimit, feeCap, tipCap, nil
}

func verifyEthereumNodeChain(ctx context.Context, node EthereumNode, expected uint64) error {
	chainID, err := node.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("query signer Ethereum chain ID: %w", err)
	}
	if chainID == nil || !chainID.IsUint64() || chainID.Uint64() != expected {
		return fmt.Errorf("signer Ethereum node chain ID mismatch")
	}
	return nil
}

func queryERC20Balance(ctx context.Context, node EthereumNode, contract, owner common.Address) (*big.Int, error) {
	data, err := signerERC20ABI.Pack("balanceOf", owner)
	if err != nil {
		return nil, err
	}
	result, err := node.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	values, err := signerERC20ABI.Unpack("balanceOf", result)
	if err != nil || len(values) != 1 {
		return nil, fmt.Errorf("decode ERC20 balance result")
	}
	balance, ok := values[0].(*big.Int)
	if !ok || balance.Sign() < 0 || balance.BitLen() > 256 {
		return nil, fmt.Errorf("ERC20 balance result is invalid")
	}
	return new(big.Int).Set(balance), nil
}

func validateSignedERC20Sweep(transaction *types.Transaction, source common.Address, config EthereumOperationConfig, amount *big.Int) error {
	if err := validateDynamicFeeEnvelope(transaction, source, config, config.MaxSweepGasLimit, config.MaxSweepTotalFeeWei); err != nil {
		return err
	}
	if transaction.To() == nil || *transaction.To() != config.USDTContract || transaction.Value().Sign() != 0 {
		return fmt.Errorf("signed ERC20 sweep contract or native value violates policy")
	}
	method, err := signerERC20ABI.MethodById(transaction.Data())
	if err != nil || method.Name != "transfer" {
		return fmt.Errorf("signed ERC20 sweep method violates policy")
	}
	values, err := method.Inputs.Unpack(transaction.Data()[4:])
	if err != nil || len(values) != 2 {
		return fmt.Errorf("signed ERC20 sweep calldata is invalid")
	}
	destination, ok := values[0].(common.Address)
	value, amountOK := values[1].(*big.Int)
	if !ok || !amountOK || destination != config.CollectionAddress || value.Cmp(amount) != 0 {
		return fmt.Errorf("signed ERC20 sweep destination or amount violates policy")
	}
	return nil
}

func validateSignedGasFunding(transaction *types.Transaction, sponsor, target common.Address, amount *big.Int, config EthereumOperationConfig) error {
	if err := validateDynamicFeeEnvelope(transaction, sponsor, config, config.MaxFundingGasLimit, config.MaxFundingTotalFeeWei); err != nil {
		return err
	}
	if transaction.To() == nil || *transaction.To() != target || transaction.Value().Cmp(amount) != 0 || len(transaction.Data()) != 0 {
		return fmt.Errorf("signed Ethereum gas funding target, amount, or calldata violates policy")
	}
	return nil
}

func validateDynamicFeeEnvelope(transaction *types.Transaction, expectedSender common.Address, config EthereumOperationConfig, maxGas uint64, maxTotalFee *big.Int) error {
	if transaction == nil || transaction.Type() != types.DynamicFeeTxType || transaction.ChainId().Cmp(new(big.Int).SetUint64(config.ChainID)) != 0 {
		return fmt.Errorf("signed Ethereum transaction type or chain ID violates policy")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
	if err != nil || sender != expectedSender {
		return fmt.Errorf("signed Ethereum transaction sender violates policy")
	}
	feeCap := transaction.GasFeeCap()
	tipCap := transaction.GasTipCap()
	if transaction.Gas() < 21_000 || transaction.Gas() > maxGas {
		return fmt.Errorf("signed Ethereum transaction gas limit violates policy")
	}
	if feeCap.Sign() <= 0 || tipCap.Sign() <= 0 || tipCap.Cmp(feeCap) > 0 ||
		feeCap.Cmp(config.MaxFeePerGasWei) > 0 || tipCap.Cmp(config.MaxPriorityFeePerGasWei) > 0 {
		return fmt.Errorf("signed Ethereum transaction fee caps violate policy")
	}
	maximumFee := new(big.Int).Mul(new(big.Int).SetUint64(transaction.Gas()), feeCap)
	if maxTotalFee == nil || maxTotalFee.Sign() <= 0 || maximumFee.Cmp(maxTotalFee) > 0 {
		return fmt.Errorf("signed Ethereum transaction total fee violates policy")
	}
	return nil
}

func addressFromPrivateKey(privateKey *ecdsa.PrivateKey) common.Address {
	return crypto.PubkeyToAddress(privateKey.PublicKey)
}

func mustSignerERC20ABI() abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(`[
		{"type":"function","name":"balanceOf","stateMutability":"view","inputs":[{"name":"account","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
		{"type":"function","name":"transfer","stateMutability":"nonpayable","inputs":[{"name":"to","type":"address"},{"name":"value","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}
	]`))
	if err != nil {
		panic(err)
	}
	return parsed
}
