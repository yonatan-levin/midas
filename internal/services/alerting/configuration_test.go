package alerting

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigurationLoader_LoadAlertRules tests loading alert rules from YAML configuration
func TestConfigurationLoader_LoadAlertRules(t *testing.T) {
	loader := NewConfigurationLoader()
	ctx := context.Background()

	tests := []struct {
		name          string
		configContent string
		expectedRules int
		expectError   bool
		description   string
	}{
		{
			name: "valid_alert_rules_config",
			configContent: `
alert_rules:
  - id: "latency_high"
    name: "High Latency Alert"
    description: "Alerts when average latency exceeds 500ms"
    severity: "critical"
    enabled: true
    channels: ["email-ops", "slack-alerts"]
    conditions:
      latency_threshold:
        operator: "gt"
        value: 500
        duration: "5m"
        consecutive: 3
      regression_detection:
        method: "statistical"
        threshold: 0.20
        confidence_level: 0.95
        statistical_test: "t-test"
        min_sample_size: 5
    escalation_policy:
      id: "standard_escalation"
      name: "Standard Escalation"
      max_escalations: 3
      steps:
        - level: 1
          delay: "15m"
          channels: ["email-ops"]
        - level: 2
          delay: "30m"
          channels: ["slack-alerts", "pagerduty-oncall"]
    suppression_windows:
      - start_time: "02:00"
        end_time: "06:00"
        days: ["saturday", "sunday"]
        timezone: "UTC"

  - id: "throughput_low"
    name: "Low Throughput Alert"
    description: "Alerts when throughput drops below 10 RPS"
    severity: "warning"
    enabled: true
    channels: ["slack-alerts"]
    conditions:
      throughput_threshold:
        operator: "lt"
        value: 10
        duration: "10m"
        consecutive: 2
`,
			expectedRules: 2,
			expectError:   false,
			description:   "Should load valid alert rules configuration",
		},
		{
			name: "invalid_yaml_syntax",
			configContent: `
alert_rules:
  - id: "invalid
    name: "Missing quote
`,
			expectedRules: 0,
			expectError:   true,
			description:   "Should reject invalid YAML syntax",
		},
		{
			name: "missing_required_fields",
			configContent: `
alert_rules:
  - name: "Alert without ID"
    severity: "warning"
`,
			expectedRules: 0,
			expectError:   true,
			description:   "Should reject alert rules missing required fields",
		},
		{
			name: "invalid_severity",
			configContent: `
alert_rules:
  - id: "test_alert"
    name: "Test Alert"
    severity: "invalid_severity"
    enabled: true
    channels: ["test"]
    conditions:
      latency_threshold:
        operator: "gt"
        value: 100
`,
			expectedRules: 0,
			expectError:   true,
			description:   "Should reject invalid severity levels",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "alert_rules.yaml")
			err := os.WriteFile(configPath, []byte(tt.configContent), 0644)
			require.NoError(t, err, "Should create temp config file")

			// Load alert rules
			rules, err := loader.LoadAlertRules(ctx, configPath)

			if tt.expectError {
				assert.Error(t, err, tt.description)
				assert.Empty(t, rules, "Should return empty rules on error")
			} else {
				assert.NoError(t, err, tt.description)
				assert.Len(t, rules, tt.expectedRules, "Should load expected number of rules")

				// Validate first rule in detail for valid config
				if len(rules) > 0 {
					rule := rules[0]
					assert.NotEmpty(t, rule.ID, "Rule should have ID")
					assert.NotEmpty(t, rule.Name, "Rule should have name")
					assert.True(t, rule.Enabled, "Rule should be enabled")
					assert.NotEmpty(t, rule.Channels, "Rule should have notification channels")
					assert.NotNil(t, rule.Conditions, "Rule should have conditions")
				}
			}
		})
	}
}

