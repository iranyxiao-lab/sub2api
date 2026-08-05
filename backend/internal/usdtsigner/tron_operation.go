package usdtsigner

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/onchain"
	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	tronTransferSelector  = "a9059cbb"
	tronBalanceOfSelector = "70a08231"
)

type TRONOperationConfig struct {
	Network           onchain.Network
	FullNodeURL       string
	USDTContract      string
	CollectionAddress string
	FeeLimitSun       int64
	Timeout           time.Duration
	ResponseMaxBytes  int64
}

func (c TRONOperationConfig) Validate() error {
	if c.Network != onchain.NetworkTronMainnet && c.Network != onchain.NetworkTronNile {
		return fmt.Errorf("signer TRON network is unsupported")
	}
	if err := onchain.ValidateTokenIdentity(c.Network, 0, c.USDTContract, onchain.USDTDecimals); err != nil {
		return fmt.Errorf("signer TRON token identity: %w", err)
	}
	if err := onchain.ValidateAddress(c.Network, c.CollectionAddress); err != nil {
		return fmt.Errorf("signer TRON collection address: %w", err)
	}
	endpoint, err := url.Parse(strings.TrimSpace(c.FullNodeURL))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return fmt.Errorf("signer TRON FullNode URL must be absolute HTTP(S)")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("signer TRON FullNode URL must not contain credentials, query, or fragment")
	}
	if c.FeeLimitSun <= 0 || c.FeeLimitSun > 1_000_000_000 {
		return fmt.Errorf("signer TRON fee limit must be between 1 and 1000000000 sun")
	}
	if c.Timeout <= 0 || c.Timeout > 30*time.Second {
		return fmt.Errorf("signer TRON timeout must be between 1ns and 30s")
	}
	if c.ResponseMaxBytes < 1024 || c.ResponseMaxBytes > 4<<20 {
		return fmt.Errorf("signer TRON response limit must be between 1 KiB and 4 MiB")
	}
	return nil
}

type TRONNode interface {
	NetworkVersion(context.Context) (int64, error)
	TRC20Balance(context.Context, string, string) (*big.Int, error)
	BuildTRC20Transfer(context.Context, TRONTransferIntent) (*TRONUnsignedTransaction, error)
	BroadcastTRC20Transfer(context.Context, *TRONSignedTransaction) (string, error)
}

type TRONTransferIntent struct {
	OwnerAddress string
	Contract     string
	Destination  string
	Amount       *big.Int
	FeeLimitSun  int64
}

type TRONUnsignedTransaction struct {
	TransactionID string
	RawDataHex    string
	RawJSON       json.RawMessage
}

type TRONSignedTransaction struct {
	TransactionID string
	RawJSON       json.RawMessage
}

func ExecuteTRC20Sweep(ctx context.Context, config TRONOperationConfig, node TRONNode, privateKey *ecdsa.PrivateKey, amount *big.Int) (string, error) {
	if err := config.Validate(); err != nil {
		return "", err
	}
	if node == nil || privateKey == nil || amount == nil || amount.Sign() <= 0 {
		return "", fmt.Errorf("TRC20 sweep requires node, private key, and positive amount")
	}
	definition, _ := onchain.NetworkDefinition(config.Network)
	version, err := node.NetworkVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("query signer TRON network: %w", err)
	}
	if version != expectedTRONP2PVersion(config.Network) {
		return "", fmt.Errorf("signer TRON node network mismatch: got %d for %s", version, definition.NetworkID)
	}
	owner := tronAddressFromPrivateKey(privateKey)
	balance, err := node.TRC20Balance(ctx, config.USDTContract, owner)
	if err != nil {
		return "", fmt.Errorf("query signer TRC20 balance: %w", err)
	}
	if balance.Cmp(amount) < 0 {
		return "", fmt.Errorf("TRC20 source balance is lower than requested sweep amount")
	}
	intent := TRONTransferIntent{
		OwnerAddress: owner, Contract: config.USDTContract,
		Destination: config.CollectionAddress, Amount: new(big.Int).Set(amount),
		FeeLimitSun: config.FeeLimitSun,
	}
	unsigned, err := node.BuildTRC20Transfer(ctx, intent)
	if err != nil {
		return "", fmt.Errorf("build signer TRC20 transfer: %w", err)
	}
	if err := validateTRONUnsignedTransaction(unsigned, intent); err != nil {
		return "", err
	}
	signed, err := signTRONTransaction(unsigned, privateKey)
	if err != nil {
		return "", err
	}
	transactionID, err := node.BroadcastTRC20Transfer(ctx, signed)
	if err != nil {
		return "", fmt.Errorf("broadcast signer TRC20 transfer: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(transactionID), unsigned.TransactionID) {
		return "", fmt.Errorf("TRON broadcast transaction ID does not match signed transaction")
	}
	return strings.ToLower(unsigned.TransactionID), nil
}

