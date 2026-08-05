package onchain

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcutil/base58"
)

type TRONAccountState struct {
	TRXBalanceSun int64
	EnergyLimit   int64
	EnergyUsed    int64
	FreeNetLimit  int64
	FreeNetUsed   int64
	NetLimit      int64
	NetUsed       int64
}

func (s TRONAccountState) AvailableEnergy() int64 {
	return nonNegativeDifference(s.EnergyLimit, s.EnergyUsed)
}

func (s TRONAccountState) AvailableBandwidth() int64 {
	free := nonNegativeDifference(s.FreeNetLimit, s.FreeNetUsed)
	staked := nonNegativeDifference(s.NetLimit, s.NetUsed)
	if free > math.MaxInt64-staked {
		return math.MaxInt64
	}
	return free + staked
}

func (c *JavaTronClient) TRONAccountState(ctx context.Context, address string) (TRONAccountState, error) {
	if err := ValidateAddress(NetworkTronMainnet, address); err != nil {
		return TRONAccountState{}, fmt.Errorf("invalid TRON account address: %w", err)
	}
	request := map[string]any{"address": strings.TrimSpace(address), "visible": true}
	var account struct {
		Balance int64 `json:"balance"`
	}
	if err := c.postJSON(ctx, JavaTronFullNode, "/wallet/getaccount", request, &account); err != nil {
		return TRONAccountState{}, err
	}
	var resources struct {
		EnergyLimit  int64 `json:"EnergyLimit"`
		EnergyUsed   int64 `json:"EnergyUsed"`
		FreeNetLimit int64 `json:"freeNetLimit"`
		FreeNetUsed  int64 `json:"freeNetUsed"`
		NetLimit     int64 `json:"NetLimit"`
		NetUsed      int64 `json:"NetUsed"`
	}
	if err := c.postJSON(ctx, JavaTronFullNode, "/wallet/getaccountresource", request, &resources); err != nil {
		return TRONAccountState{}, err
	}
	values := []int64{
		account.Balance, resources.EnergyLimit, resources.EnergyUsed,
		resources.FreeNetLimit, resources.FreeNetUsed, resources.NetLimit, resources.NetUsed,
	}
	for _, value := range values {
		if value < 0 {
			return TRONAccountState{}, &JavaTronError{
				Kind: JavaTronErrorInvalidResponse, Node: JavaTronFullNode,
				Endpoint: "/wallet/getaccountresource", Attempt: 1,
				Cause: fmt.Errorf("account resource values must be non-negative"),
			}
		}
	}
	return TRONAccountState{
		TRXBalanceSun: account.Balance, EnergyLimit: resources.EnergyLimit, EnergyUsed: resources.EnergyUsed,
		FreeNetLimit: resources.FreeNetLimit, FreeNetUsed: resources.FreeNetUsed,
		NetLimit: resources.NetLimit, NetUsed: resources.NetUsed,
	}, nil
}

func (c *JavaTronClient) TRC20Balance(ctx context.Context, contract, address string) (*big.Int, error) {
	return c.trc20Balance(ctx, JavaTronFullNode, "/wallet/triggerconstantcontract", contract, address)
}

func (c *JavaTronClient) SolidifiedTRC20Balance(ctx context.Context, contract, address string) (*big.Int, error) {
	return c.trc20Balance(ctx, JavaTronSolidityNode, "/walletsolidity/triggerconstantcontract", contract, address)
}

func (c *JavaTronClient) trc20Balance(ctx context.Context, node JavaTronNodeRole, endpoint, contract, address string) (*big.Int, error) {
	if err := ValidateAddress(NetworkTronMainnet, contract); err != nil {
		return nil, fmt.Errorf("invalid TRC20 contract address: %w", err)
	}
	parameter, err := tronABIAddressParameter(address)
	if err != nil {
		return nil, err
	}
	request := map[string]any{
		"owner_address": strings.TrimSpace(address), "contract_address": strings.TrimSpace(contract),
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
	if err := c.postJSON(ctx, node, endpoint, request, &response); err != nil {
		return nil, err
	}
	if !response.Result.Result || len(response.ConstantResult) != 1 {
		return nil, &JavaTronError{
			Kind: JavaTronErrorInvalidResponse, Node: node,
			Endpoint: endpoint, Attempt: 1,
			Cause: fmt.Errorf("balanceOf rejected with code %q", response.Result.Code),
		}
	}
	value, ok := new(big.Int).SetString(strings.TrimSpace(response.ConstantResult[0]), 16)
	if !ok || value.Sign() < 0 || value.BitLen() > 256 {
		return nil, &JavaTronError{
			Kind: JavaTronErrorInvalidResponse, Node: node,
			Endpoint: endpoint, Attempt: 1,
			Cause: fmt.Errorf("balanceOf returned an invalid uint256"),
		}
	}
	return value, nil
}

func tronABIAddressParameter(address string) (string, error) {
	payload, version, err := base58.CheckDecode(strings.TrimSpace(address))
	if err != nil || version != 0x41 || len(payload) != 20 {
		return "", fmt.Errorf("invalid TRON address parameter")
	}
	return hex.EncodeToString(append(make([]byte, 12), payload...)), nil
}

func nonNegativeDifference(limit, used int64) int64 {
	if used >= limit {
		return 0
	}
	return limit - used
}