// TestConfigurationLoader_LoadNotificationChannels tests loading notification channels from YAML
func TestConfigurationLoader_LoadNotificationChannels(t *testing.T) {
	loader := NewConfigurationLoader()
	ctx := context.Background()

	tests := []struct {
		name             string
		configContent    string
		expectedChannels int
		expectError      bool
		description      string
	}{
		{
			name: "valid_notification_channels",
			configContent: `
notification_channels:
  - id: "email-ops"
    name: "Operations Email"
    type: "email"
    enabled: true
    config:
      smtp_server: "smtp.company.com"
      smtp_port: 587
      from_email: "alerts@company.com"
      to_emails: ["ops@company.com", "devops@company.com"]
      username: "alerts@company.com"
      password: "${EMAIL_PASSWORD}"
      use_tls: true

  - id: "slack-alerts"
    name: "Slack Alerts Channel"
    type: "slack"
    enabled: true
    config:
      webhook_url: "${SLACK_WEBHOOK_URL}"
      channel: "#alerts"
      username: "AlertBot"
      icon_emoji: ":warning:"

  - id: "pagerduty-oncall"
    name: "PagerDuty On-Call"
    type: "pagerduty"
    enabled: true
    config:
      integration_key: "${PAGERDUTY_INTEGRATION_KEY}"
      severity_mapping:
        critical: "critical"
        warning: "warning"
        info: "info"

  - id: "webhook-monitoring"
    name: "Monitoring Webhook"
    type: "webhook"
    enabled: false
    config:
      url: "https://monitoring.company.com/webhook"
      method: "POST"
      headers:
        Authorization: "Bearer ${WEBHOOK_TOKEN}"
        Content-Type: "application/json"
      timeout: "30s"
`,
			expectedChannels: 4,
			expectError:      false,
			description:      "Should load valid notification channels",
		},
		{
			name: "invalid_channel_type",
			configContent: `
notification_channels:
  - id: "invalid-channel"
    name: "Invalid Channel"
    type: "unsupported_type"
    enabled: true
    config: {}
`,
			expectedChannels: 0,
			expectError:      true,
			description:      "Should reject unsupported channel types",
		},
		{
			name: "missing_channel_config",
			configContent: `
notification_channels:
  - id: "incomplete-email"
    name: "Incomplete Email Channel"
    type: "email"
    enabled: true
`,
			expectedChannels: 0,
			expectError:      true,
			description:      "Should reject channels missing required config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "notification_channels.yaml")
			err := os.WriteFile(configPath, []byte(tt.configContent), 0644)
			require.NoError(t, err, "Should create temp config file")

			// Load notification channels
			channels, err := loader.LoadNotificationChannels(ctx, configPath)

			if tt.expectError {
				assert.Error(t, err, tt.description)
				assert.Empty(t, channels, "Should return empty channels on error")
			} else {
				assert.NoError(t, err, tt.description)
				assert.Len(t, channels, tt.expectedChannels, "Should load expected number of channels")

				// Validate channels
				for _, channel := range channels {
					assert.NotEmpty(t, channel.ID, "Channel should have ID")
					assert.NotEmpty(t, channel.Name, "Channel should have name")
					assert.NotEmpty(t, channel.Type, "Channel should have type")
					assert.NotNil(t, channel.Config, "Channel should have config")
				}
			}
		})
	}
}

// TestConfigurationLoader_LoadEscalationPolicies tests loading escalation policies from YAML
func TestConfigurationLoader_LoadEscalationPolicies(t *testing.T) {
	loader := NewConfigurationLoader()
	ctx := context.Background()

	configContent := `
escalation_policies:
  - id: "standard_escalation"
    name: "Standard Escalation Policy"
    max_escalations: 3
    steps:
      - level: 1
        delay: "15m"
        channels: ["email-ops"]
      - level: 2
        delay: "30m"
        channels: ["slack-alerts", "email-management"]
      - level: 3
        delay: "1h"
        channels: ["pagerduty-oncall"]

  - id: "critical_escalation"
    name: "Critical Issues Escalation"
    max_escalations: 2
    steps:
      - level: 1
        delay: "5m"
        channels: ["slack-alerts", "pagerduty-oncall"]
      - level: 2
        delay: "15m"
        channels: ["email-executives"]
`

	// Create temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "escalation_policies.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err, "Should create temp config file")

	// Load escalation policies
	policies, err := loader.LoadEscalationPolicies(ctx, configPath)
	require.NoError(t, err, "Should load escalation policies successfully")
	assert.Len(t, policies, 2, "Should load 2 escalation policies")

	// Validate first policy
	policy := policies[0]
	assert.Equal(t, "standard_escalation", policy.ID)
	assert.Equal(t, "Standard Escalation Policy", policy.Name)
	assert.Equal(t, 3, policy.MaxEscalations)
	assert.Len(t, policy.Steps, 3, "Should have 3 escalation steps")

	// Validate escalation steps
	step1 := policy.Steps[0]
	assert.Equal(t, 1, step1.Level)
	assert.Equal(t, "15m", step1.Delay)
	assert.Contains(t, step1.Channels, "email-ops")

	step3 := policy.Steps[2]
	assert.Equal(t, 3, step3.Level)
	assert.Equal(t, "1h", step3.Delay)
	assert.Contains(t, step3.Channels, "pagerduty-oncall")
}

