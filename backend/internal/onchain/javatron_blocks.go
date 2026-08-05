package onchain

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	javaTronSolidifiedNowBlockEndpoint = "/walletsolidity/getnowblock"
	javaTronSolidifiedBlockEndpoint    = "/walletsolidity/getblockbynum"
	javaTronSolidifiedReceiptsEndpoint = "/walletsolidity/gettransactioninfobyblocknum"
)

// TRONSolidifiedBlock is the normalized block identity used by the scanner.
type TRONSolidifiedBlock struct {
	Height         int64
	Hash           string
	ParentHash     string
	Timestamp      time.Time
	TransactionIDs []string
}

// TRONTransactionReceipt is a normalized Java-Tron TransactionInfo response.
// Result and ReceiptResult are kept separate so event validation can fail
// closed against both execution result fields.
type TRONTransactionReceipt struct {
	TransactionID  string
	BlockHeight    int64
	BlockTimestamp time.Time
	Result         string
	ReceiptResult  string
	ContractResult []string
	Logs           []TRONTransactionLog
	FeeSun         int64
	EnergyUsed     int64
	BandwidthUsed  int64
}

type TRONTransactionLog struct {
	Index           int
	ContractAddress string
	Topics          []string
	Data            string
}

type javaTronSolidifiedBlockResponse struct {
	BlockID     string `json:"blockID"`
	BlockHeader struct {
		RawData struct {
			Number     int64  `json:"number"`
			Timestamp  int64  `json:"timestamp"`
			ParentHash string `json:"parentHash"`
		} `json:"raw_data"`
	} `json:"block_header"`
	Transactions []struct {
		TransactionID string `json:"txID"`
	} `json:"transactions"`
}

type javaTronTransactionInfoResponse struct {
	ID                 string   `json:"id"`
	BlockNumber        int64    `json:"blockNumber"`
	BlockTimestamp     int64    `json:"blockTimeStamp"`
	Result             string   `json:"result"`
	ContractResult     []string `json:"contractResult"`
	TransactionReceipt struct {
		Result           string `json:"result"`
		EnergyUsageTotal int64  `json:"energy_usage_total"`
		NetUsage         int64  `json:"net_usage"`
	} `json:"receipt"`
	Fee  int64 `json:"fee"`
	Logs []struct {
		Address string   `json:"address"`
		Topics  []string `json:"topics"`
		Data    string   `json:"data"`
	} `json:"log"`
}

func (c *JavaTronClient) LatestSolidifiedHeight(ctx context.Context) (int64, error) {
	block, err := c.latestSolidifiedBlock(ctx)
	if err != nil {
		return 0, err
	}
	return block.Height, nil
}

func (c *JavaTronClient) SolidifiedBlockByHeight(ctx context.Context, height int64) (TRONSolidifiedBlock, error) {
	if height < 0 {
		return TRONSolidifiedBlock{}, javaTronRequestError(javaTronSolidifiedBlockEndpoint, "block height must be non-negative")
	}
	var response javaTronSolidifiedBlockResponse
	if err := c.postJSON(ctx, JavaTronSolidityNode, javaTronSolidifiedBlockEndpoint, map[string]int64{"num": height}, &response); err != nil {
		return TRONSolidifiedBlock{}, err
	}
	block, err := normalizeTRONSolidifiedBlock(response)
	if err != nil {
		return TRONSolidifiedBlock{}, javaTronInvalidResponseError(javaTronSolidifiedBlockEndpoint, err)
	}
	if block.Height != height {
		return TRONSolidifiedBlock{}, javaTronInvalidResponseError(
			javaTronSolidifiedBlockEndpoint,
			fmt.Errorf("block height is %d, requested %d", block.Height, height),
		)
	}
	return block, nil
}

