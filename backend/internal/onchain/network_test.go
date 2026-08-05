package onchain

import "testing"

func TestProductionTokenAllowlist(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		network  Network
		chainID  uint64
		contract string
	}{
		{"tron", NetworkTronMainnet, 0, TronMainnetUSDTContract},
		{"ethereum", NetworkEthereumMainnet, EthereumMainnetChainID, EthereumMainnetUSDTContract},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateTokenIdentity(test.network, test.chainID, test.contract, USDTDecimals); err != nil {
				t.Fatalf("expected allowlisted identity: %v", err)
			}
		})
	}
}

func TestProductionTokenAllowlistRejectsImpostor(t *testing.T) {
	t.Parallel()
	if err := ValidateTokenIdentity(NetworkEthereumMainnet, 1, "0x0000000000000000000000000000000000000001", USDTDecimals); err == nil {
		t.Fatal("expected non-allowlisted contract to be rejected")
	}
}

func TestTestnetRequiresExplicitValidContract(t *testing.T) {
	t.Parallel()
	if err := ValidateTokenIdentity(NetworkEthereumSepolia, EthereumSepoliaChainID, "", USDTDecimals); err == nil {
		t.Fatal("expected empty test contract to be rejected")
	}
	if err := ValidateTokenIdentity(NetworkEthereumSepolia, EthereumSepoliaChainID, "0x0000000000000000000000000000000000000001", USDTDecimals); err != nil {
		t.Fatalf("expected deployment-owned test contract to be accepted: %v", err)
	}
}
