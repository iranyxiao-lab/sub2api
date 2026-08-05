package onchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

var (
	ErrEthereumRPCResponseTooLarge        = errors.New("Ethereum RPC response exceeds configured limit")
	ErrEthereumTransactionReceiptNotFound = errors.New("Ethereum transaction receipt was not found")
	ErrEthereumTransactionNotFound        = errors.New("Ethereum transaction was not found")
)

type EthereumRPCErrorKind string

const (
	EthereumRPCErrorInvalidRequest EthereumRPCErrorKind = "invalid_request"
	EthereumRPCErrorTimeout        EthereumRPCErrorKind = "timeout"
	EthereumRPCErrorTransport      EthereumRPCErrorKind = "transport"
	EthereumRPCErrorHTTP           EthereumRPCErrorKind = "http"
	EthereumRPCErrorRemote         EthereumRPCErrorKind = "remote"
	EthereumRPCErrorResponseLimit  EthereumRPCErrorKind = "response_too_large"
)

type EthereumRPCError struct {
	Kind       EthereumRPCErrorKind
	Method     string
	Retryable  bool
	StatusCode int
	Cause      error
}

func (e *EthereumRPCError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("Ethereum RPC %s failed (%s, status %d): %v", e.Method, e.Kind, e.StatusCode, e.Cause)
	}
	return fmt.Sprintf("Ethereum RPC %s failed (%s): %v", e.Method, e.Kind, e.Cause)
}

func (e *EthereumRPCError) Unwrap() error { return e.Cause }

type EthereumRPCClientOptions struct {
	Endpoint         string
	Timeout          time.Duration
	ResponseMaxBytes int64
	BatchLimit       int
	MaxRetries       int
	RetryBackoff     time.Duration
	HTTPClient       *http.Client
}

type EthereumRPCClient struct {
	rpc          *rpc.Client
	timeout      time.Duration
	batchLimit   int
	maxRetries   int
	retryBackoff time.Duration
}

type EthereumTransactionReceipt struct {
	TransactionHash   string
	BlockHash         string
	BlockNumber       uint64
	Status            uint64
	GasUsed           uint64
	EffectiveGasPrice *big.Int
	Logs              []types.Log
}

type EthereumTransaction struct {
	Hash     string
	ChainID  uint64
	From     string
	To       string
	Nonce    uint64
	GasLimit uint64
	ValueWei string
	Input    []byte
}

func NewEthereumRPCClient(ctx context.Context, options EthereumRPCClientOptions) (*EthereumRPCClient, error) {
	if strings.TrimSpace(options.Endpoint) == "" {
		return nil, fmt.Errorf("Ethereum RPC endpoint is required")
	}
	if options.Timeout <= 0 {
		return nil, fmt.Errorf("Ethereum RPC timeout must be positive")
	}
	if options.ResponseMaxBytes < 1024 {
		return nil, fmt.Errorf("Ethereum RPC response limit must be at least 1024 bytes")
	}
	if options.BatchLimit < 1 {
		return nil, fmt.Errorf("Ethereum RPC batch limit must be positive")
	}
	if options.MaxRetries < 0 || options.MaxRetries > 5 {
		return nil, fmt.Errorf("Ethereum RPC max retries must be between 0 and 5")
	}
	if options.RetryBackoff < 0 {
		return nil, fmt.Errorf("Ethereum RPC retry backoff must be non-negative")
	}

	baseTransport := http.RoundTripper(http.DefaultTransport)
	checkRedirect := func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	if options.HTTPClient != nil {
		if options.HTTPClient.Transport != nil {
			baseTransport = options.HTTPClient.Transport
		}
		if options.HTTPClient.CheckRedirect != nil {
			checkRedirect = options.HTTPClient.CheckRedirect
		}
	}
	httpClient := &http.Client{
		Transport:     ethereumResponseLimitTransport{base: baseTransport, maxBytes: options.ResponseMaxBytes},
		Timeout:       options.Timeout,
		CheckRedirect: checkRedirect,
	}
	rpcClient, err := rpc.DialOptions(ctx, options.Endpoint, rpc.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("dial Ethereum RPC endpoint: %w", err)
	}
	return &EthereumRPCClient{
		rpc: rpcClient, timeout: options.Timeout, batchLimit: options.BatchLimit,
		maxRetries: options.MaxRetries, retryBackoff: options.RetryBackoff,
	}, nil
}

func (c *EthereumRPCClient) Close() {
	if c != nil && c.rpc != nil {
		c.rpc.Close()
	}
}

