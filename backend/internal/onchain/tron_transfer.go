package onchain

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
)

const TRC20TransferTopic = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

type TRC20TransferErrorCode string

const (
	TRC20TransferUnsupportedNetwork TRC20TransferErrorCode = "UNSUPPORTED_NETWORK"
	TRC20TransferInvalidConfig      TRC20TransferErrorCode = "INVALID_CONFIG"
	TRC20TransferInvalidReceipt     TRC20TransferErrorCode = "INVALID_RECEIPT"
	TRC20TransferReceiptFailed      TRC20TransferErrorCode = "RECEIPT_FAILED"
	TRC20TransferInvalidLogIndex    TRC20TransferErrorCode = "INVALID_LOG_INDEX"
	TRC20TransferContractMismatch   TRC20TransferErrorCode = "CONTRACT_MISMATCH"
	TRC20TransferTopicMismatch      TRC20TransferErrorCode = "TOPIC_MISMATCH"
	TRC20TransferInvalidAddress     TRC20TransferErrorCode = "INVALID_ADDRESS"
	TRC20TransferRecipientMismatch  TRC20TransferErrorCode = "RECIPIENT_MISMATCH"
	TRC20TransferInvalidAmount      TRC20TransferErrorCode = "INVALID_AMOUNT"
)

type TRC20TransferError struct {
	Code  TRC20TransferErrorCode
	Cause error
}

func (e *TRC20TransferError) Error() string {
	if e.Cause == nil {
		return "TRC20 Transfer rejected: " + string(e.Code)
	}
	return fmt.Sprintf("TRC20 Transfer rejected (%s): %v", e.Code, e.Cause)
}

func (e *TRC20TransferError) Unwrap() error { return e.Cause }

type TRC20TransferParseOptions struct {
	Network          Network
	ContractAddress  string
	RecipientAddress string
}

type TRC20Transfer struct {
	TransactionID   string
	LogIndex        int
	BlockHeight     int64
	BlockTimestamp  time.Time
	ContractAddress string
	FromAddress     string
	ToAddress       string
	AmountRaw       string
}

