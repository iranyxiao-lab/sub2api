package onchain

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeEthereumStartupSource struct {
	chainID      uint64
	syncing      bool
	latest       uint64
	finalized    EthereumBlockRef
	code         []byte
	decimals     uint8
	finalizedErr error
	blocks       map[uint64]EthereumBlockRef
}

func (f *fakeEthereumStartupSource) ChainID(context.Context) (uint64, error) { return f.chainID, nil }
func (f *fakeEthereumStartupSource) Syncing(context.Context) (bool, error)   { return f.syncing, nil }
func (f *fakeEthereumStartupSource) LatestBlockNumber(context.Context) (uint64, error) {
	return f.latest, nil
}
func (f *fakeEthereumStartupSource) FinalizedBlock(context.Context) (EthereumBlockRef, error) {
	return f.finalized, f.finalizedErr
}
func (f *fakeEthereumStartupSource) BlockByNumber(_ context.Context, number uint64) (EthereumBlockRef, error) {
	block, ok := f.blocks[number]
	if !ok {
		return EthereumBlockRef{}, errors.New("block not found")
	}
	return block, nil
}
func (f *fakeEthereumStartupSource) ContractCode(context.Context, string, EthereumBlockRef) ([]byte, error) {
	return f.code, nil
}
func (f *fakeEthereumStartupSource) ERC20Decimals(context.Context, string, EthereumBlockRef) (uint8, error) {
	return f.decimals, nil
}

func healthyEthereumStartupSource() *fakeEthereumStartupSource {
	return &fakeEthereumStartupSource{
		chainID: EthereumMainnetChainID, latest: 100, finalized: EthereumBlockRef{Number: 98, Hash: "0xabc"},
		code: []byte{1}, decimals: USDTDecimals, blocks: map[uint64]EthereumBlockRef{},
	}
}

func ethereumStartupTestOptions() EthereumStartupOptions {
	return EthereumStartupOptions{
		Network: NetworkEthereumMainnet, ChainID: EthereumMainnetChainID,
		USDTContract: EthereumMainnetUSDTContract, USDTDecimals: USDTDecimals, MaxFinalizedLag: 8,
	}
}

func TestValidateEthereumStartupAcceptsHealthyIndependentEndpoints(t *testing.T) {
	report, err := ValidateEthereumStartup(context.Background(), healthyEthereumStartupSource(), healthyEthereumStartupSource(), ethereumStartupTestOptions())
	require.NoError(t, err)
	require.Equal(t, uint64(98), report.Primary.Finalized.Number)
	require.Equal(t, uint64(98), report.Backup.Finalized.Number)
	require.Equal(t, uint64(98), report.CommonFinalized.Number)
}

func TestValidateEthereumStartupFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*fakeEthereumStartupSource)
		message string
	}{
		{name: "wrong chain ID", mutate: func(s *fakeEthereumStartupSource) { s.chainID = 2 }, message: "chain ID"},
		{name: "still syncing", mutate: func(s *fakeEthereumStartupSource) { s.syncing = true }, message: "still syncing"},
		{name: "finalized unavailable", mutate: func(s *fakeEthereumStartupSource) { s.finalizedErr = errors.New("unsupported tag") }, message: "finalized block is unavailable"},
		{name: "invalid finalized", mutate: func(s *fakeEthereumStartupSource) { s.finalized.Hash = "" }, message: "invalid finalized"},
		{name: "finalized lag", mutate: func(s *fakeEthereumStartupSource) { s.latest = 200 }, message: "finalized lag"},
		{name: "missing contract", mutate: func(s *fakeEthereumStartupSource) { s.code = nil }, message: "has no code"},
		{name: "wrong decimals", mutate: func(s *fakeEthereumStartupSource) { s.decimals = 18 }, message: "decimals"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := healthyEthereumStartupSource()
			tt.mutate(primary)
			_, err := ValidateEthereumStartup(context.Background(), primary, healthyEthereumStartupSource(), ethereumStartupTestOptions())
			require.ErrorContains(t, err, tt.message)
		})
	}
}

func TestValidateEthereumStartupChecksBackupEndpoint(t *testing.T) {
	backup := healthyEthereumStartupSource()
	backup.syncing = true
	_, err := ValidateEthereumStartup(context.Background(), healthyEthereumStartupSource(), backup, ethereumStartupTestOptions())
	require.ErrorContains(t, err, "backup Ethereum endpoint is still syncing")
}

func TestValidateEthereumStartupComparesCommonFinalizedHash(t *testing.T) {
	primary := healthyEthereumStartupSource()
	primary.finalized = EthereumBlockRef{Number: 99, Hash: "0xdef"}
	primary.blocks[98] = EthereumBlockRef{Number: 98, Hash: "0xabc"}
	backup := healthyEthereumStartupSource()
	report, err := ValidateEthereumStartup(context.Background(), primary, backup, ethereumStartupTestOptions())
	require.NoError(t, err)
	require.Equal(t, EthereumBlockRef{Number: 98, Hash: "0xabc"}, report.CommonFinalized)

	primary.blocks[98] = EthereumBlockRef{Number: 98, Hash: "0x999"}
	_, err = ValidateEthereumStartup(context.Background(), primary, backup, ethereumStartupTestOptions())
	require.ErrorContains(t, err, "finalized hash divergence")
}
