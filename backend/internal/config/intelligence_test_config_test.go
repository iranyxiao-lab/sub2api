package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntelligenceTimeoutConfiguration(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 300, cfg.IntelligenceTest.TimeoutSeconds())
	t.Setenv("INTELLIGENCE_TEST_EXECUTION_TIMEOUT_SECONDS", "600")
	cfg, err = Load()
	require.NoError(t, err)
	require.Equal(t, 600, cfg.IntelligenceTest.TimeoutSeconds())
	for _, value := range []string{"-1", "29", "1801"} {
		t.Setenv("INTELLIGENCE_TEST_EXECUTION_TIMEOUT_SECONDS", value)
		_, err = Load()
		require.ErrorContains(t, err, "intelligence_test.execution_timeout_seconds")
	}
	t.Setenv("INTELLIGENCE_TEST_EXECUTION_TIMEOUT_SECONDS", "0")
	cfg, err = Load()
	require.NoError(t, err)
	require.Equal(t, 300, cfg.IntelligenceTest.TimeoutSeconds())
}
