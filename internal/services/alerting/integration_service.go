package alerting

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// ProcessingResult represents the result of processing performance data
type ProcessingResult struct {
	RegressionDetected bool                         `json:"regression_detected"`
	AlertsGenerated    []*entities.PerformanceAlert `json:"alerts_generated"`
	NotificationsSent  []*entities.NotificationLog  `json:"notifications_sent"`
	ProcessedAt        time.Time                    `json:"processed_at"`
	ProcessingDuration time.Duration                `json:"processing_duration"`
	Error              error                        `json:"error,omitempty"`
}

// BaselineStorage represents baseline performance data storage
type BaselineStorage struct {
	mutex     sync.RWMutex
	baselines map[string][]entities.BenchmarkResult
}

// IntegrationService orchestrates the complete performance monitoring flow
type IntegrationService struct {
	regressionDetector  ports.RegressionDetectionService
	alertRepo           ports.AlertRepository
	notificationService ports.NotificationService
	configLoader        ports.ConfigurationLoader
	logger              *zap.Logger

	// Baseline management
	baselineStorage *BaselineStorage

	// Real-time monitoring
	monitoringActive    bool
	performanceDataChan chan []entities.BenchmarkResult

	mutex sync.RWMutex
}

// NewIntegrationService creates a new integration service
func NewIntegrationService(
	regressionDetector ports.RegressionDetectionService,
	alertRepo ports.AlertRepository,
	notificationService ports.NotificationService,
	configLoader ports.ConfigurationLoader,
	logger *zap.Logger,
) *IntegrationService {
	return &IntegrationService{
		regressionDetector:  regressionDetector,
		alertRepo:           alertRepo,
		notificationService: notificationService,
		configLoader:        configLoader,
		logger:              logger,
		baselineStorage: &BaselineStorage{
			baselines: make(map[string][]entities.BenchmarkResult),
		},
		performanceDataChan: make(chan []entities.BenchmarkResult, 100),
	}
}

// ProcessPerformanceData processes performance data through the complete monitoring flow
func (s *IntegrationService) ProcessPerformanceData(
	ctx context.Context,
	currentData []entities.BenchmarkResult,
	baselineData []entities.BenchmarkResult,
) (*ProcessingResult, error) {
	start := time.Now()

	result := &ProcessingResult{
		ProcessedAt: start,
	}

	s.logger.Info("Processing performance data",
		zap.Int("current_samples", len(currentData)),
		zap.Int("baseline_samples", len(baselineData)),
	)

	// Step 1: Detect regressions
	regressionConfig := entities.RegressionCondition{
		Method:          "statistical",
		Threshold:       0.20,
		ConfidenceLevel: 0.95,
		StatisticalTest: "t-test",
		MinSampleSize:   3,
	}

	regressionResult, err := s.regressionDetector.DetectStatisticalRegression(ctx, baselineData, currentData, regressionConfig)
	if err != nil {
		result.Error = fmt.Errorf("regression detection failed: %w", err)
		return result, err
	}

	result.RegressionDetected = regressionResult.HasRegression

	// Step 2: If regression detected, evaluate alert rules
	if result.RegressionDetected {
		alerts, err := s.evaluateAlertRules(ctx, currentData, regressionResult)
		if err != nil {
			result.Error = fmt.Errorf("alert evaluation failed: %w", err)
			return result, err
		}
		result.AlertsGenerated = alerts

		// Step 3: Send notifications for generated alerts
		notifications, err := s.sendNotifications(ctx, alerts)
		if err != nil {
			result.Error = fmt.Errorf("notification sending failed: %w", err)
			return result, err
		}
		result.NotificationsSent = notifications
	}

	result.ProcessingDuration = time.Since(start)

	s.logger.Info("Performance data processing completed",
		zap.Bool("regression_detected", result.RegressionDetected),
		zap.Int("alerts_generated", len(result.AlertsGenerated)),
		zap.Int("notifications_sent", len(result.NotificationsSent)),
		zap.Duration("processing_duration", result.ProcessingDuration),
	)

	return result, nil
}

