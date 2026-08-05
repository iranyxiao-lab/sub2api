package usdtsigner

import (
	"context"
	"errors"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
)

var ErrOperationUnavailable = errors.New("signer operation is not available until key custody and policy initialization succeeds")

type TRC20Sweeper interface {
	SweepTRC20(context.Context, signerv1.SweepTRC20Request) (*signerv1.OperationResponse, error)
}

type ERC20GasFunder interface {
	FundERC20Gas(context.Context, signerv1.FundERC20GasRequest) (*signerv1.OperationResponse, error)
}

type ERC20Sweeper interface {
	SweepERC20(context.Context, signerv1.SweepERC20Request) (*signerv1.OperationResponse, error)
}

type Operations struct {
	TRC20Sweeper   TRC20Sweeper
	ERC20GasFunder ERC20GasFunder
	ERC20Sweeper   ERC20Sweeper
}

// UnavailableOperations keeps the independently deployable boundary fail-closed
// until the later key custody, policy, idempotency, and broadcast tasks land.
type UnavailableOperations struct{}

func (UnavailableOperations) SweepTRC20(context.Context, signerv1.SweepTRC20Request) (*signerv1.OperationResponse, error) {
	return nil, ErrOperationUnavailable
}

func (UnavailableOperations) FundERC20Gas(context.Context, signerv1.FundERC20GasRequest) (*signerv1.OperationResponse, error) {
	return nil, ErrOperationUnavailable
}

func (UnavailableOperations) SweepERC20(context.Context, signerv1.SweepERC20Request) (*signerv1.OperationResponse, error) {
	return nil, ErrOperationUnavailable
}