func (c *EthereumRPCClient) CallContext(ctx context.Context, result any, method string, args ...any) error {
	if c == nil || c.rpc == nil {
		return &EthereumRPCError{Kind: EthereumRPCErrorInvalidRequest, Method: method, Cause: errors.New("client is nil")}
	}
	for attempt := 0; ; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, c.timeout)
		err := c.rpc.CallContext(callCtx, result, method, args...)
		cancel()
		if err == nil {
			return nil
		}
		classified := classifyEthereumRPCError(method, err)
		if !classified.Retryable || attempt >= c.maxRetries {
			return classified
		}
		if err := waitEthereumRPCRetry(ctx, c.retryBackoff); err != nil {
			return classifyEthereumRPCError(method, err)
		}
	}
}

func (c *EthereumRPCClient) BatchCallContext(ctx context.Context, batch []rpc.BatchElem) error {
	if c == nil || c.rpc == nil {
		return &EthereumRPCError{Kind: EthereumRPCErrorInvalidRequest, Method: "batch", Cause: errors.New("client is nil")}
	}
	if len(batch) == 0 || len(batch) > c.batchLimit {
		return &EthereumRPCError{
			Kind: EthereumRPCErrorInvalidRequest, Method: "batch",
			Cause: fmt.Errorf("batch size must be between 1 and %d", c.batchLimit),
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := c.rpc.BatchCallContext(callCtx, batch); err != nil {
		return classifyEthereumRPCError("batch", err)
	}
	return nil
}

func (c *EthereumRPCClient) ChainID(ctx context.Context) (uint64, error) {
	var result hexutil.Big
	if err := c.CallContext(ctx, &result, "eth_chainId"); err != nil {
		return 0, err
	}
	value := (*big.Int)(&result)
	if !value.IsUint64() {
		return 0, fmt.Errorf("Ethereum RPC chain ID is outside uint64")
	}
	return value.Uint64(), nil
}

func (c *EthereumRPCClient) Syncing(ctx context.Context) (bool, error) {
	var raw json.RawMessage
	if err := c.CallContext(ctx, &raw, "eth_syncing"); err != nil {
		return false, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "false" {
		return false, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		return true, nil
	}
	return false, fmt.Errorf("Ethereum RPC eth_syncing returned an invalid response")
}

func (c *EthereumRPCClient) LatestBlockNumber(ctx context.Context) (uint64, error) {
	var result hexutil.Uint64
	if err := c.CallContext(ctx, &result, "eth_blockNumber"); err != nil {
		return 0, err
	}
	return uint64(result), nil
}

func (c *EthereumRPCClient) FinalizedBlock(ctx context.Context) (EthereumBlockRef, error) {
	return c.blockByTag(ctx, rpc.FinalizedBlockNumber.String())
}

func (c *EthereumRPCClient) BlockByNumber(ctx context.Context, number uint64) (EthereumBlockRef, error) {
	return c.blockByTag(ctx, hexutil.EncodeUint64(number))
}

func (c *EthereumRPCClient) blockByTag(ctx context.Context, tag string) (EthereumBlockRef, error) {
	var result struct {
		Number     hexutil.Uint64 `json:"number"`
		Hash       common.Hash    `json:"hash"`
		ParentHash common.Hash    `json:"parentHash"`
		Timestamp  hexutil.Uint64 `json:"timestamp"`
	}
	if err := c.CallContext(ctx, &result, "eth_getBlockByNumber", tag, false); err != nil {
		return EthereumBlockRef{}, err
	}
	if result.Hash == (common.Hash{}) {
		return EthereumBlockRef{}, fmt.Errorf("Ethereum RPC block %s is null or missing its hash", tag)
	}
	block := EthereumBlockRef{
		Number: uint64(result.Number), Hash: result.Hash.Hex(), ParentHash: result.ParentHash.Hex(),
	}
	if result.Timestamp > 0 {
		block.Timestamp = time.Unix(int64(result.Timestamp), 0).UTC()
	}
	return block, nil
}

func (c *EthereumRPCClient) Logs(ctx context.Context, fromBlock, toBlock uint64, contract string, topics [][]common.Hash) ([]types.Log, error) {
	if fromBlock > toBlock {
		return nil, fmt.Errorf("Ethereum log range start %d exceeds end %d", fromBlock, toBlock)
	}
	if !common.IsHexAddress(contract) {
		return nil, fmt.Errorf("Ethereum log contract address is invalid")
	}
	filter := map[string]any{
		"fromBlock": hexutil.EncodeUint64(fromBlock),
		"toBlock":   hexutil.EncodeUint64(toBlock),
		"address":   common.HexToAddress(contract),
	}
	if len(topics) > 0 {
		filter["topics"] = topics
	}
	var result []types.Log
	if err := c.CallContext(ctx, &result, "eth_getLogs", filter); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *EthereumRPCClient) TransactionReceipt(ctx context.Context, transactionHash string) (EthereumTransactionReceipt, error) {
	if !common.IsHexHash(transactionHash) {
		return EthereumTransactionReceipt{}, fmt.Errorf("Ethereum transaction hash is invalid")
	}
	var raw json.RawMessage
	if err := c.CallContext(ctx, &raw, "eth_getTransactionReceipt", common.HexToHash(transactionHash)); err != nil {
		return EthereumTransactionReceipt{}, err
	}
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return EthereumTransactionReceipt{}, ErrEthereumTransactionReceiptNotFound
	}
	var result struct {
		TransactionHash   common.Hash    `json:"transactionHash"`
		BlockHash         common.Hash    `json:"blockHash"`
		BlockNumber       hexutil.Uint64 `json:"blockNumber"`
		Status            hexutil.Uint64 `json:"status"`
		GasUsed           hexutil.Uint64 `json:"gasUsed"`
		EffectiveGasPrice *hexutil.Big   `json:"effectiveGasPrice"`
		Logs              []types.Log    `json:"logs"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return EthereumTransactionReceipt{}, fmt.Errorf("decode Ethereum transaction receipt: %w", err)
	}
	if result.TransactionHash == (common.Hash{}) || result.BlockHash == (common.Hash{}) {
		return EthereumTransactionReceipt{}, fmt.Errorf("Ethereum transaction receipt is missing transaction or block hash")
	}
	receipt := EthereumTransactionReceipt{
		TransactionHash: result.TransactionHash.Hex(),
		BlockHash:       result.BlockHash.Hex(),
		BlockNumber:     uint64(result.BlockNumber),
		Status:          uint64(result.Status),
		GasUsed:         uint64(result.GasUsed),
		Logs:            result.Logs,
	}
	if result.EffectiveGasPrice != nil {
		receipt.EffectiveGasPrice = new(big.Int).Set((*big.Int)(result.EffectiveGasPrice))
	}
	return receipt, nil
}

func (c *EthereumRPCClient) TransactionByHash(ctx context.Context, transactionHash string) (EthereumTransaction, error) {
	if !common.IsHexHash(transactionHash) {
		return EthereumTransaction{}, fmt.Errorf("Ethereum transaction hash is invalid")
	}
	var raw json.RawMessage
	if err := c.CallContext(ctx, &raw, "eth_getTransactionByHash", common.HexToHash(transactionHash)); err != nil {
		return EthereumTransaction{}, err
	}
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return EthereumTransaction{}, ErrEthereumTransactionNotFound
	}
	var result struct {
		Hash    common.Hash     `json:"hash"`
		ChainID *hexutil.Big    `json:"chainId"`
		From    common.Address  `json:"from"`
		To      *common.Address `json:"to"`
		Nonce   hexutil.Uint64  `json:"nonce"`
		Gas     hexutil.Uint64  `json:"gas"`
		Value   *hexutil.Big    `json:"value"`
		Input   hexutil.Bytes   `json:"input"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return EthereumTransaction{}, fmt.Errorf("decode Ethereum transaction: %w", err)
	}
	if result.Hash == (common.Hash{}) || result.ChainID == nil || result.From == (common.Address{}) || result.To == nil || result.Value == nil {
		return EthereumTransaction{}, fmt.Errorf("Ethereum transaction is missing required fields")
	}
	chainID := (*big.Int)(result.ChainID)
	if !chainID.IsUint64() {
		return EthereumTransaction{}, fmt.Errorf("Ethereum transaction chain ID is invalid")
	}
	return EthereumTransaction{Hash: result.Hash.Hex(), ChainID: chainID.Uint64(), From: result.From.Hex(), To: result.To.Hex(), Nonce: uint64(result.Nonce), GasLimit: uint64(result.Gas), ValueWei: (*big.Int)(result.Value).String(), Input: append([]byte(nil), result.Input...)}, nil
}

// CallEthereumWithFailover retries a read against the backup endpoint only
// when the primary failure was classified as transient.
func CallEthereumWithFailover(ctx context.Context, primary, backup *EthereumRPCClient, operation func(context.Context, *EthereumRPCClient) error) error {
	if primary == nil || backup == nil || operation == nil {
		return &EthereumRPCError{Kind: EthereumRPCErrorInvalidRequest, Method: "failover", Cause: errors.New("primary, backup, and operation are required")}
	}
	primaryErr := operation(ctx, primary)
	if primaryErr == nil {
		return nil
	}
	if !IsEthereumRPCRetryable(primaryErr) {
		return primaryErr
	}
	backupErr := operation(ctx, backup)
	if backupErr == nil {
		return nil
	}
	return fmt.Errorf("primary and backup Ethereum RPC reads failed: %w", errors.Join(primaryErr, backupErr))
}

func IsEthereumRPCRetryable(err error) bool {
	var rpcErr *EthereumRPCError
	return errors.As(err, &rpcErr) && rpcErr.Retryable
}

func (c *EthereumRPCClient) ContractCode(ctx context.Context, contract string, block EthereumBlockRef) ([]byte, error) {
	if !common.IsHexAddress(contract) || !common.IsHexHash(block.Hash) {
		return nil, fmt.Errorf("Ethereum contract address and finalized block hash must be valid")
	}
	var result hexutil.Bytes
	blockRef := rpc.BlockNumberOrHashWithHash(common.HexToHash(block.Hash), false)
	if err := c.CallContext(ctx, &result, "eth_getCode", common.HexToAddress(contract), blockRef); err != nil {
		return nil, err
	}
	return []byte(result), nil
}

func (c *EthereumRPCClient) ERC20Decimals(ctx context.Context, contract string, block EthereumBlockRef) (uint8, error) {
	if !common.IsHexAddress(contract) || !common.IsHexHash(block.Hash) {
		return 0, fmt.Errorf("Ethereum contract address and finalized block hash must be valid")
	}
	var result hexutil.Bytes
	call := map[string]any{
		"to":   common.HexToAddress(contract),
		"data": hexutil.Bytes{0x31, 0x3c, 0xe5, 0x67},
	}
	blockRef := rpc.BlockNumberOrHashWithHash(common.HexToHash(block.Hash), false)
	if err := c.CallContext(ctx, &result, "eth_call", call, blockRef); err != nil {
		return 0, err
	}
	if len(result) != 32 {
		return 0, fmt.Errorf("Ethereum ERC20 decimals result must be 32 bytes")
	}
	value := new(big.Int).SetBytes(result)
	if !value.IsUint64() || value.Uint64() > 255 {
		return 0, fmt.Errorf("Ethereum ERC20 decimals result is outside uint8")
	}
	return uint8(value.Uint64()), nil
}

func (c *EthereumRPCClient) EthereumBalance(ctx context.Context, address string) (*big.Int, error) {
	if !common.IsHexAddress(address) {
		return nil, fmt.Errorf("Ethereum balance address is invalid")
	}
	var result hexutil.Big
	if err := c.CallContext(ctx, &result, "eth_getBalance", common.HexToAddress(address), "latest"); err != nil {
		return nil, err
	}
	return new(big.Int).Set((*big.Int)(&result)), nil
}

func (c *EthereumRPCClient) PendingNonce(ctx context.Context, address string) (uint64, error) {
	if !common.IsHexAddress(address) {
		return 0, fmt.Errorf("Ethereum pending nonce address is invalid")
	}
	var result hexutil.Uint64
	if err := c.CallContext(ctx, &result, "eth_getTransactionCount", common.HexToAddress(address), "pending"); err != nil {
		return 0, err
	}
	return uint64(result), nil
}

func (c *EthereumRPCClient) ERC20Balance(ctx context.Context, contract, owner string) (*big.Int, error) {
	if !common.IsHexAddress(contract) || !common.IsHexAddress(owner) {
		return nil, fmt.Errorf("Ethereum ERC20 balance contract or owner is invalid")
	}
	data := append([]byte{0x70, 0xa0, 0x82, 0x31}, common.LeftPadBytes(common.HexToAddress(owner).Bytes(), 32)...)
	var result hexutil.Bytes
	if err := c.CallContext(ctx, &result, "eth_call", map[string]any{
		"to": common.HexToAddress(contract), "data": hexutil.Bytes(data),
	}, "latest"); err != nil {
		return nil, err
	}
	if len(result) != 32 {
		return nil, fmt.Errorf("Ethereum ERC20 balance result must be 32 bytes")
	}
	return new(big.Int).SetBytes(result), nil
}

func (c *EthereumRPCClient) EstimateERC20TransferGas(ctx context.Context, contract, from, destination string, amount *big.Int) (uint64, error) {
	if !common.IsHexAddress(contract) || !common.IsHexAddress(from) || !common.IsHexAddress(destination) || amount == nil || amount.Sign() <= 0 || amount.BitLen() > 256 {
		return 0, fmt.Errorf("Ethereum ERC20 transfer estimate parameters are invalid")
	}
	data := make([]byte, 4, 68)
	copy(data, []byte{0xa9, 0x05, 0x9c, 0xbb})
	data = append(data, common.LeftPadBytes(common.HexToAddress(destination).Bytes(), 32)...)
	data = append(data, amount.FillBytes(make([]byte, 32))...)
	var result hexutil.Uint64
	if err := c.CallContext(ctx, &result, "eth_estimateGas", map[string]any{
		"from": common.HexToAddress(from), "to": common.HexToAddress(contract),
		"value": "0x0", "data": hexutil.Bytes(data),
	}); err != nil {
		return 0, err
	}
	return uint64(result), nil
}

func (c *EthereumRPCClient) SuggestedMaxFeePerGas(ctx context.Context) (*big.Int, error) {
	var tip hexutil.Big
	if err := c.CallContext(ctx, &tip, "eth_maxPriorityFeePerGas"); err != nil {
		return nil, err
	}
	var header struct {
		BaseFeePerGas *hexutil.Big `json:"baseFeePerGas"`
	}
	if err := c.CallContext(ctx, &header, "eth_getBlockByNumber", "latest", false); err != nil {
		return nil, err
	}
	if header.BaseFeePerGas == nil || (*big.Int)(header.BaseFeePerGas).Sign() <= 0 || (*big.Int)(&tip).Sign() <= 0 {
		return nil, fmt.Errorf("Ethereum EIP-1559 fee suggestion is unavailable")
	}
	return new(big.Int).Add(new(big.Int).Mul((*big.Int)(header.BaseFeePerGas), big.NewInt(2)), (*big.Int)(&tip)), nil
}

func classifyEthereumRPCError(method string, err error) *EthereumRPCError {
	classified := &EthereumRPCError{Kind: EthereumRPCErrorRemote, Method: method, Cause: err}
	if errors.Is(err, ErrEthereumRPCResponseTooLarge) {
		classified.Kind = EthereumRPCErrorResponseLimit
		return classified
	}
	if errors.Is(err, context.DeadlineExceeded) {
		classified.Kind = EthereumRPCErrorTimeout
		classified.Retryable = true
		return classified
	}
	if errors.Is(err, context.Canceled) {
		classified.Kind = EthereumRPCErrorTimeout
		return classified
	}
	var httpErr rpc.HTTPError
	if errors.As(err, &httpErr) {
		classified.Kind = EthereumRPCErrorHTTP
		classified.StatusCode = httpErr.StatusCode
		classified.Retryable = httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= 500
		return classified
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		classified.Kind = EthereumRPCErrorTransport
		classified.Retryable = true
		return classified
	}
	var rpcErr rpc.Error
	if errors.As(err, &rpcErr) {
		classified.Kind = EthereumRPCErrorRemote
		classified.Retryable = rpcErr.ErrorCode() == -32002 || rpcErr.ErrorCode() == -32005
	}
	return classified
}

func waitEthereumRPCRetry(ctx context.Context, delay time.Duration) error {
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type ethereumResponseLimitTransport struct {
	base     http.RoundTripper
	maxBytes int64
}

func (t ethereumResponseLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > t.maxBytes {
		response.Body.Close()
		return nil, ErrEthereumRPCResponseTooLarge
	}
	response.Body = &ethereumLimitedReadCloser{body: response.Body, remaining: t.maxBytes}
	return response, nil
}

type ethereumLimitedReadCloser struct {
	body      io.ReadCloser
	remaining int64
}

func (r *ethereumLimitedReadCloser) Read(buffer []byte) (int, error) {
	if r.remaining < 0 {
		return 0, ErrEthereumRPCResponseTooLarge
	}
	limit := int64(len(buffer))
	if limit > r.remaining+1 {
		limit = r.remaining + 1
	}
	n, err := r.body.Read(buffer[:limit])
	if int64(n) > r.remaining {
		r.remaining = -1
		return 0, ErrEthereumRPCResponseTooLarge
	}
	r.remaining -= int64(n)
	return n, err
}

func (r *ethereumLimitedReadCloser) Close() error { return r.body.Close() }