// evaluateAlertRules evaluates all enabled alert rules against the performance data
func (s *IntegrationService) evaluateAlertRules(
	ctx context.Context,
	performanceData []entities.BenchmarkResult,
	regressionResult *ports.RegressionAnalysis,
) ([]*entities.PerformanceAlert, error) {
	// Get all enabled alert rules
	alertRules, err := s.alertRepo.ListAlertRules(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list alert rules: %w", err)
	}

	var alerts []*entities.PerformanceAlert

	for _, rule := range alertRules {
		if shouldTriggerAlert(rule, performanceData, regressionResult) {
			alert := s.createAlert(rule, performanceData, regressionResult)

			// Store the alert
			if err := s.alertRepo.CreateAlert(ctx, alert); err != nil {
				s.logger.Error("Failed to create alert",
					zap.String("rule_id", rule.ID),
					zap.Error(err),
				)
				continue
			}

			alerts = append(alerts, alert)

			s.logger.Info("Alert generated",
				zap.String("rule_id", rule.ID),
				zap.String("alert_id", alert.ID),
				zap.String("severity", string(alert.Severity)),
			)
		}
	}

	return alerts, nil
}

// shouldTriggerAlert determines if an alert rule should trigger based on performance data
func shouldTriggerAlert(
	rule *entities.AlertRule,
	performanceData []entities.BenchmarkResult,
	regressionResult *ports.RegressionAnalysis,
) bool {
	if !rule.Enabled {
		return false
	}

	// Check latency threshold
	if rule.Conditions.LatencyThreshold != nil {
		avgLatency := calculateAverageLatency(performanceData)
		if !evaluateThresholdCondition(rule.Conditions.LatencyThreshold, avgLatency) {
			return false
		}
	}

	// Check throughput threshold
	if rule.Conditions.ThroughputThreshold != nil {
		avgThroughput := calculateAverageThroughput(performanceData)
		if !evaluateThresholdCondition(rule.Conditions.ThroughputThreshold, avgThroughput) {
			return false
		}
	}

	// Check error rate threshold
	if rule.Conditions.ErrorRateThreshold != nil {
		avgErrorRate := calculateAverageErrorRate(performanceData)
		if !evaluateThresholdCondition(rule.Conditions.ErrorRateThreshold, avgErrorRate) {
			return false
		}
	}

	// Check regression detection conditions
	if rule.Conditions.RegressionDetection != nil {
		if !regressionResult.HasRegression {
			return false
		}

		// Check if the regression severity meets the rule's requirements
		if regressionResult.ConfidenceLevel < rule.Conditions.RegressionDetection.ConfidenceLevel {
			return false
		}
	}

	return true
}

// evaluateThresholdCondition evaluates a threshold condition against a value
func evaluateThresholdCondition(condition *entities.ThresholdCondition, value float64) bool {
	switch condition.Operator {
	case "gt":
		return value > condition.Value
	case "gte":
		return value >= condition.Value
	case "lt":
		return value < condition.Value
	case "lte":
		return value <= condition.Value
	case "eq":
		return value == condition.Value
	case "ne":
		return value != condition.Value
	default:
		return false
	}
}

