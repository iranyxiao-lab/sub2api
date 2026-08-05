package onchain

import "testing"

func TestProductionDefaults(t *testing.T) {
	t.Parallel()
	policy := DefaultProductionPolicy()
	checks := map[string]string{
		"min recharge":        policy.MinRechargeRaw.String(),
		"max recharge":        policy.MaxRechargeRaw.String(),
		"daily limit":         policy.DailyLimitRaw.String(),
		"TRON sweep":          policy.TronMinSweepRaw.String(),
		"Ethereum sweep":      policy.EthereumMinSweepRaw.String(),
		"Ethereum gas budget": policy.EthereumMaxGasWei.String(),
	}
	want := map[string]string{
		"min recharge": "10000000", "max recharge": "100000000000",
		"daily limit": "0", "TRON sweep": "50000000",
		"Ethereum sweep": "200000000", "Ethereum gas budget": "6000000000000000",
	}
	for name, got := range checks {
		if got != want[name] {
			t.Fatalf("%s = %s, want %s", name, got, want[name])
		}
	}
	if policy.MaxPendingOrders != 3 {
		t.Fatalf("max pending orders = %d, want 3", policy.MaxPendingOrders)
	}
}
