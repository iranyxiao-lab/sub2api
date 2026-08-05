package signerv1

import (
	"fmt"
	"math/big"
	"regexp"
)

const (
	APIVersion = "v1"

	SweepTRC20Path   = "/internal/usdt-signer/v1/sweep-trc20"
	FundERC20GasPath = "/internal/usdt-signer/v1/fund-erc20-gas"
	SweepERC20Path   = "/internal/usdt-signer/v1/sweep-erc20"
	HealthPath       = "/internal/usdt-signer/v1/healthz"
)

const maxDerivationIndex = uint32(1<<31 - 1)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// SweepTRC20Request is intentionally operation-specific. Network, contract,
// destination, method, calldata, and transaction bytes are signer-owned.
type SweepTRC20Request struct {
	TaskID          string `json:"task_id"`
	DerivationIndex uint32 `json:"derivation_index"`
	RawAmount       string `json:"raw_amount"`
	IdempotencyKey  string `json:"idempotency_key"`
}

func (r SweepTRC20Request) Validate() error {
	return validateRequest(r.TaskID, r.DerivationIndex, r.RawAmount, r.IdempotencyKey)
}

// FundERC20GasRequest carries only the target derivation index and signer-owned
// policy input. It cannot name an address or inject an Ethereum transaction.
type FundERC20GasRequest struct {
	TaskID          string `json:"task_id"`
	DerivationIndex uint32 `json:"derivation_index"`
	RawAmount       string `json:"raw_amount"`
	IdempotencyKey  string `json:"idempotency_key"`
}

func (r FundERC20GasRequest) Validate() error {
	return validateRequest(r.TaskID, r.DerivationIndex, r.RawAmount, r.IdempotencyKey)
}

// SweepERC20Request is isolated from both TRC20 sweeping and ETH gas funding.
type SweepERC20Request struct {
	TaskID          string `json:"task_id"`
	DerivationIndex uint32 `json:"derivation_index"`
	RawAmount       string `json:"raw_amount"`
	IdempotencyKey  string `json:"idempotency_key"`
}

func (r SweepERC20Request) Validate() error {
	return validateRequest(r.TaskID, r.DerivationIndex, r.RawAmount, r.IdempotencyKey)
}

type OperationResponse struct {
	Version                    string `json:"version"`
	TaskID                     string `json:"task_id"`
	IdempotencyKey             string `json:"idempotency_key"`
	Status                     string `json:"status"`
	TransactionID              string `json:"transaction_id,omitempty"`
	ReplacementOfTransactionID string `json:"replacement_of_transaction_id,omitempty"`
	TransactionVersion         int    `json:"transaction_version,omitempty"`
	Nonce                      uint64 `json:"nonce,omitempty"`
	GasLimit                   uint64 `json:"gas_limit,omitempty"`
	MaxFeePerGasWei            string `json:"max_fee_per_gas_wei,omitempty"`
	MaxPriorityFeePerGasWei    string `json:"max_priority_fee_per_gas_wei,omitempty"`
	AuditID                    string `json:"audit_id,omitempty"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func validateRequest(taskID string, derivationIndex uint32, rawAmount, idempotencyKey string) error {
	if !identifierPattern.MatchString(taskID) {
		return fmt.Errorf("task_id must contain 1 to 128 safe identifier characters")
	}
	if derivationIndex > maxDerivationIndex {
		return fmt.Errorf("derivation_index must be a non-hardened child index")
	}
	if !identifierPattern.MatchString(idempotencyKey) {
		return fmt.Errorf("idempotency_key must contain 1 to 128 safe identifier characters")
	}
	if len(rawAmount) == 0 || len(rawAmount) > 78 || rawAmount[0] < '1' || rawAmount[0] > '9' {
		return fmt.Errorf("raw_amount must be a canonical positive base-10 integer")
	}
	for i := 1; i < len(rawAmount); i++ {
		if rawAmount[i] < '0' || rawAmount[i] > '9' {
			return fmt.Errorf("raw_amount must be a canonical positive base-10 integer")
		}
	}
	amount, ok := new(big.Int).SetString(rawAmount, 10)
	if !ok || amount.Sign() <= 0 || amount.BitLen() > 256 {
		return fmt.Errorf("raw_amount must fit in an unsigned 256-bit integer")
	}
	return nil
}
