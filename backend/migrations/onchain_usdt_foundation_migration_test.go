package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration192CreatesOnchainFoundationWithoutMutatingPaymentOrders(t *testing.T) {
	content, err := FS.ReadFile("192_add_onchain_usdt_foundation.sql")
	require.NoError(t, err)

	sql := string(content)
	for _, table := range []string{
		"onchain_payment_intents",
		"onchain_deposits",
		"chain_scan_cursors",
		"wallet_derivation_cursors",
		"wallet_sweeps",
		"ethereum_gas_fundings",
		"ethereum_nonce_states",
	} {
		require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS "+table)
	}

	normalized := strings.ToUpper(sql)
	require.NotContains(t, normalized, "ALTER TABLE PAYMENT_ORDERS")
	require.NotContains(t, normalized, "UPDATE PAYMENT_ORDERS")
	require.NotContains(t, normalized, "DELETE FROM PAYMENT_ORDERS")
	require.Contains(t, sql, "FOREIGN KEY (payment_order_id) REFERENCES payment_orders (id)")
	require.Contains(t, sql, "NUMERIC(78,0)")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS onchaindeposit_network_transaction_id_log_index")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS walletsweep_network_transaction_id")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS ethereumgasfunding_transaction_hash")
}
