package signerv1

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOperationRequestsAcceptOnlyCanonicalHighLevelFields(t *testing.T) {
	valid := SweepTRC20Request{
		TaskID:          "sweep-42",
		DerivationIndex: 42,
		RawAmount:       "50000000",
		IdempotencyKey:  "tron:sweep:42",
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		mutate func(*SweepTRC20Request)
	}{
		{name: "empty task", mutate: func(r *SweepTRC20Request) { r.TaskID = "" }},
		{name: "unsafe task", mutate: func(r *SweepTRC20Request) { r.TaskID = "task/../../key" }},
		{name: "hardened index", mutate: func(r *SweepTRC20Request) { r.DerivationIndex = 1 << 31 }},
		{name: "zero amount", mutate: func(r *SweepTRC20Request) { r.RawAmount = "0" }},
		{name: "signed amount", mutate: func(r *SweepTRC20Request) { r.RawAmount = "+1" }},
		{name: "leading zero", mutate: func(r *SweepTRC20Request) { r.RawAmount = "01" }},
		{name: "fractional amount", mutate: func(r *SweepTRC20Request) { r.RawAmount = "1.5" }},
		{name: "uint256 overflow", mutate: func(r *SweepTRC20Request) { r.RawAmount = strings.Repeat("9", 79) }},
		{name: "empty idempotency key", mutate: func(r *SweepTRC20Request) { r.IdempotencyKey = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			require.Error(t, request.Validate())
		})
	}
}

func TestAllOperationTypesUseTheSameRestrictedEnvelope(t *testing.T) {
	require.NoError(t, FundERC20GasRequest{
		TaskID: "gas-1", DerivationIndex: 1, RawAmount: "1000000000000000", IdempotencyKey: "gas:1",
	}.Validate())
	require.NoError(t, SweepERC20Request{
		TaskID: "sweep-1", DerivationIndex: 1, RawAmount: "200000000", IdempotencyKey: "erc20:1",
	}.Validate())
}
