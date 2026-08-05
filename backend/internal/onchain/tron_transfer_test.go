package onchain

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/stretchr/testify/require"
)

func TestParseTRC20Transfer(t *testing.T) {
	receipt, options, fromAddress := validTRC20TransferFixture(t)

	transfer, err := ParseTRC20Transfer(receipt, 0, options)
	require.NoError(t, err)
	require.Equal(t, receipt.TransactionID, transfer.TransactionID)
	require.Equal(t, 0, transfer.LogIndex)
	require.Equal(t, receipt.BlockHeight, transfer.BlockHeight)
	require.Equal(t, receipt.BlockTimestamp, transfer.BlockTimestamp)
	require.Equal(t, TronMainnetUSDTContract, transfer.ContractAddress)
	require.Equal(t, fromAddress, transfer.FromAddress)
	require.Equal(t, options.RecipientAddress, transfer.ToAddress)
	require.Equal(t, "12500000", transfer.AmountRaw)
}

func TestParseTRC20TransferRejectsInvalidCandidate(t *testing.T) {
	tests := []struct {
		name          string
		expectedCode  TRC20TransferErrorCode
		mutateReceipt func(*TRONTransactionReceipt)
		mutateOptions func(*TRC20TransferParseOptions)
		logIndex      int
	}{
		{
			name:         "unsupported network",
			expectedCode: TRC20TransferUnsupportedNetwork,
			mutateOptions: func(options *TRC20TransferParseOptions) {
				options.Network = NetworkEthereumMainnet
			},
		},
		{
			name:         "invalid configured contract",
			expectedCode: TRC20TransferInvalidConfig,
			mutateOptions: func(options *TRC20TransferParseOptions) {
				options.ContractAddress = "not-an-address"
			},
		},
		{
			name:         "failed receipt",
			expectedCode: TRC20TransferReceiptFailed,
			mutateReceipt: func(receipt *TRONTransactionReceipt) {
				receipt.ReceiptResult = "OUT_OF_ENERGY"
			},
		},
		{
			name:         "failed transaction result",
			expectedCode: TRC20TransferReceiptFailed,
			mutateReceipt: func(receipt *TRONTransactionReceipt) {
				receipt.Result = "FAILED"
			},
		},
		{
			name:         "wrong contract",
			expectedCode: TRC20TransferContractMismatch,
			mutateReceipt: func(receipt *TRONTransactionReceipt) {
				receipt.Logs[0].ContractAddress = strings.Repeat("11", 20)
			},
		},
		{
			name:         "wrong event topic",
			expectedCode: TRC20TransferTopicMismatch,
			mutateReceipt: func(receipt *TRONTransactionReceipt) {
				receipt.Logs[0].Topics[0] = strings.Repeat("ff", 32)
			},
		},
		{
			name:         "noncanonical address topic",
			expectedCode: TRC20TransferInvalidAddress,
			mutateReceipt: func(receipt *TRONTransactionReceipt) {
				receipt.Logs[0].Topics[2] = "01" + receipt.Logs[0].Topics[2][2:]
			},
		},
		{
			name:         "wrong recipient",
			expectedCode: TRC20TransferRecipientMismatch,
			mutateOptions: func(options *TRC20TransferParseOptions) {
				options.RecipientAddress = base58.CheckEncode(bytes.Repeat([]byte{0x44}, 20), 0x41)
			},
		},
		{
			name:         "zero amount",
			expectedCode: TRC20TransferInvalidAmount,
			mutateReceipt: func(receipt *TRONTransactionReceipt) {
				receipt.Logs[0].Data = strings.Repeat("0", 64)
			},
		},
		{
			name:         "non-uint256 amount",
			expectedCode: TRC20TransferInvalidAmount,
			mutateReceipt: func(receipt *TRONTransactionReceipt) {
				receipt.Logs[0].Data = "01"
			},
		},
		{
			name:         "log index out of range",
			expectedCode: TRC20TransferInvalidLogIndex,
			logIndex:     1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt, options, _ := validTRC20TransferFixture(t)
			if test.mutateReceipt != nil {
				test.mutateReceipt(&receipt)
			}
			if test.mutateOptions != nil {
				test.mutateOptions(&options)
			}

			_, err := ParseTRC20Transfer(receipt, test.logIndex, options)
			var transferErr *TRC20TransferError
			require.ErrorAs(t, err, &transferErr)
			require.Equal(t, test.expectedCode, transferErr.Code)
		})
	}
}

func validTRC20TransferFixture(t *testing.T) (TRONTransactionReceipt, TRC20TransferParseOptions, string) {
	t.Helper()
	contractPayload, version, err := base58.CheckDecode(TronMainnetUSDTContract)
	require.NoError(t, err)
	require.Equal(t, byte(0x41), version)
	fromPayload := bytes.Repeat([]byte{0x22}, 20)
	toPayload := bytes.Repeat([]byte{0x33}, 20)
	fromAddress := base58.CheckEncode(fromPayload, 0x41)
	toAddress := base58.CheckEncode(toPayload, 0x41)

	receipt := TRONTransactionReceipt{
		TransactionID:  strings.Repeat("ab", 32),
		BlockHeight:    100,
		BlockTimestamp: time.UnixMilli(1700000000123).UTC(),
		ReceiptResult:  "SUCCESS",
		Logs: []TRONTransactionLog{
			{
				Index:           0,
				ContractAddress: hex.EncodeToString(contractPayload),
				Topics: []string{
					TRC20TransferTopic,
					tronAddressTopic(fromPayload),
					tronAddressTopic(toPayload),
				},
				Data: "0000000000000000000000000000000000000000000000000000000000bebc20",
			},
		},
	}
	options := TRC20TransferParseOptions{
		Network:          NetworkTronMainnet,
		ContractAddress:  TronMainnetUSDTContract,
		RecipientAddress: toAddress,
	}
	return receipt, options, fromAddress
}

func tronAddressTopic(payload []byte) string {
	return strings.Repeat("0", 24) + hex.EncodeToString(payload)
}
