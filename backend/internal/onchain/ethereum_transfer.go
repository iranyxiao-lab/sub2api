package onchain

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var ERC20TransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

type ERC20TransferErrorCode string

const (
	ERC20TransferUnsupportedNetwork ERC20TransferErrorCode = "UNSUPPORTED_NETWORK"
	ERC20TransferInvalidConfig      ERC20TransferErrorCode = "INVALID_CONFIG"
	ERC20TransferReceiptFailed      ERC20TransferErrorCode = "RECEIPT_FAILED"
	ERC20TransferContractMismatch   ERC20TransferErrorCode = "CONTRACT_MISMATCH"
	ERC20TransferTopicMismatch      ERC20TransferErrorCode = "TOPIC_MISMATCH"
	ERC20TransferInvalidAddress     ERC20TransferErrorCode = "INVALID_ADDRESS"
	ERC20TransferRecipientMismatch  ERC20TransferErrorCode = "RECIPIENT_MISMATCH"
	ERC20TransferInvalidAmount      ERC20TransferErrorCode = "INVALID_AMOUNT"
	ERC20TransferBlockMismatch      ERC20TransferErrorCode = "BLOCK_MISMATCH"
	ERC20TransferNotFinalized       ERC20TransferErrorCode = "NOT_FINALIZED"
)

type ERC20TransferError struct {
	Code  ERC20TransferErrorCode
	Cause error
}

func (e *ERC20TransferError) Error() string {
	if e.Cause == nil {
		return "ERC20 Transfer rejected: " + string(e.Code)
	}
	return fmt.Sprintf("ERC20 Transfer rejected (%s): %v", e.Code, e.Cause)
}

func (e *ERC20TransferError) Unwrap() error { return e.Cause }

type ERC20TransferParseOptions struct {
	Network          Network
	ContractAddress  string
	RecipientAddress string
	Block            EthereumBlockRef
	FinalizedHead    EthereumBlockRef
}

type ERC20Transfer struct {
	TransactionHash string
	LogIndex        uint
	BlockNumber     uint64
	BlockHash       string
	ContractAddress string
	FromAddress     string
	ToAddress       string
	AmountRaw       string
}

func ParseERC20Transfer(logEntry types.Log, receipt EthereumTransactionReceipt, options ERC20TransferParseOptions) (ERC20Transfer, error) {
	if options.Network != NetworkEthereumMainnet && options.Network != NetworkEthereumSepolia {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferUnsupportedNetwork, "network %q is not Ethereum", options.Network)
	}
	if !common.IsHexAddress(options.ContractAddress) || !common.IsHexAddress(options.RecipientAddress) {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferInvalidConfig, "contract and recipient must be valid Ethereum addresses")
	}
	if logEntry.Removed {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferNotFinalized, "removed logs cannot be credited")
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferReceiptFailed, "transaction receipt status is %d", receipt.Status)
	}
	if !strings.EqualFold(receipt.TransactionHash, logEntry.TxHash.Hex()) ||
		!strings.EqualFold(receipt.BlockHash, logEntry.BlockHash.Hex()) || receipt.BlockNumber != logEntry.BlockNumber {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferBlockMismatch, "receipt and log transaction or block identity differ")
	}
	if options.Block.Number != logEntry.BlockNumber || !strings.EqualFold(options.Block.Hash, logEntry.BlockHash.Hex()) {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferBlockMismatch, "log does not belong to the requested block header")
	}
	if logEntry.BlockNumber > options.FinalizedHead.Number {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferNotFinalized, "log block %d exceeds finalized height %d", logEntry.BlockNumber, options.FinalizedHead.Number)
	}
	if logEntry.BlockNumber == options.FinalizedHead.Number && !strings.EqualFold(logEntry.BlockHash.Hex(), options.FinalizedHead.Hash) {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferNotFinalized, "log hash does not match finalized head hash")
	}
	if logEntry.Address != common.HexToAddress(options.ContractAddress) {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferContractMismatch, "log contract does not match configured USDT contract")
	}
	if len(logEntry.Topics) != 3 || logEntry.Topics[0] != ERC20TransferTopic {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferTopicMismatch, "Transfer log must contain the signature and two indexed addresses")
	}
	from, err := decodeEthereumAddressTopic("from", logEntry.Topics[1])
	if err != nil {
		return ERC20Transfer{}, &ERC20TransferError{Code: ERC20TransferInvalidAddress, Cause: err}
	}
	to, err := decodeEthereumAddressTopic("to", logEntry.Topics[2])
	if err != nil {
		return ERC20Transfer{}, &ERC20TransferError{Code: ERC20TransferInvalidAddress, Cause: err}
	}
	if to != common.HexToAddress(options.RecipientAddress) {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferRecipientMismatch, "Transfer recipient does not match the registered address")
	}
	if len(logEntry.Data) != 32 {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferInvalidAmount, "Transfer value must be exactly 32 bytes")
	}
	amount := new(big.Int).SetBytes(logEntry.Data)
	if amount.Sign() <= 0 {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferInvalidAmount, "Transfer value must be a positive uint256")
	}
	if !receiptContainsEthereumLog(receipt.Logs, logEntry) {
		return ERC20Transfer{}, newERC20TransferError(ERC20TransferBlockMismatch, "receipt does not contain the candidate log")
	}
	return ERC20Transfer{
		TransactionHash: logEntry.TxHash.Hex(), LogIndex: logEntry.Index,
		BlockNumber: logEntry.BlockNumber, BlockHash: logEntry.BlockHash.Hex(),
		ContractAddress: logEntry.Address.Hex(), FromAddress: from.Hex(), ToAddress: to.Hex(),
		AmountRaw: amount.String(),
	}, nil
}

func decodeEthereumAddressTopic(field string, topic common.Hash) (common.Address, error) {
	value := topic.Bytes()
	if !allZero(value[:12]) {
		return common.Address{}, fmt.Errorf("%s address topic is not a right-aligned 20-byte address", field)
	}
	return common.BytesToAddress(value[12:]), nil
}

func receiptContainsEthereumLog(logs []types.Log, candidate types.Log) bool {
	for _, logEntry := range logs {
		if logEntry.TxHash == candidate.TxHash && logEntry.Index == candidate.Index &&
			logEntry.BlockHash == candidate.BlockHash && logEntry.BlockNumber == candidate.BlockNumber &&
			logEntry.Address == candidate.Address && bytes.Equal(logEntry.Data, candidate.Data) &&
			equalEthereumTopics(logEntry.Topics, candidate.Topics) {
			return true
		}
	}
	return false
}

func equalEthereumTopics(left, right []common.Hash) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func newERC20TransferError(code ERC20TransferErrorCode, format string, args ...any) error {
	return &ERC20TransferError{Code: code, Cause: fmt.Errorf(format, args...)}
}
