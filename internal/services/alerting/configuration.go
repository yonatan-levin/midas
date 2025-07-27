package alerting

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// ConfigurationLoader implements loading and validation of alert configurations from YAML files
type ConfigurationLoader struct {
	// TODO: Add dependencies like logger, file watcher, etc.
}

// NewConfigurationLoader creates a new configuration loader
func NewConfigurationLoader() *ConfigurationLoader {
	return &ConfigurationLoader{}
}

// LoadAlertRules loads alert rules from YAML configuration file
func (c *ConfigurationLoader) LoadAlertRules(ctx context.Context, configPath string) ([]*entities.AlertRule, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Substitute environment variables
	content := c.substituteEnvironmentVariables(string(data))

	var config struct {
		AlertRules []alertRuleConfig `yaml:"alert_rules"`
	}

	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	rules := make([]*entities.AlertRule, 0, len(config.AlertRules))
	for _, ruleConfig := range config.AlertRules {
		rule, err := c.convertAlertRule(ruleConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to convert alert rule %s: %w", ruleConfig.ID, err)
		}
		rules = append(rules, rule)
	}

	return rules, nil
}

// LoadNotificationChannels loads notification channels from YAML configuration file
func (c *ConfigurationLoader) LoadNotificationChannels(ctx context.Context, configPath string) ([]*entities.NotificationChannel, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Substitute environment variables
	content := c.substituteEnvironmentVariables(string(data))

	var config struct {
		NotificationChannels []notificationChannelConfig `yaml:"notification_channels"`
	}

	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	channels := make([]*entities.NotificationChannel, 0, len(config.NotificationChannels))
	for _, channelConfig := range config.NotificationChannels {
		channel, err := c.convertNotificationChannel(channelConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to convert notification channel %s: %w", channelConfig.ID, err)
		}
		channels = append(channels, channel)
	}

	return channels, nil
}

// LoadEscalationPolicies loads escalation policies from YAML configuration file
func (c *ConfigurationLoader) LoadEscalationPolicies(ctx context.Context, configPath string) ([]*entities.EscalationPolicy, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Substitute environment variables
	content := c.substituteEnvironmentVariables(string(data))

	var config struct {
		EscalationPolicies []escalationPolicyConfig `yaml:"escalation_policies"`
	}

	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	policies := make([]*entities.EscalationPolicy, 0, len(config.EscalationPolicies))
	for _, policyConfig := range config.EscalationPolicies {
		policy, err := c.convertEscalationPolicy(policyConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to convert escalation policy %s: %w", policyConfig.ID, err)
		}
		policies = append(policies, policy)
	}

	return policies, nil
}

// ValidateConfiguration validates a complete configuration file
func (c *ConfigurationLoader) ValidateConfiguration(ctx context.Context, configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Substitute environment variables
	content := c.substituteEnvironmentVariables(string(data))

	var config struct {
		AlertRules           []alertRuleConfig           `yaml:"alert_rules"`
		NotificationChannels []notificationChannelConfig `yaml:"notification_channels"`
		EscalationPolicies   []escalationPolicyConfig    `yaml:"escalation_policies"`
	}

	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate alert rules
	for _, ruleConfig := range config.AlertRules {
		if err := c.validateAlertRule(ruleConfig); err != nil {
			return fmt.Errorf("invalid alert rule %s: %w", ruleConfig.ID, err)
		}
	}

	// Validate notification channels
	for _, channelConfig := range config.NotificationChannels {
		if err := c.validateNotificationChannel(channelConfig); err != nil {
			return fmt.Errorf("invalid notification channel %s: %w", channelConfig.ID, err)
		}
	}

	// Validate escalation policies
	for _, policyConfig := range config.EscalationPolicies {
		if err := c.validateEscalationPolicy(policyConfig); err != nil {
			return fmt.Errorf("invalid escalation policy %s: %w", policyConfig.ID, err)
		}
	}

	return nil
}

