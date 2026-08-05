package onchain

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

const javaTronSolidifiedTransactionInfoEndpoint = "/walletsolidity/gettransactioninfobyid"

type TRONSolidifiedTransaction struct {
	Found         bool
	Success       bool
	BlockHeight   int64
	BlockHash     string
	FeeSun        int64
	EnergyUsed    int64
	BandwidthUsed int64
	Receipt       TRONTransactionReceipt
}

func (c *JavaTronClient) SolidifiedTRONTransaction(ctx context.Context, transactionID string) (TRONSolidifiedTransaction, error) {
	normalizedID, err := normalizeFixedTRONHex("transaction ID", transactionID, 32)
	if err != nil {
		return TRONSolidifiedTransaction{}, javaTronRequestError(javaTronSolidifiedTransactionInfoEndpoint, err.Error())
	}
	var response javaTronTransactionInfoResponse
	if err := c.postJSON(ctx, JavaTronSolidityNode, javaTronSolidifiedTransactionInfoEndpoint, map[string]string{"value": normalizedID}, &response); err != nil {
		return TRONSolidifiedTransaction{}, err
	}
	if strings.TrimSpace(response.ID) == "" {
		return TRONSolidifiedTransaction{}, nil
	}
	receipt, err := normalizeTRONTransactionReceipt(response)
	if err != nil {
		return TRONSolidifiedTransaction{}, javaTronInvalidResponseError(javaTronSolidifiedTransactionInfoEndpoint, err)
	}
	if receipt.TransactionID != normalizedID {
		return TRONSolidifiedTransaction{}, javaTronInvalidResponseError(
			javaTronSolidifiedTransactionInfoEndpoint,
			fmt.Errorf("returned transaction ID %s does not match %s", receipt.TransactionID, normalizedID),
		)
	}
	block, err := c.SolidifiedBlockByHeight(ctx, receipt.BlockHeight)
	if err != nil {
		return TRONSolidifiedTransaction{}, fmt.Errorf("verify solidified TRON transaction block: %w", err)
	}
	if !slices.Contains(block.TransactionIDs, normalizedID) || !block.Timestamp.Equal(receipt.BlockTimestamp) {
		return TRONSolidifiedTransaction{}, javaTronInvalidResponseError(
			javaTronSolidifiedTransactionInfoEndpoint,
			fmt.Errorf("transaction receipt does not belong to its solidified block"),
		)
	}
	success := receipt.Result == "SUCCESS" && receipt.ReceiptResult == "SUCCESS"
	return TRONSolidifiedTransaction{
		Found: true, Success: success, BlockHeight: receipt.BlockHeight, BlockHash: block.Hash,
		FeeSun: receipt.FeeSun, EnergyUsed: receipt.EnergyUsed, BandwidthUsed: receipt.BandwidthUsed,
		Receipt: receipt,
	}, nil
}