func validateTRONUnsignedTransaction(transaction *TRONUnsignedTransaction, intent TRONTransferIntent) error {
	if transaction == nil {
		return fmt.Errorf("TRON node returned no transaction")
	}
	rawData, err := hex.DecodeString(strings.TrimSpace(transaction.RawDataHex))
	if err != nil || len(rawData) == 0 {
		return fmt.Errorf("TRON node returned invalid raw transaction bytes")
	}
	digest := sha256.Sum256(rawData)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), strings.TrimSpace(transaction.TransactionID)) {
		return fmt.Errorf("TRON node transaction ID does not match raw transaction bytes")
	}
	var semantic struct {
		TransactionID string `json:"txID"`
		RawDataHex    string `json:"raw_data_hex"`
		RawData       struct {
			FeeLimit int64 `json:"fee_limit"`
			Contract []struct {
				Type      string `json:"type"`
				Parameter struct {
					TypeURL string `json:"type_url"`
					Value   struct {
						OwnerAddress    string `json:"owner_address"`
						ContractAddress string `json:"contract_address"`
						Data            string `json:"data"`
						CallValue       int64  `json:"call_value"`
					} `json:"value"`
				} `json:"parameter"`
			} `json:"contract"`
		} `json:"raw_data"`
	}
	if err := json.Unmarshal(transaction.RawJSON, &semantic); err != nil {
		return fmt.Errorf("decode TRON node transaction: %w", err)
	}
	if !strings.EqualFold(semantic.TransactionID, transaction.TransactionID) || !strings.EqualFold(semantic.RawDataHex, transaction.RawDataHex) {
		return fmt.Errorf("TRON transaction envelope identifiers are inconsistent")
	}
	if semantic.RawData.FeeLimit != intent.FeeLimitSun || len(semantic.RawData.Contract) != 1 {
		return fmt.Errorf("TRON transaction fee or contract count violates policy")
	}
	contract := semantic.RawData.Contract[0]
	if contract.Type != "TriggerSmartContract" || !strings.HasSuffix(contract.Parameter.TypeURL, ".TriggerSmartContract") {
		return fmt.Errorf("TRON transaction contract type violates policy")
	}
	value := contract.Parameter.Value
	if value.OwnerAddress != intent.OwnerAddress || value.ContractAddress != intent.Contract || value.CallValue != 0 {
		return fmt.Errorf("TRON transaction owner, token contract, or call value violates policy")
	}
	expectedData, err := tronTransferData(intent.Destination, intent.Amount)
	if err != nil {
		return err
	}
	if !strings.EqualFold(value.Data, expectedData) {
		return fmt.Errorf("TRON transaction transfer destination or amount violates policy")
	}
	return nil
}

func signTRONTransaction(unsigned *TRONUnsignedTransaction, privateKey *ecdsa.PrivateKey) (*TRONSignedTransaction, error) {
	rawData, _ := hex.DecodeString(unsigned.RawDataHex)
	digest := sha256.Sum256(rawData)
	signature, err := crypto.Sign(digest[:], privateKey)
	if err != nil {
		return nil, fmt.Errorf("sign TRON transaction: %w", err)
	}
	defer zeroBytes(signature)
	var transaction map[string]any
	if err := json.Unmarshal(unsigned.RawJSON, &transaction); err != nil {
		return nil, fmt.Errorf("decode TRON transaction for signing: %w", err)
	}
	transaction["signature"] = []string{hex.EncodeToString(signature)}
	payload, err := json.Marshal(transaction)
	if err != nil {
		return nil, fmt.Errorf("encode signed TRON transaction: %w", err)
	}
	return &TRONSignedTransaction{TransactionID: strings.ToLower(unsigned.TransactionID), RawJSON: payload}, nil
}

type JavaTronSignerClient struct {
	baseURL          *url.URL
	timeout          time.Duration
	responseMaxBytes int64
	httpClient       *http.Client
}

func NewJavaTronSignerClient(config TRONOperationConfig, httpClient *http.Client) (*JavaTronSignerClient, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	baseURL, _ := url.Parse(strings.TrimSpace(config.FullNodeURL))
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &JavaTronSignerClient{baseURL: baseURL, timeout: config.Timeout, responseMaxBytes: config.ResponseMaxBytes, httpClient: httpClient}, nil
}

func (c *JavaTronSignerClient) NetworkVersion(ctx context.Context) (int64, error) {
	var response struct {
		ConfigNodeInfo struct {
			P2PVersion int64 `json:"p2pVersion"`
		} `json:"configNodeInfo"`
	}
	if err := c.post(ctx, "/wallet/getnodeinfo", struct{}{}, &response); err != nil {
		return 0, err
	}
	return response.ConfigNodeInfo.P2PVersion, nil
}

