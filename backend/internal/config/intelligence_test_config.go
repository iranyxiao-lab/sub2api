package config

import "fmt"

const DefaultIntelligenceTimeoutSeconds = 300

type IntelligenceTestConfig struct {
	ExecutionTimeoutSeconds int `mapstructure:"execution_timeout_seconds"`
}

func (c IntelligenceTestConfig) Validate() error {
	// Zero preserves defaults for programmatic configuration callers.
	if c.ExecutionTimeoutSeconds != 0 && (c.ExecutionTimeoutSeconds < 30 || c.ExecutionTimeoutSeconds > 1800) {
		return fmt.Errorf("intelligence_test.execution_timeout_seconds must be between 30 and 1800")
	}
	return nil
}

func (c IntelligenceTestConfig) TimeoutSeconds() int {
	if c.ExecutionTimeoutSeconds == 0 {
		return DefaultIntelligenceTimeoutSeconds
	}
	return c.ExecutionTimeoutSeconds
}
