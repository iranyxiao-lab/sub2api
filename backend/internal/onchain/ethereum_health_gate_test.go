package onchain

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEthereumHealthGateFailsClosedBeforeFirstCheck(t *testing.T) {
	gate := NewEthereumHealthGate()
	require.False(t, gate.NewOrdersAllowed())
	require.True(t, gate.ExistingAddressProcessingAllowed())
	var gateErr *EthereumOrderGateError
	require.ErrorAs(t, gate.RequireNewOrder(), &gateErr)
	require.Equal(t, EthereumRechargeUnavailable, gateErr.Code)
}

func TestEthereumHealthGateRefreshAndDivergence(t *testing.T) {
	gate := NewEthereumHealthGate()
	_, err := gate.Refresh(context.Background(), healthyEthereumStartupSource(), healthyEthereumStartupSource(), ethereumStartupTestOptions())
	require.NoError(t, err)
	require.True(t, gate.NewOrdersAllowed())
	require.True(t, gate.ExistingAddressProcessingAllowed())

	gate.Record(EthereumStartupReport{}, fmt.Errorf("%w at height 10", ErrEthereumFinalizedDivergence))
	require.False(t, gate.NewOrdersAllowed())
	require.False(t, gate.ExistingAddressProcessingAllowed())
	require.ErrorIs(t, gate.RequireNewOrder(), ErrEthereumFinalizedDivergence)

	gate.Record(EthereumStartupReport{}, errors.New("primary unavailable"))
	require.False(t, gate.NewOrdersAllowed())
	require.True(t, gate.ExistingAddressProcessingAllowed(), "a healthy backup may continue existing-address recovery")
}