func (c *JavaTronClient) SolidifiedTransactionReceiptsByBlockHeight(ctx context.Context, height int64) ([]TRONTransactionReceipt, error) {
	if height < 0 {
		return nil, javaTronRequestError(javaTronSolidifiedReceiptsEndpoint, "block height must be non-negative")
	}
	var response []javaTronTransactionInfoResponse
	if err := c.postJSON(ctx, JavaTronSolidityNode, javaTronSolidifiedReceiptsEndpoint, map[string]int64{"num": height}, &response); err != nil {
		return nil, err
	}
	receipts := make([]TRONTransactionReceipt, len(response))
	seenTransactionIDs := make(map[string]struct{}, len(response))
	for index := range response {
		receipt, err := normalizeTRONTransactionReceipt(response[index])
		if err != nil {
			return nil, javaTronInvalidResponseError(
				javaTronSolidifiedReceiptsEndpoint,
				fmt.Errorf("transaction info %d: %w", index, err),
			)
		}
		if receipt.BlockHeight != height {
			return nil, javaTronInvalidResponseError(
				javaTronSolidifiedReceiptsEndpoint,
				fmt.Errorf("transaction %s belongs to block %d, requested %d", receipt.TransactionID, receipt.BlockHeight, height),
			)
		}
		if _, exists := seenTransactionIDs[receipt.TransactionID]; exists {
			return nil, javaTronInvalidResponseError(
				javaTronSolidifiedReceiptsEndpoint,
				fmt.Errorf("duplicate transaction info %s", receipt.TransactionID),
			)
		}
		seenTransactionIDs[receipt.TransactionID] = struct{}{}
		receipts[index] = receipt
	}
	return receipts, nil
}

func (c *JavaTronClient) latestSolidifiedBlock(ctx context.Context) (TRONSolidifiedBlock, error) {
	var response javaTronSolidifiedBlockResponse
	if err := c.postJSON(ctx, JavaTronSolidityNode, javaTronSolidifiedNowBlockEndpoint, struct{}{}, &response); err != nil {
		return TRONSolidifiedBlock{}, err
	}
	block, err := normalizeTRONSolidifiedBlock(response)
	if err != nil {
		return TRONSolidifiedBlock{}, javaTronInvalidResponseError(javaTronSolidifiedNowBlockEndpoint, err)
	}
	return block, nil
}

func normalizeTRONSolidifiedBlock(response javaTronSolidifiedBlockResponse) (TRONSolidifiedBlock, error) {
	if response.BlockHeader.RawData.Number < 0 {
		return TRONSolidifiedBlock{}, fmt.Errorf("block height must be non-negative")
	}
	blockHash, err := normalizeFixedTRONHex("block hash", response.BlockID, 32)
	if err != nil {
		return TRONSolidifiedBlock{}, err
	}
	parentHash := ""
	if response.BlockHeader.RawData.ParentHash != "" {
		parentHash, err = normalizeFixedTRONHex("parent block hash", response.BlockHeader.RawData.ParentHash, 32)
		if err != nil {
			return TRONSolidifiedBlock{}, err
		}
	} else if response.BlockHeader.RawData.Number != 0 {
		return TRONSolidifiedBlock{}, fmt.Errorf("parent block hash is required")
	}
	timestamp, err := normalizeTRONBlockTimestamp(response.BlockHeader.RawData.Timestamp)
	if err != nil {
		return TRONSolidifiedBlock{}, err
	}
	transactionIDs := make([]string, len(response.Transactions))
	seenTransactionIDs := make(map[string]struct{}, len(response.Transactions))
	for index, transaction := range response.Transactions {
		transactionID, err := normalizeFixedTRONHex(fmt.Sprintf("transaction %d ID", index), transaction.TransactionID, 32)
		if err != nil {
			return TRONSolidifiedBlock{}, err
		}
		if _, exists := seenTransactionIDs[transactionID]; exists {
			return TRONSolidifiedBlock{}, fmt.Errorf("duplicate transaction ID %s", transactionID)
		}
		seenTransactionIDs[transactionID] = struct{}{}
		transactionIDs[index] = transactionID
	}
	return TRONSolidifiedBlock{
		Height:         response.BlockHeader.RawData.Number,
		Hash:           blockHash,
		ParentHash:     parentHash,
		Timestamp:      timestamp,
		TransactionIDs: transactionIDs,
	}, nil
}

