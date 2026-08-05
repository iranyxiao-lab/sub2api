//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestOnchainFoundationMigrationPreservesPaymentOrderColumnsAndRows(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	const schemaName = "onchain_migration_test"

	_, err := tx.ExecContext(ctx, "CREATE SCHEMA "+schemaName)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SET LOCAL search_path TO "+schemaName)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
CREATE TABLE payment_orders (
    id BIGSERIAL PRIMARY KEY,
    sentinel TEXT NOT NULL
);
INSERT INTO payment_orders (sentinel) VALUES ('must-survive');
`)
	require.NoError(t, err)

	columnsBefore := paymentOrderColumnSignature(t, ctx, tx, schemaName)
	rowsBefore := paymentOrderRows(t, ctx, tx)

	migrationSQL, err := migrations.FS.ReadFile("192_add_onchain_usdt_foundation.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err, "migration must be idempotent")

	require.Equal(t, columnsBefore, paymentOrderColumnSignature(t, ctx, tx, schemaName))
	require.Equal(t, rowsBefore, paymentOrderRows(t, ctx, tx))

	var referencedOrderID int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO onchain_payment_intents (
    user_id, network, chain_id, token_contract, deposit_address,
    derivation_index, expected_amount_raw, config_snapshot, config_version,
    payment_order_id
) VALUES (1, 'tron-mainnet', 0, 'token', 'address', 1, 1000000, '{}'::jsonb, 'v1', 1)
RETURNING payment_order_id
`).Scan(&referencedOrderID)
	require.NoError(t, err)
	require.Equal(t, int64(1), referencedOrderID)
}

func paymentOrderColumnSignature(t *testing.T, ctx context.Context, tx *sql.Tx, schemaName string) string {
	t.Helper()
	var signature string
	err := tx.QueryRowContext(ctx, `
SELECT string_agg(format('%s:%s:%s', column_name, data_type, is_nullable), ',' ORDER BY ordinal_position)
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = 'payment_orders'
`, schemaName).Scan(&signature)
	require.NoError(t, err)
	return signature
}

func paymentOrderRows(t *testing.T, ctx context.Context, tx *sql.Tx) string {
	t.Helper()
	var rows string
	err := tx.QueryRowContext(ctx, `
SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.id)::text, '[]')
FROM payment_orders AS p
`).Scan(&rows)
	require.NoError(t, err)
	return rows
}
