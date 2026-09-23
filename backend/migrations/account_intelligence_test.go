package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountIntelligenceMigrationPreservesConnectivityPlans(t *testing.T) {
	content, err := FS.ReadFile("241_account_intelligence_tests.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "test_kind VARCHAR(20) NOT NULL DEFAULT 'connectivity'")
	require.Contains(t, sql, "WHERE test_kind = 'intelligence'")
	require.Contains(t, sql, "question_snapshot JSONB")
	require.Contains(t, sql, "score INT")
}
