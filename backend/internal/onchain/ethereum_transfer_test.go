package onchain

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func TestParseERC20TransferValidatesReceiptAndFinalizedMembership(t *testing.T) {
	logEntry, receipt, options := validERC20TransferFixture()

	transfer, err := ParseERC20Transfer(logEntry, receipt, options)
	require.NoError(t, err)
	require.Equal(t, logEntry.TxHash.Hex(), transfer.TransactionHash)
	require.Equal(t, uint(7), transfer.LogIndex)
	require.Equal(t, "12500000", transfer.AmountRaw)
	require.Equal(t, common.HexToAddress(options.RecipientAddress).Hex(), transfer.ToAddress)
}

func TestParseERC20TransferRejectsUntrustedCandidates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.Log, *EthereumTransactionReceipt, *ERC20TransferParseOptions)
		code   ERC20TransferErrorCode
	}{
		{name: "failed receipt", mutate: func(_ *types.Log, r *EthereumTransactionReceipt, _ *ERC20TransferParseOptions) { r.Status = 0 }, code: ERC20TransferReceiptFailed},
		{name: "fake contract", mutate: func(l *types.Log, _ *EthereumTransactionReceipt, _ *ERC20TransferParseOptions) {
			l.Address = common.HexToAddress("0x0000000000000000000000000000000000000009")
		}, code: ERC20TransferContractMismatch},
		{name: "wrong signature", mutate: func(l *types.Log, _ *EthereumTransactionReceipt, _ *ERC20TransferParseOptions) {
			l.Topics[0] = common.HexToHash("0x01")
		}, code: ERC20TransferTopicMismatch},
		{name: "malformed address topic", mutate: func(l *types.Log, _ *EthereumTransactionReceipt, _ *ERC20TransferParseOptions) { l.Topics[2][0] = 1 }, code: ERC20TransferInvalidAddress},
		{name: "wrong recipient", mutate: func(_ *types.Log, _ *EthereumTransactionReceipt, o *ERC20TransferParseOptions) {
			o.RecipientAddress = "0x0000000000000000000000000000000000000008"
		}, code: ERC20TransferRecipientMismatch},
		{name: "zero value", mutate: func(l *types.Log, _ *EthereumTransactionReceipt, _ *ERC20TransferParseOptions) {
			l.Data = make([]byte, 32)
		}, code: ERC20TransferInvalidAmount},
		{name: "block header mismatch", mutate: func(_ *types.Log, _ *EthereumTransactionReceipt, o *ERC20TransferParseOptions) {
			o.Block.Hash = common.HexToHash("0x99").Hex()
		}, code: ERC20TransferBlockMismatch},
		{name: "above finalized", mutate: func(l *types.Log, r *EthereumTransactionReceipt, o *ERC20TransferParseOptions) {
			l.BlockNumber = 101
			r.BlockNumber = 101
			o.Block.Number = 101
		}, code: ERC20TransferNotFinalized},
		{name: "missing from receipt", mutate: func(_ *types.Log, r *EthereumTransactionReceipt, _ *ERC20TransferParseOptions) { r.Logs = nil }, code: ERC20TransferBlockMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logEntry, receipt, options := validERC20TransferFixture()
			test.mutate(&logEntry, &receipt, &options)
			_, err := ParseERC20Transfer(logEntry, receipt, options)
			var transferErr *ERC20TransferError
			require.ErrorAs(t, err, &transferErr)
			require.Equal(t, test.code, transferErr.Code)
		})
	}
}

func validERC20TransferFixture() (types.Log, EthereumTransactionReceipt, ERC20TransferParseOptions) {
	transactionHash := common.HexToHash("0x22")
	blockHash := common.HexToHash("0x11")
	contract := common.HexToAddress(EthereumMainnetUSDTContract)
	from := common.HexToAddress("0x0000000000000000000000000000000000000002")
	to := common.HexToAddress("0x0000000000000000000000000000000000000003")
	amount := new(big.Int).SetInt64(12_500_000).FillBytes(make([]byte, 32))
	logEntry := types.Log{
		Address: contract, Topics: []common.Hash{ERC20TransferTopic, common.BytesToHash(from.Bytes()), common.BytesToHash(to.Bytes())},
		Data: amount, BlockNumber: 99, TxHash: transactionHash, TxIndex: 1, BlockHash: blockHash, Index: 7,
	}
	receipt := EthereumTransactionReceipt{
		TransactionHash: transactionHash.Hex(), BlockHash: blockHash.Hex(), BlockNumber: 99,
		Status: types.ReceiptStatusSuccessful, Logs: []types.Log{logEntry},
	}
	options := ERC20TransferParseOptions{
		Network: NetworkEthereumMainnet, ContractAddress: contract.Hex(), RecipientAddress: to.Hex(),
		Block:         EthereumBlockRef{Number: 99, Hash: blockHash.Hex()},
		FinalizedHead: EthereumBlockRef{Number: 100, Hash: common.HexToHash("0x44").Hex()},
	}
	return logEntry, receipt, options
}
