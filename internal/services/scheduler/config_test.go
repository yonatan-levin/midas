package scheduler

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestSchedulerConfigDefaults verifies that scheduler handles default configuration properly
func TestSchedulerConfigDefaults(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name           string
		inputConfig    Config
		expectedConfig Config
	}{
		{
			name: "disabled_by_default",
			inputConfig: Config{
				Enabled: false,
			},
			expectedConfig: Config{
				Enabled:        false,
				Interval:       time.Hour, // default 1 hour
				MaxConcurrency: 2,         // default 2
			},
		},
		{
			name: "enabled_with_custom_values",
			inputConfig: Config{
				Enabled:        true,
				Interval:       24 * time.Hour,
				MaxConcurrency: 5,
			},
			expectedConfig: Config{
				Enabled:        true,
				Interval:       24 * time.Hour,
				MaxConcurrency: 5,
			},
		},
		{
			name: "zero_values_get_defaults",
			inputConfig: Config{
				Enabled:        true,
				Interval:       0, // should get default
				MaxConcurrency: 0, // should get default
			},
			expectedConfig: Config{
				Enabled:        true,
				Interval:       time.Hour, // default 1 hour
				MaxConcurrency: 2,         // default 2
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := New(tt.inputConfig, logger)

			// Access the private config field for testing
			actualConfig := svc.cfg

			if actualConfig.Enabled != tt.expectedConfig.Enabled {
				t.Errorf("Expected Enabled=%v, got %v", tt.expectedConfig.Enabled, actualConfig.Enabled)
			}

			if actualConfig.Interval != tt.expectedConfig.Interval {
				t.Errorf("Expected Interval=%v, got %v", tt.expectedConfig.Interval, actualConfig.Interval)
			}

			if actualConfig.MaxConcurrency != tt.expectedConfig.MaxConcurrency {
				t.Errorf("Expected MaxConcurrency=%v, got %v", tt.expectedConfig.MaxConcurrency, actualConfig.MaxConcurrency)
			}
		})
	}
}