// createAlert creates a performance alert from a rule and performance data
func (s *IntegrationService) createAlert(
	rule *entities.AlertRule,
	performanceData []entities.BenchmarkResult,
	regressionResult *ports.RegressionAnalysis,
) *entities.PerformanceAlert {
	alert := &entities.PerformanceAlert{
		ID:        generateAlertID(),
		RuleID:    rule.ID,
		RuleName:  rule.Name,
		Severity:  rule.Severity,
		Status:    entities.StatusActive,
		Message:   generateAlertMessage(rule, performanceData, regressionResult),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Context: entities.AlertContext{
			TestScenario:     getTestScenario(performanceData),
			Metric:           "latency",
			CurrentValue:     calculateAverageLatency(performanceData),
			BaselineValue:    0, // TODO: Calculate from baseline data
			PercentageChange: 0, // TODO: Calculate percentage change
			ConfidenceLevel:  regressionResult.ConfidenceLevel,
			PValue:           regressionResult.PValue,
			PerformanceData: map[string]interface{}{
				"average_latency":    calculateAverageLatency(performanceData),
				"average_throughput": calculateAverageThroughput(performanceData),
				"average_error_rate": calculateAverageErrorRate(performanceData),
			},
			Recommendations: regressionResult.Recommendations,
		},
	}

	return alert
}

// sendNotifications sends notifications for all alerts
func (s *IntegrationService) sendNotifications(
	ctx context.Context,
	alerts []*entities.PerformanceAlert,
) ([]*entities.NotificationLog, error) {
	var allLogs []*entities.NotificationLog

	for _, alert := range alerts {
		// Get the rule to find associated channels
		rule, err := s.alertRepo.GetAlertRule(ctx, alert.RuleID)
		if err != nil {
			s.logger.Error("Failed to get alert rule for notification",
				zap.String("rule_id", alert.RuleID),
				zap.Error(err),
			)
			continue
		}

		// Send notification to each configured channel
		for _, channelID := range rule.Channels {
			channel, err := s.alertRepo.GetNotificationChannel(ctx, channelID)
			if err != nil {
				s.logger.Error("Failed to get notification channel",
					zap.String("channel_id", channelID),
					zap.Error(err),
				)
				continue
			}

			if !channel.Enabled {
				continue
			}

			log, err := s.notificationService.SendNotification(ctx, alert, channel)
			if err != nil {
				s.logger.Error("Failed to send notification",
					zap.String("alert_id", alert.ID),
					zap.String("channel_id", channelID),
					zap.Error(err),
				)
				continue
			}

			allLogs = append(allLogs, log)
		}
	}

	return allLogs, nil
}

// UpdateBaseline updates the baseline for a specific test scenario
func (s *IntegrationService) UpdateBaseline(
	ctx context.Context,
	scenario string,
	performanceData []entities.BenchmarkResult,
) error {
	// Validate baseline quality
	if err := s.validateBaselineQuality(performanceData); err != nil {
		return fmt.Errorf("baseline quality validation failed: %w", err)
	}

	s.baselineStorage.mutex.Lock()
	defer s.baselineStorage.mutex.Unlock()

	s.baselineStorage.baselines[scenario] = performanceData

	s.logger.Info("Baseline updated",
		zap.String("scenario", scenario),
		zap.Int("data_points", len(performanceData)),
	)

	return nil
}

// GetBaseline retrieves the baseline for a specific test scenario
func (s *IntegrationService) GetBaseline(
	ctx context.Context,
	scenario string,
) ([]entities.BenchmarkResult, error) {
	s.baselineStorage.mutex.RLock()
	defer s.baselineStorage.mutex.RUnlock()

	baseline, exists := s.baselineStorage.baselines[scenario]
	if !exists {
		return nil, errors.New("baseline not found")
	}

	return baseline, nil
}

// validateBaselineQuality validates that performance data is suitable for use as a baseline
func (s *IntegrationService) validateBaselineQuality(data []entities.BenchmarkResult) error {
	if len(data) < 3 {
		return errors.New("insufficient data points for baseline")
	}

	// Check for excessive error rates
	avgErrorRate := calculateAverageErrorRate(data)
	if avgErrorRate > 1.0 { // More than 1% error rate
		return fmt.Errorf("baseline quality too poor: error rate %.2f%% exceeds 1%%", avgErrorRate)
	}

	// Check for excessive latency
	avgLatency := calculateAverageLatency(data)
	if avgLatency > 2000 { // More than 2 seconds
		return fmt.Errorf("baseline quality too poor: average latency %.2fms exceeds 2000ms", avgLatency)
	}

	return nil
}