func ParseTRC20Transfer(receipt TRONTransactionReceipt, logIndex int, options TRC20TransferParseOptions) (TRC20Transfer, error) {
	if options.Network != NetworkTronMainnet && options.Network != NetworkTronNile {
		return TRC20Transfer{}, newTRC20TransferError(TRC20TransferUnsupportedNetwork, "network %q is not TRON", options.Network)
	}
	contractPayload, err := decodeConfiguredTRONAddress(options.Network, "contract", options.ContractAddress)
	if err != nil {
		return TRC20Transfer{}, &TRC20TransferError{Code: TRC20TransferInvalidConfig, Cause: err}
	}
	recipientPayload, err := decodeConfiguredTRONAddress(options.Network, "recipient", options.RecipientAddress)
	if err != nil {
		return TRC20Transfer{}, &TRC20TransferError{Code: TRC20TransferInvalidConfig, Cause: err}
	}
	transactionID, err := normalizeFixedTRONHex("transaction ID", receipt.TransactionID, 32)
	if err != nil || receipt.BlockHeight < 0 || receipt.BlockTimestamp.IsZero() {
		if err == nil {
			err = fmt.Errorf("receipt block identity is incomplete")
		}
		return TRC20Transfer{}, &TRC20TransferError{Code: TRC20TransferInvalidReceipt, Cause: err}
	}
	if receipt.ReceiptResult != "SUCCESS" || (receipt.Result != "" && receipt.Result != "SUCCESS") {
		return TRC20Transfer{}, newTRC20TransferError(
			TRC20TransferReceiptFailed,
			"transaction result %q and receipt result %q are not successful",
			receipt.Result,
			receipt.ReceiptResult,
		)
	}
	if logIndex < 0 || logIndex >= len(receipt.Logs) {
		return TRC20Transfer{}, newTRC20TransferError(TRC20TransferInvalidLogIndex, "log index %d is outside [0,%d)", logIndex, len(receipt.Logs))
	}
	logEntry := receipt.Logs[logIndex]
	if logEntry.Index != logIndex {
		return TRC20Transfer{}, newTRC20TransferError(TRC20TransferInvalidLogIndex, "normalized log index is %d, requested %d", logEntry.Index, logIndex)
	}
	logContract, err := normalizeFixedTRONHex("log contract address", logEntry.ContractAddress, 20)
	if err != nil {
		return TRC20Transfer{}, &TRC20TransferError{Code: TRC20TransferInvalidAddress, Cause: err}
	}
	if !strings.EqualFold(logContract, hex.EncodeToString(contractPayload)) {
		return TRC20Transfer{}, newTRC20TransferError(TRC20TransferContractMismatch, "log contract does not match configured contract")
	}
	if len(logEntry.Topics) != 3 {
		return TRC20Transfer{}, newTRC20TransferError(TRC20TransferTopicMismatch, "Transfer log must contain exactly 3 topics")
	}
	topic0, err := normalizeFixedTRONHex("topic0", logEntry.Topics[0], 32)
	if err != nil || topic0 != TRC20TransferTopic {
		if err == nil {
			err = fmt.Errorf("topic0 is not Transfer(address,address,uint256)")
		}
		return TRC20Transfer{}, &TRC20TransferError{Code: TRC20TransferTopicMismatch, Cause: err}
	}
	fromPayload, err := decodeTRONAddressTopic("from", logEntry.Topics[1])
	if err != nil {
		return TRC20Transfer{}, &TRC20TransferError{Code: TRC20TransferInvalidAddress, Cause: err}
	}
	toPayload, err := decodeTRONAddressTopic("to", logEntry.Topics[2])
	if err != nil {
		return TRC20Transfer{}, &TRC20TransferError{Code: TRC20TransferInvalidAddress, Cause: err}
	}
	if !bytes.Equal(toPayload, recipientPayload) {
		return TRC20Transfer{}, newTRC20TransferError(TRC20TransferRecipientMismatch, "Transfer recipient does not match the registered address")
	}
	amountHex, err := normalizeFixedTRONHex("Transfer amount", logEntry.Data, 32)
	if err != nil {
		return TRC20Transfer{}, &TRC20TransferError{Code: TRC20TransferInvalidAmount, Cause: err}
	}
	amount, ok := new(big.Int).SetString(amountHex, 16)
	if !ok || amount.Sign() <= 0 {
		return TRC20Transfer{}, newTRC20TransferError(TRC20TransferInvalidAmount, "Transfer amount must be a positive uint256")
	}
	return TRC20Transfer{
		TransactionID:   transactionID,
		LogIndex:        logIndex,
		BlockHeight:     receipt.BlockHeight,
		BlockTimestamp:  receipt.BlockTimestamp.UTC(),
		ContractAddress: strings.TrimSpace(options.ContractAddress),
		FromAddress:     base58.CheckEncode(fromPayload, 0x41),
		ToAddress:       base58.CheckEncode(toPayload, 0x41),
		AmountRaw:       amount.String(),
	}, nil
}

func decodeConfiguredTRONAddress(network Network, field, address string) ([]byte, error) {
	if err := ValidateAddress(network, address); err != nil {
		return nil, fmt.Errorf("invalid %s address: %w", field, err)
	}
	payload, version, err := base58.CheckDecode(strings.TrimSpace(address))
	if err != nil || version != 0x41 || len(payload) != 20 {
		return nil, fmt.Errorf("invalid %s TRON Base58Check payload", field)
	}
	return payload, nil
}

func decodeTRONAddressTopic(field, topic string) ([]byte, error) {
	normalized, err := normalizeFixedTRONHex(field+" address topic", topic, 32)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, err
	}
	if !allZero(decoded[:12]) {
		return nil, fmt.Errorf("%s address topic is not a right-aligned 20-byte address", field)
	}
	payload := make([]byte, 20)
	copy(payload, decoded[12:])
	return payload, nil
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

func newTRC20TransferError(code TRC20TransferErrorCode, format string, args ...any) error {
	return &TRC20TransferError{Code: code, Cause: fmt.Errorf(format, args...)}
}
