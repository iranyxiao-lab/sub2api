package onchain

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
)

type TRONHealthErrorCode string

const (
	TRONHealthNodeUnavailable  TRONHealthErrorCode = "NODE_UNAVAILABLE"
	TRONHealthNetworkMismatch  TRONHealthErrorCode = "NETWORK_MISMATCH"
	TRONHealthInvalidHeight    TRONHealthErrorCode = "INVALID_HEIGHT"
	TRONHealthScanLag          TRONHealthErrorCode = "SCAN_LAG"
	TRONHealthContractMismatch TRONHealthErrorCode = "CONTRACT_MISMATCH"
	TRONHealthContractCall     TRONHealthErrorCode = "CONTRACT_CALL_FAILED"
	TRONHealthDecimalsMismatch TRONHealthErrorCode = "DECIMALS_MISMATCH"
)

type TRONHealthError struct {
	Code  TRONHealthErrorCode
	Cause error
}

func (e *TRONHealthError) Error() string {
	if e.Cause == nil {
		return "TRON health check failed: " + string(e.Code)
	}
	return fmt.Sprintf("TRON health check failed (%s): %v", e.Code, e.Cause)
}

func (e *TRONHealthError) Unwrap() error { return e.Cause }

type TRONHealthCheckOptions struct {
	Network          Network
	USDTContract     string
	ContractCaller   string
	ExpectedDecimals uint8
	MaxBlockLag      int64
}

type TRONHealthReport struct {
	Healthy          bool
	Network          Network
	P2PVersion       int64
	FullNodeHeight   int64
	FullNodeBlockID  string
	SolidHeight      int64
	SolidBlockID     string
	BlockLag         int64
	ContractDecimals uint8
	CheckedAt        time.Time
}

type javaTronNodeInfoResponse struct {
	Block          string `json:"block"`
	SolidityBlock  string `json:"solidityBlock"`
	ConfigNodeInfo struct {
		P2PVersion int64 `json:"p2pVersion"`
	} `json:"configNodeInfo"`
}

type javaTronBlockResponse struct {
	BlockID     string `json:"blockID"`
	BlockHeader struct {
		RawData struct {
			Number    int64 `json:"number"`
			Timestamp int64 `json:"timestamp"`
		} `json:"raw_data"`
	} `json:"block_header"`
}

type javaTronConstantCallResponse struct {
	Result struct {
		Result  bool   `json:"result"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"result"`
	ConstantResult []string `json:"constant_result"`
}

func (c *JavaTronClient) CheckTRONHealth(ctx context.Context, options TRONHealthCheckOptions) (TRONHealthReport, error) {
	report := TRONHealthReport{Network: options.Network, CheckedAt: time.Now().UTC()}
	expectedP2PVersion, ok := expectedTRONP2PVersion(options.Network)
	if !ok {
		return report, &TRONHealthError{Code: TRONHealthNetworkMismatch, Cause: fmt.Errorf("unsupported TRON network %q", options.Network)}
	}
	if options.MaxBlockLag < 0 {
		return report, &TRONHealthError{Code: TRONHealthInvalidHeight, Cause: fmt.Errorf("maximum block lag must be non-negative")}
	}
	if err := ValidateTokenIdentity(options.Network, 0, options.USDTContract, options.ExpectedDecimals); err != nil {
		return report, &TRONHealthError{Code: TRONHealthContractMismatch, Cause: err}
	}

	var nodeInfo javaTronNodeInfoResponse
	if err := c.postJSON(ctx, JavaTronFullNode, "/wallet/getnodeinfo", struct{}{}, &nodeInfo); err != nil {
		return report, &TRONHealthError{Code: TRONHealthNodeUnavailable, Cause: err}
	}
	report.P2PVersion = nodeInfo.ConfigNodeInfo.P2PVersion
	if report.P2PVersion != expectedP2PVersion {
		return report, &TRONHealthError{
			Code:  TRONHealthNetworkMismatch,
			Cause: fmt.Errorf("node p2p version is %d, expected %d for %s", report.P2PVersion, expectedP2PVersion, options.Network),
		}
	}

	var latest, solid javaTronBlockResponse
	if err := c.postJSON(ctx, JavaTronFullNode, "/wallet/getnowblock", struct{}{}, &latest); err != nil {
		return report, &TRONHealthError{Code: TRONHealthNodeUnavailable, Cause: err}
	}
	if err := c.postJSON(ctx, JavaTronSolidityNode, "/walletsolidity/getnowblock", struct{}{}, &solid); err != nil {
		return report, &TRONHealthError{Code: TRONHealthNodeUnavailable, Cause: err}
	}
	report.FullNodeHeight = latest.BlockHeader.RawData.Number
	report.FullNodeBlockID = latest.BlockID
	report.SolidHeight = solid.BlockHeader.RawData.Number
	report.SolidBlockID = solid.BlockID
	if report.FullNodeHeight < 0 || report.SolidHeight < 0 || report.FullNodeBlockID == "" || report.SolidBlockID == "" || report.SolidHeight > report.FullNodeHeight {
		return report, &TRONHealthError{Code: TRONHealthInvalidHeight, Cause: fmt.Errorf("invalid full/solid block boundary %d/%d", report.FullNodeHeight, report.SolidHeight)}
	}
	report.BlockLag = report.FullNodeHeight - report.SolidHeight
	if report.BlockLag > options.MaxBlockLag {
		return report, &TRONHealthError{Code: TRONHealthScanLag, Cause: fmt.Errorf("solidified block lag %d exceeds %d", report.BlockLag, options.MaxBlockLag)}
	}

	decimals, err := c.tronContractDecimals(ctx, options.USDTContract, options.ContractCaller)
	if err != nil {
		return report, &TRONHealthError{Code: TRONHealthContractCall, Cause: err}
	}
	report.ContractDecimals = decimals
	if decimals != options.ExpectedDecimals {
		return report, &TRONHealthError{
			Code:  TRONHealthDecimalsMismatch,
			Cause: fmt.Errorf("contract decimals are %d, expected %d", decimals, options.ExpectedDecimals),
		}
	}
	report.Healthy = true
	return report, nil
}

func (c *JavaTronClient) tronContractDecimals(ctx context.Context, contractAddress, ownerAddress string) (uint8, error) {
	request := map[string]any{
		"owner_address":     ownerAddress,
		"contract_address":  contractAddress,
		"function_selector": "decimals()",
		"visible":           true,
	}
	var response javaTronConstantCallResponse
	if err := c.postJSON(ctx, JavaTronFullNode, "/wallet/triggerconstantcontract", request, &response); err != nil {
		return 0, err
	}
	if !response.Result.Result || len(response.ConstantResult) != 1 {
		return 0, fmt.Errorf("constant call rejected with code %q", response.Result.Code)
	}
	raw := strings.TrimPrefix(strings.TrimSpace(response.ConstantResult[0]), "0x")
	if raw == "" {
		return 0, fmt.Errorf("constant call returned an empty value")
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return 0, fmt.Errorf("decode constant result: %w", err)
	}
	value, ok := new(big.Int).SetString(raw, 16)
	if !ok || !value.IsUint64() || value.Uint64() > 255 {
		return 0, fmt.Errorf("constant result is not a uint8")
	}
	return uint8(value.Uint64()), nil
}

func expectedTRONP2PVersion(network Network) (int64, bool) {
	switch network {
	case NetworkTronMainnet:
		return TronMainnetP2PVersion, true
	case NetworkTronNile:
		return TronNileP2PVersion, true
	default:
		return 0, false
	}
}