// TestConfigurationLoader_ValidateConfiguration tests configuration validation
func TestConfigurationLoader_ValidateConfiguration(t *testing.T) {
	loader := NewConfigurationLoader()
	ctx := context.Background()

	tests := []struct {
		name          string
		configContent string
		expectError   bool
		description   string
	}{
		{
			name: "valid_complete_configuration",
			configContent: `
alert_rules:
  - id: "test_alert"
    name: "Test Alert"
    severity: "warning"
    enabled: true
    channels: ["email-test"]
    conditions:
      latency_threshold:
        operator: "gt"
        value: 100
        duration: "5m"

notification_channels:
  - id: "email-test"
    name: "Test Email"
    type: "email"
    enabled: true
    config:
      smtp_server: "localhost"
      from_email: "test@example.com"
      to_emails: ["admin@example.com"]
`,
			expectError: false,
			description: "Should validate complete valid configuration",
		},
		{
			name: "invalid_operator",
			configContent: `
alert_rules:
  - id: "test_alert"
    name: "Test Alert"
    severity: "warning"
    enabled: true
    channels: ["email-test"]
    conditions:
      latency_threshold:
        operator: "invalid_operator"
        value: 100
`,
			expectError: true,
			description: "Should reject invalid threshold operators",
		},
		{
			name: "negative_threshold_value",
			configContent: `
alert_rules:
  - id: "test_alert"
    name: "Test Alert"
    severity: "warning"
    enabled: true
    channels: ["email-test"]
    conditions:
      latency_threshold:
        operator: "gt"
        value: -100
`,
			expectError: true,
			description: "Should reject negative threshold values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "config.yaml")
			err := os.WriteFile(configPath, []byte(tt.configContent), 0644)
			require.NoError(t, err, "Should create temp config file")

			// Validate configuration
			err = loader.ValidateConfiguration(ctx, configPath)

			if tt.expectError {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

// TestConfigurationLoader_WatchConfiguration tests configuration file watching
func TestConfigurationLoader_WatchConfiguration(t *testing.T) {
	loader := NewConfigurationLoader()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "watch_test.yaml")
	initialContent := `
alert_rules:
  - id: "test_alert"
    name: "Initial Alert"
    severity: "info"
    enabled: true
    channels: ["test"]
    conditions:
      latency_threshold:
        operator: "gt"
        value: 100
`
	err := os.WriteFile(configPath, []byte(initialContent), 0644)
	require.NoError(t, err, "Should create initial config file")

	// Start watching configuration
	changes, err := loader.WatchConfiguration(ctx, configPath)
	require.NoError(t, err, "Should start watching configuration")

	// Modify the configuration file
	go func() {
		time.Sleep(100 * time.Millisecond)
		updatedContent := `
alert_rules:
  - id: "test_alert"
    name: "Updated Alert"
    severity: "warning"
    enabled: true
    channels: ["test"]
    conditions:
      latency_threshold:
        operator: "gt"
        value: 200
`
		err := os.WriteFile(configPath, []byte(updatedContent), 0644)
		require.NoError(t, err, "Should update config file")
	}()

	// Wait for change notification
	select {
	case change := <-changes:
		assert.Equal(t, "updated", change.Type, "Should detect file update")
		assert.Equal(t, configPath, change.Path, "Should report correct file path")
		assert.WithinDuration(t, time.Now(), change.Timestamp, 5*time.Second, "Should have recent timestamp")
	case <-ctx.Done():
		t.Fatal("Should detect configuration change within timeout")
	}
}

// TestConfigurationLoader_EnvironmentVariableSubstitution tests environment variable substitution in configs
func TestConfigurationLoader_EnvironmentVariableSubstitution(t *testing.T) {
	loader := NewConfigurationLoader()
	ctx := context.Background()

	// Set test environment variables
	os.Setenv("TEST_EMAIL_PASSWORD", "secret123")
	os.Setenv("TEST_SLACK_WEBHOOK", "https://hooks.slack.com/services/test")
	defer func() {
		os.Unsetenv("TEST_EMAIL_PASSWORD")
		os.Unsetenv("TEST_SLACK_WEBHOOK")
	}()

	configContent := `
notification_channels:
  - id: "email-test"
    name: "Test Email"
    type: "email"
    enabled: true
    config:
      smtp_server: "smtp.test.com"
      from_email: "alerts@test.com"
      to_emails: ["admin@test.com"]
      password: "${TEST_EMAIL_PASSWORD}"

  - id: "slack-test"
    name: "Test Slack"
    type: "slack"
    enabled: true
    config:
      webhook_url: "${TEST_SLACK_WEBHOOK}"
      channel: "#test"
`

	// Create temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "env_test.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err, "Should create temp config file")

	// Load notification channels
	channels, err := loader.LoadNotificationChannels(ctx, configPath)
	require.NoError(t, err, "Should load channels with environment variable substitution")
	assert.Len(t, channels, 2, "Should load 2 channels")

	// Verify environment variable substitution
	emailChannel := channels[0]
	password, exists := emailChannel.Config["password"]
	assert.True(t, exists, "Should have password config")
	assert.Equal(t, "secret123", password, "Should substitute environment variable")

	slackChannel := channels[1]
	webhookURL, exists := slackChannel.Config["webhook_url"]
	assert.True(t, exists, "Should have webhook_url config")
	assert.Equal(t, "https://hooks.slack.com/services/test", webhookURL, "Should substitute environment variable")
}