// StartRealTimeMonitoring starts real-time performance monitoring
func (s *IntegrationService) StartRealTimeMonitoring(
	ctx context.Context,
	resultsChan chan *ProcessingResult,
) error {
	s.mutex.Lock()
	s.monitoringActive = true
	s.mutex.Unlock()

	defer func() {
		s.mutex.Lock()
		s.monitoringActive = false
		s.mutex.Unlock()
	}()

	s.logger.Info("Started real-time performance monitoring")

	for {
		select {
		case performanceData := <-s.performanceDataChan:
			// Process the performance data
			go func(data []entities.BenchmarkResult) {
				// Get baseline for the test scenario
				scenario := getTestScenario(data)
				baseline, err := s.GetBaseline(ctx, scenario)
				if err != nil {
					s.logger.Warn("No baseline found for real-time monitoring",
						zap.String("scenario", scenario),
					)
					return
				}

				result, err := s.ProcessPerformanceData(ctx, data, baseline)
				if err != nil {
					s.logger.Error("Real-time processing failed",
						zap.Error(err),
					)
					return
				}

				select {
				case resultsChan <- result:
				case <-ctx.Done():
					return
				}
			}(performanceData)

		case <-ctx.Done():
			s.logger.Info("Real-time monitoring stopped")
			return ctx.Err()
		}
	}
}

// SubmitPerformanceData submits performance data for real-time processing
func (s *IntegrationService) SubmitPerformanceData(
	ctx context.Context,
	data []entities.BenchmarkResult,
) {
	select {
	case s.performanceDataChan <- data:
	case <-ctx.Done():
		s.logger.Warn("Failed to submit performance data: context cancelled")
	default:
		s.logger.Warn("Performance data channel full, dropping data")
	}
}

// Helper functions

func calculateAverageLatency(data []entities.BenchmarkResult) float64 {
	if len(data) == 0 {
		return 0
	}

	total := time.Duration(0)
	for _, result := range data {
		total += result.AvgLatency
	}

	return float64(total.Nanoseconds()) / float64(len(data)) / 1000000 // Convert to milliseconds
}

func calculateAverageThroughput(data []entities.BenchmarkResult) float64 {
	if len(data) == 0 {
		return 0
	}

	total := 0.0
	for _, result := range data {
		total += result.ThroughputRPS
	}

	return total / float64(len(data))
}

func calculateAverageErrorRate(data []entities.BenchmarkResult) float64 {
	if len(data) == 0 {
		return 0
	}

	total := 0.0
	for _, result := range data {
		total += result.ErrorRatePercent
	}

	return total / float64(len(data))
}

func getTestScenario(data []entities.BenchmarkResult) string {
	if len(data) > 0 {
		return data[0].TestName
	}
	return "unknown"
}

func generateAlertID() string {
	return fmt.Sprintf("alert_%d", time.Now().UnixNano())
}

func generateAlertMessage(
	rule *entities.AlertRule,
	data []entities.BenchmarkResult,
	regressionResult *ports.RegressionAnalysis,
) string {
	avgLatency := calculateAverageLatency(data)
	avgThroughput := calculateAverageThroughput(data)
	avgErrorRate := calculateAverageErrorRate(data)

	regressionSummary := fmt.Sprintf("Regression detected using %s method (confidence: %.1f%%)", regressionResult.Method, regressionResult.ConfidenceLevel*100)
	if len(regressionResult.Recommendations) > 0 {
		regressionSummary += fmt.Sprintf(". Recommendations: %s", regressionResult.Recommendations[0])
	}

	return fmt.Sprintf(
		"Performance alert triggered for rule '%s': Average latency: %.2fms, Throughput: %.2f RPS, Error rate: %.2f%%. %s",
		rule.Name,
		avgLatency,
		avgThroughput,
		avgErrorRate,
		regressionSummary,
	)
}