func normalizeTRONTransactionReceipt(response javaTronTransactionInfoResponse) (TRONTransactionReceipt, error) {
	if response.BlockNumber < 0 {
		return TRONTransactionReceipt{}, fmt.Errorf("block height must be non-negative")
	}
	if response.Fee < 0 || response.TransactionReceipt.EnergyUsageTotal < 0 || response.TransactionReceipt.NetUsage < 0 {
		return TRONTransactionReceipt{}, fmt.Errorf("transaction resource usage must be non-negative")
	}
	transactionID, err := normalizeFixedTRONHex("transaction ID", response.ID, 32)
	if err != nil {
		return TRONTransactionReceipt{}, err
	}
	blockTimestamp, err := normalizeTRONBlockTimestamp(response.BlockTimestamp)
	if err != nil {
		return TRONTransactionReceipt{}, err
	}
	contractResult := make([]string, len(response.ContractResult))
	for index, value := range response.ContractResult {
		contractResult[index], err = normalizeVariableTRONHex(fmt.Sprintf("contract result %d", index), value, true)
		if err != nil {
			return TRONTransactionReceipt{}, err
		}
	}
	logs := make([]TRONTransactionLog, len(response.Logs))
	for index, rawLog := range response.Logs {
		contractAddress, err := normalizeFixedTRONHex(fmt.Sprintf("log %d contract address", index), rawLog.Address, 20)
		if err != nil {
			return TRONTransactionReceipt{}, err
		}
		topics := make([]string, len(rawLog.Topics))
		for topicIndex, topic := range rawLog.Topics {
			topics[topicIndex], err = normalizeFixedTRONHex(fmt.Sprintf("log %d topic %d", index, topicIndex), topic, 32)
			if err != nil {
				return TRONTransactionReceipt{}, err
			}
		}
		data, err := normalizeVariableTRONHex(fmt.Sprintf("log %d data", index), rawLog.Data, true)
		if err != nil {
			return TRONTransactionReceipt{}, err
		}
		logs[index] = TRONTransactionLog{
			Index:           index,
			ContractAddress: contractAddress,
			Topics:          topics,
			Data:            data,
		}
	}
	return TRONTransactionReceipt{
		TransactionID:  transactionID,
		BlockHeight:    response.BlockNumber,
		BlockTimestamp: blockTimestamp,
		Result:         strings.ToUpper(strings.TrimSpace(response.Result)),
		ReceiptResult:  strings.ToUpper(strings.TrimSpace(response.TransactionReceipt.Result)),
		ContractResult: contractResult,
		Logs:           logs,
		FeeSun:         response.Fee,
		EnergyUsed:     response.TransactionReceipt.EnergyUsageTotal,
		BandwidthUsed:  response.TransactionReceipt.NetUsage,
	}, nil
}

func normalizeFixedTRONHex(field, value string, byteLength int) (string, error) {
	normalized, err := normalizeVariableTRONHex(field, value, false)
	if err != nil {
		return "", err
	}
	if len(normalized) != byteLength*2 {
		return "", fmt.Errorf("%s must be %d bytes", field, byteLength)
	}
	return normalized, nil
}

func normalizeVariableTRONHex(field, value string, allowEmpty bool) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("%s is required", field)
	}
	if strings.HasPrefix(normalized, "0x") || strings.HasPrefix(normalized, "0X") {
		return "", fmt.Errorf("%s must not have a 0x prefix", field)
	}
	if len(normalized)%2 != 0 {
		return "", fmt.Errorf("%s must contain an even number of hexadecimal characters", field)
	}
	if _, err := hex.DecodeString(normalized); err != nil {
		return "", fmt.Errorf("%s is not hexadecimal: %w", field, err)
	}
	return strings.ToLower(normalized), nil
}

func normalizeTRONBlockTimestamp(milliseconds int64) (time.Time, error) {
	if milliseconds <= 0 {
		return time.Time{}, fmt.Errorf("block timestamp must be positive")
	}
	timestamp := time.UnixMilli(milliseconds).UTC()
	if timestamp.Year() < 1 || timestamp.Year() > 9999 {
		return time.Time{}, fmt.Errorf("block timestamp is outside the supported range")
	}
	return timestamp, nil
}

func javaTronRequestError(endpoint, message string) error {
	return &JavaTronError{
		Kind:     JavaTronErrorRequestEncode,
		Node:     JavaTronSolidityNode,
		Endpoint: endpoint,
		Attempt:  1,
		Cause:    fmt.Errorf("%s", message),
	}
}

func javaTronInvalidResponseError(endpoint string, cause error) error {
	return &JavaTronError{
		Kind:     JavaTronErrorInvalidResponse,
		Node:     JavaTronSolidityNode,
		Endpoint: endpoint,
		Attempt:  1,
		Cause:    cause,
	}
}
