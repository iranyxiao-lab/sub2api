package usdtsigner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
)

type mockResult struct {
	requestDigest string
	response      signerv1.OperationResponse
}

// MockSigner implements the real high-level protocol without key access,
// node calls, signing, or broadcast. Config validation limits its use to
// explicit non-production runtime mode.
type MockSigner struct {
	mu      sync.Mutex
	results map[string]mockResult
}

func NewMockSigner() *MockSigner {
	return &MockSigner{results: make(map[string]mockResult)}
}

func (m *MockSigner) SweepTRC20(ctx context.Context, request signerv1.SweepTRC20Request) (*signerv1.OperationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return m.execute(ctx, OperationSweepTRC20, request.TaskID, request.DerivationIndex, request.RawAmount, request.IdempotencyKey, false)
}

func (m *MockSigner) FundERC20Gas(ctx context.Context, request signerv1.FundERC20GasRequest) (*signerv1.OperationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return m.execute(ctx, OperationFundERC20Gas, request.TaskID, request.DerivationIndex, request.RawAmount, request.IdempotencyKey, true)
}

func (m *MockSigner) SweepERC20(ctx context.Context, request signerv1.SweepERC20Request) (*signerv1.OperationResponse, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return m.execute(ctx, OperationSweepERC20, request.TaskID, request.DerivationIndex, request.RawAmount, request.IdempotencyKey, true)
}

func (m *MockSigner) execute(
	ctx context.Context,
	operation OperationKind,
	taskID string,
	derivationIndex uint32,
	rawAmount string,
	idempotencyKey string,
	ethereum bool,
) (*signerv1.OperationResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	digest, err := signerRequestDigest(operation, taskID, derivationIndex, rawAmount, idempotencyKey)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.results[idempotencyKey]; ok {
		if existing.requestDigest != digest {
			return nil, ErrIdempotencyReplay
		}
		response := existing.response
		return &response, nil
	}
	txDigest := sha256.Sum256([]byte("mock-transaction:" + string(operation) + ":" + digest))
	auditDigest := sha256.Sum256([]byte("mock-audit:" + string(operation) + ":" + digest))
	transactionID := hex.EncodeToString(txDigest[:])
	if ethereum {
		transactionID = "0x" + transactionID
	}
	response := signerv1.OperationResponse{
		Version:        signerv1.APIVersion,
		TaskID:         taskID,
		IdempotencyKey: idempotencyKey,
		Status:         string(OperationStatusBroadcast),
		TransactionID:  transactionID,
		AuditID:        fmt.Sprintf("mock-%s", hex.EncodeToString(auditDigest[:16])),
	}
	m.results[idempotencyKey] = mockResult{requestDigest: digest, response: response}
	return &response, nil
}
