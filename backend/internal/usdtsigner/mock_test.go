package usdtsigner

import (
	"context"
	"testing"

	signerv1 "github.com/Wei-Shaw/sub2api/internal/signerapi/v1"
	"github.com/stretchr/testify/require"
)

func TestMockSignerIsDeterministicAndIdempotent(t *testing.T) {
	mock := NewMockSigner()
	request := signerv1.SweepTRC20Request{
		TaskID: "sweep-1", DerivationIndex: 7, RawAmount: "50000000", IdempotencyKey: "sweep-1:attempt-1",
	}
	first, err := mock.SweepTRC20(context.Background(), request)
	require.NoError(t, err)
	second, err := mock.SweepTRC20(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, signerv1.APIVersion, first.Version)
	require.Equal(t, string(OperationStatusBroadcast), first.Status)
	require.Len(t, first.TransactionID, 64)
	require.Contains(t, first.AuditID, "mock-")
}

func TestMockSignerRejectsIdempotencyReplayWithDifferentContent(t *testing.T) {
	mock := NewMockSigner()
	request := signerv1.SweepERC20Request{
		TaskID: "sweep-1", DerivationIndex: 7, RawAmount: "50000000", IdempotencyKey: "shared-key",
	}
	_, err := mock.SweepERC20(context.Background(), request)
	require.NoError(t, err)
	request.RawAmount = "50000001"
	_, err = mock.SweepERC20(context.Background(), request)
	require.ErrorIs(t, err, ErrIdempotencyReplay)

	_, err = mock.FundERC20Gas(context.Background(), signerv1.FundERC20GasRequest{
		TaskID: "fund-1", DerivationIndex: 7, RawAmount: "1", IdempotencyKey: "shared-key",
	})
	require.ErrorIs(t, err, ErrIdempotencyReplay)
}

func TestMockSignerHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewMockSigner().FundERC20Gas(ctx, signerv1.FundERC20GasRequest{
		TaskID: "fund-1", RawAmount: "1", IdempotencyKey: "fund-1:attempt-1",
	})
	require.ErrorIs(t, err, context.Canceled)
}