// WatchConfiguration watches for configuration file changes
func (c *ConfigurationLoader) WatchConfiguration(ctx context.Context, configPath string) (<-chan ports.ConfigurationChange, error) {
	changes := make(chan ports.ConfigurationChange, 10)

	// Get initial file info
	initialInfo, err := os.Stat(configPath)
	if err != nil {
		close(changes)
		return nil, fmt.Errorf("failed to stat config file: %w", err)
	}

	go func() {
		defer close(changes)

		lastModTime := initialInfo.ModTime()
		ticker := time.NewTicker(500 * time.Millisecond) // Poll every 500ms
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info, err := os.Stat(configPath)
				if err != nil {
					// File might have been deleted
					continue
				}

				if info.ModTime().After(lastModTime) {
					lastModTime = info.ModTime()

					// Read the updated content
					content, err := os.ReadFile(configPath)
					if err != nil {
						continue
					}

					change := ports.ConfigurationChange{
						Type:      "updated",
						Path:      configPath,
						Timestamp: time.Now(),
						Content:   content,
					}

					select {
					case changes <- change:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return changes, nil
}

// Configuration structs for YAML parsing

type alertRuleConfig struct {
	ID                 string                    `yaml:"id"`
	Name               string                    `yaml:"name"`
	Description        string                    `yaml:"description"`
	Severity           string                    `yaml:"severity"`
	Enabled            bool                      `yaml:"enabled"`
	Channels           []string                  `yaml:"channels"`
	Conditions         alertConditionsConfig     `yaml:"conditions"`
	EscalationPolicy   *escalationPolicyConfig   `yaml:"escalation_policy,omitempty"`
	SuppressionWindows []suppressionWindowConfig `yaml:"suppression_windows,omitempty"`
}

type alertConditionsConfig struct {
	LatencyThreshold    *thresholdConditionConfig  `yaml:"latency_threshold,omitempty"`
	ThroughputThreshold *thresholdConditionConfig  `yaml:"throughput_threshold,omitempty"`
	ErrorRateThreshold  *thresholdConditionConfig  `yaml:"error_rate_threshold,omitempty"`
	RegressionDetection *regressionConditionConfig `yaml:"regression_detection,omitempty"`
	AnomalyDetection    *anomalyConditionConfig    `yaml:"anomaly_detection,omitempty"`
	TrendAnalysis       *trendConditionConfig      `yaml:"trend_analysis,omitempty"`
}

type thresholdConditionConfig struct {
	Operator    string  `yaml:"operator"`
	Value       float64 `yaml:"value"`
	Duration    string  `yaml:"duration"`
	Consecutive int     `yaml:"consecutive"`
}

type regressionConditionConfig struct {
	Method          string  `yaml:"method"`
	Threshold       float64 `yaml:"threshold"`
	ConfidenceLevel float64 `yaml:"confidence_level"`
	BaselinePeriod  string  `yaml:"baseline_period"`
	MinSampleSize   int     `yaml:"min_sample_size"`
	StatisticalTest string  `yaml:"statistical_test"`
}

type anomalyConditionConfig struct {
	Method        string  `yaml:"method"`
	Sensitivity   float64 `yaml:"sensitivity"`
	WindowSize    int     `yaml:"window_size"`
	MinDeviations float64 `yaml:"min_deviations"`
}

type trendConditionConfig struct {
	Direction       string  `yaml:"direction"`
	MinSlope        float64 `yaml:"min_slope"`
	WindowSize      int     `yaml:"window_size"`
	ConfidenceLevel float64 `yaml:"confidence_level"`
}

type escalationPolicyConfig struct {
	ID             string                 `yaml:"id"`
	Name           string                 `yaml:"name"`
	MaxEscalations int                    `yaml:"max_escalations"`
	Steps          []escalationStepConfig `yaml:"steps"`
}

type escalationStepConfig struct {
	Level    int      `yaml:"level"`
	Delay    string   `yaml:"delay"`
	Channels []string `yaml:"channels"`
}

type suppressionWindowConfig struct {
	StartTime string   `yaml:"start_time"`
	EndTime   string   `yaml:"end_time"`
	Days      []string `yaml:"days"`
	Timezone  string   `yaml:"timezone"`
}

type notificationChannelConfig struct {
	ID      string                 `yaml:"id"`
	Name    string                 `yaml:"name"`
	Type    string                 `yaml:"type"`
	Enabled bool                   `yaml:"enabled"`
	Config  map[string]interface{} `yaml:"config"`
}

// Conversion methods

func (c *ConfigurationLoader) convertAlertRule(config alertRuleConfig) (*entities.AlertRule, error) {
	// Validate required fields
	if config.ID == "" {
		return nil, fmt.Errorf("alert rule ID is required")
	}
	if config.Name == "" {
		return nil, fmt.Errorf("alert rule name is required")
	}

	// Convert severity
	severity, err := c.parseSeverity(config.Severity)
	if err != nil {
		return nil, fmt.Errorf("invalid severity: %w", err)
	}

	// Convert conditions
	conditions, err := c.convertAlertConditions(config.Conditions)
	if err != nil {
		return nil, fmt.Errorf("invalid conditions: %w", err)
	}

	// Convert escalation policy
	var escalationPolicy *entities.EscalationPolicy
	if config.EscalationPolicy != nil {
		escalationPolicy, err = c.convertEscalationPolicy(*config.EscalationPolicy)
		if err != nil {
			return nil, fmt.Errorf("invalid escalation policy: %w", err)
		}
	}

	// Convert suppression windows
	suppressionWindows := make([]entities.SuppressionWindow, len(config.SuppressionWindows))
	for i, windowConfig := range config.SuppressionWindows {
		suppressionWindows[i] = entities.SuppressionWindow{
			StartTime: windowConfig.StartTime,
			EndTime:   windowConfig.EndTime,
			Days:      windowConfig.Days,
			Timezone:  windowConfig.Timezone,
		}
	}

	return &entities.AlertRule{
		ID:                 config.ID,
		Name:               config.Name,
		Description:        config.Description,
		Severity:           severity,
		Enabled:            config.Enabled,
		Conditions:         *conditions,
		Channels:           config.Channels,
		EscalationPolicy:   escalationPolicy,
		SuppressionWindows: suppressionWindows,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}, nil
}

func (c *ConfigurationLoader) convertAlertConditions(config alertConditionsConfig) (*entities.AlertConditions, error) {
	conditions := &entities.AlertConditions{}

	if config.LatencyThreshold != nil {
		threshold, err := c.convertThresholdCondition(*config.LatencyThreshold)
		if err != nil {
			return nil, fmt.Errorf("invalid latency threshold: %w", err)
		}
		conditions.LatencyThreshold = threshold
	}

	if config.ThroughputThreshold != nil {
		threshold, err := c.convertThresholdCondition(*config.ThroughputThreshold)
		if err != nil {
			return nil, fmt.Errorf("invalid throughput threshold: %w", err)
		}
		conditions.ThroughputThreshold = threshold
	}

	if config.ErrorRateThreshold != nil {
		threshold, err := c.convertThresholdCondition(*config.ErrorRateThreshold)
		if err != nil {
			return nil, fmt.Errorf("invalid error rate threshold: %w", err)
		}
		conditions.ErrorRateThreshold = threshold
	}

	if config.RegressionDetection != nil {
		conditions.RegressionDetection = &entities.RegressionCondition{
			Method:          config.RegressionDetection.Method,
			Threshold:       config.RegressionDetection.Threshold,
			ConfidenceLevel: config.RegressionDetection.ConfidenceLevel,
			BaselinePeriod:  config.RegressionDetection.BaselinePeriod,
			MinSampleSize:   config.RegressionDetection.MinSampleSize,
			StatisticalTest: config.RegressionDetection.StatisticalTest,
		}
	}

	if config.AnomalyDetection != nil {
		conditions.AnomalyDetection = &entities.AnomalyCondition{
			Method:        config.AnomalyDetection.Method,
			Sensitivity:   config.AnomalyDetection.Sensitivity,
			WindowSize:    config.AnomalyDetection.WindowSize,
			MinDeviations: config.AnomalyDetection.MinDeviations,
		}
	}

	if config.TrendAnalysis != nil {
		conditions.TrendAnalysis = &entities.TrendCondition{
			Direction:       config.TrendAnalysis.Direction,
			MinSlope:        config.TrendAnalysis.MinSlope,
			WindowSize:      config.TrendAnalysis.WindowSize,
			ConfidenceLevel: config.TrendAnalysis.ConfidenceLevel,
		}
	}

	return conditions, nil
}

func (c *ConfigurationLoader) convertThresholdCondition(config thresholdConditionConfig) (*entities.ThresholdCondition, error) {
	// Validate operator
	if !c.isValidOperator(config.Operator) {
		return nil, fmt.Errorf("invalid operator: %s", config.Operator)
	}

	// Validate value
	if config.Value < 0 {
		return nil, fmt.Errorf("threshold value cannot be negative: %f", config.Value)
	}

	return &entities.ThresholdCondition{
		Operator:    config.Operator,
		Value:       config.Value,
		Duration:    config.Duration,
		Consecutive: config.Consecutive,
	}, nil
}

func (c *ConfigurationLoader) convertNotificationChannel(config notificationChannelConfig) (*entities.NotificationChannel, error) {
	// Validate required fields
	if config.ID == "" {
		return nil, fmt.Errorf("notification channel ID is required")
	}
	if config.Name == "" {
		return nil, fmt.Errorf("notification channel name is required")
	}
	if config.Type == "" {
		return nil, fmt.Errorf("notification channel type is required")
	}

	// Validate channel type
	if !c.isValidChannelType(config.Type) {
		return nil, fmt.Errorf("unsupported channel type: %s", config.Type)
	}

	// Validate channel-specific configuration
	if err := c.validateChannelConfig(config.Type, config.Config); err != nil {
		return nil, fmt.Errorf("invalid channel config: %w", err)
	}

	return &entities.NotificationChannel{
		ID:        config.ID,
		Name:      config.Name,
		Type:      config.Type,
		Config:    config.Config,
		Enabled:   config.Enabled,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (c *ConfigurationLoader) convertEscalationPolicy(config escalationPolicyConfig) (*entities.EscalationPolicy, error) {
	// Validate required fields
	if config.ID == "" {
		return nil, fmt.Errorf("escalation policy ID is required")
	}
	if config.Name == "" {
		return nil, fmt.Errorf("escalation policy name is required")
	}
	if len(config.Steps) == 0 {
		return nil, fmt.Errorf("escalation policy must have at least one step")
	}

	// Convert steps
	steps := make([]entities.EscalationStep, len(config.Steps))
	for i, stepConfig := range config.Steps {
		steps[i] = entities.EscalationStep{
			Level:    stepConfig.Level,
			Delay:    stepConfig.Delay,
			Channels: stepConfig.Channels,
		}
	}

	return &entities.EscalationPolicy{
		ID:             config.ID,
		Name:           config.Name,
		Steps:          steps,
		MaxEscalations: config.MaxEscalations,
	}, nil
}

// Validation methods

func (c *ConfigurationLoader) validateAlertRule(config alertRuleConfig) error {
	if config.ID == "" {
		return fmt.Errorf("ID is required")
	}
	if config.Name == "" {
		return fmt.Errorf("name is required")
	}
	if _, err := c.parseSeverity(config.Severity); err != nil {
		return fmt.Errorf("invalid severity: %w", err)
	}
	return c.validateAlertConditionsConfig(config.Conditions)
}

func (c *ConfigurationLoader) validateAlertConditionsConfig(config alertConditionsConfig) error {
	if config.LatencyThreshold != nil {
		if err := c.validateThresholdCondition(*config.LatencyThreshold); err != nil {
			return fmt.Errorf("invalid latency threshold: %w", err)
		}
	}
	if config.ThroughputThreshold != nil {
		if err := c.validateThresholdCondition(*config.ThroughputThreshold); err != nil {
			return fmt.Errorf("invalid throughput threshold: %w", err)
		}
	}
	if config.ErrorRateThreshold != nil {
		if err := c.validateThresholdCondition(*config.ErrorRateThreshold); err != nil {
			return fmt.Errorf("invalid error rate threshold: %w", err)
		}
	}
	return nil
}

func (c *ConfigurationLoader) validateThresholdCondition(config thresholdConditionConfig) error {
	if !c.isValidOperator(config.Operator) {
		return fmt.Errorf("invalid operator: %s", config.Operator)
	}
	if config.Value < 0 {
		return fmt.Errorf("value cannot be negative: %f", config.Value)
	}
	return nil
}

func (c *ConfigurationLoader) validateNotificationChannel(config notificationChannelConfig) error {
	if config.ID == "" {
		return fmt.Errorf("ID is required")
	}
	if config.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !c.isValidChannelType(config.Type) {
		return fmt.Errorf("unsupported channel type: %s", config.Type)
	}
	return c.validateChannelConfig(config.Type, config.Config)
}

func (c *ConfigurationLoader) validateEscalationPolicy(config escalationPolicyConfig) error {
	if config.ID == "" {
		return fmt.Errorf("ID is required")
	}
	if config.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(config.Steps) == 0 {
		return fmt.Errorf("must have at least one step")
	}
	return nil
}

// Helper methods

func (c *ConfigurationLoader) parseSeverity(severity string) (entities.AlertSeverity, error) {
	switch strings.ToLower(severity) {
	case "info":
		return entities.SeverityInfo, nil
	case "warning":
		return entities.SeverityWarning, nil
	case "critical":
		return entities.SeverityCritical, nil
	default:
		return "", fmt.Errorf("invalid severity: %s (must be info, warning, or critical)", severity)
	}
}

func (c *ConfigurationLoader) isValidOperator(operator string) bool {
	validOperators := []string{"gt", "gte", "lt", "lte", "eq", "ne"}
	for _, valid := range validOperators {
		if operator == valid {
			return true
		}
	}
	return false
}

func (c *ConfigurationLoader) isValidChannelType(channelType string) bool {
	validTypes := []string{"email", "slack", "webhook", "pagerduty"}
	for _, valid := range validTypes {
		if channelType == valid {
			return true
		}
	}
	return false
}

func (c *ConfigurationLoader) validateChannelConfig(channelType string, config map[string]interface{}) error {
	if config == nil {
		return fmt.Errorf("channel config is required")
	}

	switch channelType {
	case "email":
		return c.validateEmailConfig(config)
	case "slack":
		return c.validateSlackConfig(config)
	case "webhook":
		return c.validateWebhookConfig(config)
	case "pagerduty":
		return c.validatePagerDutyConfig(config)
	default:
		return fmt.Errorf("unsupported channel type: %s", channelType)
	}
}

func (c *ConfigurationLoader) validateEmailConfig(config map[string]interface{}) error {
	requiredFields := []string{"smtp_server", "from_email", "to_emails"}
	for _, field := range requiredFields {
		if _, exists := config[field]; !exists {
			return fmt.Errorf("required field %s is missing", field)
		}
	}
	return nil
}

func (c *ConfigurationLoader) validateSlackConfig(config map[string]interface{}) error {
	if _, exists := config["webhook_url"]; !exists {
		return fmt.Errorf("webhook_url is required for Slack channels")
	}
	return nil
}

func (c *ConfigurationLoader) validateWebhookConfig(config map[string]interface{}) error {
	if _, exists := config["url"]; !exists {
		return fmt.Errorf("url is required for webhook channels")
	}
	return nil
}

func (c *ConfigurationLoader) validatePagerDutyConfig(config map[string]interface{}) error {
	if _, exists := config["integration_key"]; !exists {
		return fmt.Errorf("integration_key is required for PagerDuty channels")
	}
	return nil
}

// substituteEnvironmentVariables replaces ${VAR_NAME} patterns with environment variable values
func (c *ConfigurationLoader) substituteEnvironmentVariables(content string) string {
	// Pattern to match ${VAR_NAME}
	re := regexp.MustCompile(`\$\{([^}]+)\}`)

	return re.ReplaceAllStringFunc(content, func(match string) string {
		// Extract variable name from ${VAR_NAME}
		varName := match[2 : len(match)-1]

		// Get environment variable value
		if value := os.Getenv(varName); value != "" {
			return value
		}

		// Return original if environment variable is not set
		return match
	})
}