func (c *JavaTronSignerClient) TRC20Balance(ctx context.Context, contract, owner string) (*big.Int, error) {
	parameter, err := tronAddressParameter(owner)
	if err != nil {
		return nil, err
	}
	request := map[string]any{
		"owner_address": owner, "contract_address": contract,
		"function_selector": "balanceOf(address)", "parameter": parameter, "visible": true,
	}
	var response struct {
		Result struct {
			Result  bool   `json:"result"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"result"`
		ConstantResult []string `json:"constant_result"`
	}
	if err := c.post(ctx, "/wallet/triggerconstantcontract", request, &response); err != nil {
		return nil, err
	}
	if !response.Result.Result || len(response.ConstantResult) != 1 {
		return nil, fmt.Errorf("Java-Tron rejected balance query: %s", response.Result.Code)
	}
	value, ok := new(big.Int).SetString(response.ConstantResult[0], 16)
	if !ok || value.Sign() < 0 || value.BitLen() > 256 {
		return nil, fmt.Errorf("Java-Tron returned invalid TRC20 balance")
	}
	return value, nil
}

func (c *JavaTronSignerClient) BuildTRC20Transfer(ctx context.Context, intent TRONTransferIntent) (*TRONUnsignedTransaction, error) {
	data, err := tronTransferData(intent.Destination, intent.Amount)
	if err != nil {
		return nil, err
	}
	request := map[string]any{
		"owner_address": intent.OwnerAddress, "contract_address": intent.Contract,
		"function_selector": "transfer(address,uint256)", "parameter": strings.TrimPrefix(data, tronTransferSelector),
		"fee_limit": intent.FeeLimitSun, "call_value": 0, "visible": true,
	}
	var response struct {
		Result struct {
			Result  bool   `json:"result"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"result"`
		Transaction json.RawMessage `json:"transaction"`
	}
	if err := c.post(ctx, "/wallet/triggersmartcontract", request, &response); err != nil {
		return nil, err
	}
	if !response.Result.Result || len(response.Transaction) == 0 {
		return nil, fmt.Errorf("Java-Tron rejected transfer construction: %s", response.Result.Code)
	}
	var identity struct {
		TransactionID string `json:"txID"`
		RawDataHex    string `json:"raw_data_hex"`
	}
	if err := json.Unmarshal(response.Transaction, &identity); err != nil {
		return nil, fmt.Errorf("decode Java-Tron transaction identity: %w", err)
	}
	return &TRONUnsignedTransaction{TransactionID: identity.TransactionID, RawDataHex: identity.RawDataHex, RawJSON: response.Transaction}, nil
}

func (c *JavaTronSignerClient) BroadcastTRC20Transfer(ctx context.Context, transaction *TRONSignedTransaction) (string, error) {
	var response struct {
		Result  bool   `json:"result"`
		TxID    string `json:"txid"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := c.postRaw(ctx, "/wallet/broadcasttransaction", transaction.RawJSON, &response); err != nil {
		return "", err
	}
	if !response.Result {
		return "", fmt.Errorf("Java-Tron rejected broadcast: %s", response.Code)
	}
	if response.TxID == "" {
		response.TxID = transaction.TransactionID
	}
	return response.TxID, nil
}

func (c *JavaTronSignerClient) post(ctx context.Context, path string, request, response any) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return c.postRaw(ctx, path, payload, response)
}

func (c *JavaTronSignerClient) postRaw(ctx context.Context, path string, payload []byte, response any) error {
	requestURL, err := url.JoinPath(c.baseURL.String(), path)
	if err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	result, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	body, err := io.ReadAll(io.LimitReader(result.Body, c.responseMaxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > c.responseMaxBytes {
		return fmt.Errorf("Java-Tron signer response exceeds configured limit")
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return fmt.Errorf("Java-Tron signer endpoint returned HTTP %d", result.StatusCode)
	}
	if err := json.Unmarshal(body, response); err != nil {
		return fmt.Errorf("decode Java-Tron signer response: %w", err)
	}
	return nil
}

func tronTransferData(destination string, amount *big.Int) (string, error) {
	if amount == nil || amount.Sign() <= 0 || amount.BitLen() > 256 {
		return "", fmt.Errorf("TRC20 transfer amount must be a positive uint256")
	}
	parameter, err := tronAddressParameter(destination)
	if err != nil {
		return "", err
	}
	return tronTransferSelector + parameter + hex.EncodeToString(amount.FillBytes(make([]byte, 32))), nil
}

func tronAddressParameter(address string) (string, error) {
	payload, version, err := base58.CheckDecode(strings.TrimSpace(address))
	if err != nil || version != 0x41 || len(payload) != 20 {
		return "", fmt.Errorf("invalid TRON address parameter")
	}
	return hex.EncodeToString(append(make([]byte, 12), payload...)), nil
}

func tronAddressFromPrivateKey(privateKey *ecdsa.PrivateKey) string {
	return base58.CheckEncode(crypto.PubkeyToAddress(privateKey.PublicKey).Bytes(), 0x41)
}

func expectedTRONP2PVersion(network onchain.Network) int64 {
	if network == onchain.NetworkTronMainnet {
		return onchain.TronMainnetP2PVersion
	}
	return onchain.TronNileP2PVersion
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
