package alerting

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// MockAlertRepository for testing
type MockAlertRepository struct {
	mock.Mock
}

func (m *MockAlertRepository) CreateAlert(ctx context.Context, alert *entities.PerformanceAlert) error {
	args := m.Called(ctx, alert)
	return args.Error(0)
}

func (m *MockAlertRepository) GetAlert(ctx context.Context, id string) (*entities.PerformanceAlert, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*entities.PerformanceAlert), args.Error(1)
}

func (m *MockAlertRepository) UpdateAlert(ctx context.Context, alert *entities.PerformanceAlert) error {
	args := m.Called(ctx, alert)
	return args.Error(0)
}

func (m *MockAlertRepository) DeleteAlert(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockAlertRepository) ListActiveAlerts(ctx context.Context) ([]*entities.PerformanceAlert, error) {
	args := m.Called(ctx)
	return args.Get(0).([]*entities.PerformanceAlert), args.Error(1)
}

func (m *MockAlertRepository) ListAlertsByRule(ctx context.Context, ruleID string) ([]*entities.PerformanceAlert, error) {
	args := m.Called(ctx, ruleID)
	return args.Get(0).([]*entities.PerformanceAlert), args.Error(1)
}

func (m *MockAlertRepository) ListAlertsByStatus(ctx context.Context, status entities.AlertStatus) ([]*entities.PerformanceAlert, error) {
	args := m.Called(ctx, status)
	return args.Get(0).([]*entities.PerformanceAlert), args.Error(1)
}

func (m *MockAlertRepository) ListAlertsInTimeRange(ctx context.Context, start, end time.Time) ([]*entities.PerformanceAlert, error) {
	args := m.Called(ctx, start, end)
	return args.Get(0).([]*entities.PerformanceAlert), args.Error(1)
}

func (m *MockAlertRepository) CreateAlertRule(ctx context.Context, rule *entities.AlertRule) error {
	args := m.Called(ctx, rule)
	return args.Error(0)
}

func (m *MockAlertRepository) GetAlertRule(ctx context.Context, id string) (*entities.AlertRule, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*entities.AlertRule), args.Error(1)
}

func (m *MockAlertRepository) UpdateAlertRule(ctx context.Context, rule *entities.AlertRule) error {
	args := m.Called(ctx, rule)
	return args.Error(0)
}

func (m *MockAlertRepository) DeleteAlertRule(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockAlertRepository) ListAlertRules(ctx context.Context, enabled bool) ([]*entities.AlertRule, error) {
	args := m.Called(ctx, enabled)
	return args.Get(0).([]*entities.AlertRule), args.Error(1)
}

func (m *MockAlertRepository) CreateNotificationChannel(ctx context.Context, channel *entities.NotificationChannel) error {
	args := m.Called(ctx, channel)
	return args.Error(0)
}

func (m *MockAlertRepository) GetNotificationChannel(ctx context.Context, id string) (*entities.NotificationChannel, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*entities.NotificationChannel), args.Error(1)
}

func (m *MockAlertRepository) UpdateNotificationChannel(ctx context.Context, channel *entities.NotificationChannel) error {
	args := m.Called(ctx, channel)
	return args.Error(0)
}

func (m *MockAlertRepository) DeleteNotificationChannel(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockAlertRepository) ListNotificationChannels(ctx context.Context, enabled bool) ([]*entities.NotificationChannel, error) {
	args := m.Called(ctx, enabled)
	return args.Get(0).([]*entities.NotificationChannel), args.Error(1)
}

func (m *MockAlertRepository) SaveTrendAnalysis(ctx context.Context, analysis *entities.TrendAnalysis) error {
	args := m.Called(ctx, analysis)
	return args.Error(0)
}

func (m *MockAlertRepository) GetTrendAnalysis(ctx context.Context, id string) (*entities.TrendAnalysis, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*entities.TrendAnalysis), args.Error(1)
}

func (m *MockAlertRepository) ListTrendAnalyses(ctx context.Context, scenario string, metric string, limit int) ([]*entities.TrendAnalysis, error) {
	args := m.Called(ctx, scenario, metric, limit)
	return args.Get(0).([]*entities.TrendAnalysis), args.Error(1)
}

func (m *MockAlertRepository) SaveAnomalyDetectionResult(ctx context.Context, result *entities.AnomalyDetectionResult) error {
	args := m.Called(ctx, result)
	return args.Error(0)
}

func (m *MockAlertRepository) GetAnomalyDetectionResult(ctx context.Context, id string) (*entities.AnomalyDetectionResult, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*entities.AnomalyDetectionResult), args.Error(1)
}

func (m *MockAlertRepository) ListAnomalies(ctx context.Context, scenario string, metric string, limit int) ([]*entities.AnomalyDetectionResult, error) {
	args := m.Called(ctx, scenario, metric, limit)
	return args.Get(0).([]*entities.AnomalyDetectionResult), args.Error(1)
}

// MockNotificationService for testing
type MockNotificationService struct {
	mock.Mock
}

func (m *MockNotificationService) SendNotification(ctx context.Context, alert *entities.PerformanceAlert, channel *entities.NotificationChannel) (*entities.NotificationLog, error) {
	args := m.Called(ctx, alert, channel)
	return args.Get(0).(*entities.NotificationLog), args.Error(1)
}

func (m *MockNotificationService) SendBulkNotifications(ctx context.Context, alert *entities.PerformanceAlert, channels []*entities.NotificationChannel) ([]*entities.NotificationLog, error) {
	args := m.Called(ctx, alert, channels)
	return args.Get(0).([]*entities.NotificationLog), args.Error(1)
}

func (m *MockNotificationService) RetryFailedNotifications(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockNotificationService) TestNotificationChannel(ctx context.Context, channel *entities.NotificationChannel) error {
	args := m.Called(ctx, channel)
	return args.Error(0)
}

func (m *MockNotificationService) RenderNotificationTemplate(ctx context.Context, alert *entities.PerformanceAlert, templateType string) (string, string, error) {
	args := m.Called(ctx, alert, templateType)
	return args.String(0), args.String(1), args.Error(2)
}

// TestIntegrationService_CompleteFlow tests the complete performance monitoring flow
func TestIntegrationService_CompleteFlow(t *testing.T) {
	// Setup mocks
	regressionDetector := &EnhancedRegressionDetectionService{}
	alertRepo := &MockAlertRepository{}
	notificationService := &MockNotificationService{}
	configLoader := &ConfigurationLoader{}
	
	service := NewIntegrationService(
		regressionDetector,
		alertRepo,
		notificationService,
		configLoader,
		zap.NewNop(),
	)

	ctx := context.Background()

	t.Run("complete_performance_monitoring_flow", func(t *testing.T) {
		// Create test performance data representing a regression
		baselineData := []entities.BenchmarkResult{
			createTestBenchmarkResult("api_test", 300*time.Millisecond, 25.0, 0.1),
			createTestBenchmarkResult("api_test", 310*time.Millisecond, 24.5, 0.2),
			createTestBenchmarkResult("api_test", 295*time.Millisecond, 25.5, 0.1),
			createTestBenchmarkResult("api_test", 305*time.Millisecond, 24.8, 0.15),
			createTestBenchmarkResult("api_test", 290*time.Millisecond, 25.2, 0.1),
		}

		currentData := []entities.BenchmarkResult{
			createTestBenchmarkResult("api_test", 450*time.Millisecond, 25.1, 0.12), // 50% latency increase
			createTestBenchmarkResult("api_test", 465*time.Millisecond, 24.3, 0.18),
			createTestBenchmarkResult("api_test", 442*time.Millisecond, 25.3, 0.11),
			createTestBenchmarkResult("api_test", 458*time.Millisecond, 25.0, 0.14),
			createTestBenchmarkResult("api_test", 435*time.Millisecond, 24.9, 0.13),
		}

		// Setup test alert rule
		alertRule := &entities.AlertRule{
			ID:          "high_latency_critical",
			Name:        "Critical Latency Alert",
			Description: "Alerts when latency exceeds 500ms",
			Severity:    entities.SeverityCritical,
			Enabled:     true,
			Channels:    []string{"email-ops", "slack-critical"},
			Conditions: entities.AlertConditions{
				LatencyThreshold: &entities.ThresholdCondition{
					Operator:    "gt",
					Value:       400.0, // 400ms threshold
					Duration:    "5m",
					Consecutive: 2,
				},
				RegressionDetection: &entities.RegressionCondition{
					Method:          "statistical",
					Threshold:       0.20, // 20% change threshold
					ConfidenceLevel: 0.95,
					StatisticalTest: "t-test",
					MinSampleSize:   5,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Setup notification channels
		emailChannel := &entities.NotificationChannel{
			ID:      "email-ops",
			Name:    "Operations Email",
			Type:    "email",
			Enabled: true,
			Config: map[string]interface{}{
				"smtp_server": "smtp.company.com",
				"from_email":  "alerts@company.com",
				"to_emails":   []string{"ops@company.com"},
			},
		}

		slackChannel := &entities.NotificationChannel{
			ID:      "slack-critical",
			Name:    "Critical Slack Channel",
			Type:    "slack",
			Enabled: true,
			Config: map[string]interface{}{
				"webhook_url": "https://hooks.slack.com/services/test",
				"channel":     "#critical-alerts",
			},
		}

		// Setup mock expectations
		alertRepo.On("ListAlertRules", ctx, true).Return([]*entities.AlertRule{alertRule}, nil)
		alertRepo.On("GetAlertRule", ctx, "high_latency_critical").Return(alertRule, nil)
		alertRepo.On("GetNotificationChannel", ctx, "email-ops").Return(emailChannel, nil)
		alertRepo.On("GetNotificationChannel", ctx, "slack-critical").Return(slackChannel, nil)
		alertRepo.On("CreateAlert", ctx, mock.AnythingOfType("*entities.PerformanceAlert")).Return(nil)

		// Mock notification sending
		emailLog := &entities.NotificationLog{
			ID:          "email-log-1",
			ChannelID:   "email-ops",
			ChannelType: "email",
			Status:      "sent",
			SentAt:      time.Now(),
		}
		slackLog := &entities.NotificationLog{
			ID:          "slack-log-1",
			ChannelID:   "slack-critical",
			ChannelType: "slack",
			Status:      "sent",
			SentAt:      time.Now(),
		}

		notificationService.On("SendNotification", ctx, mock.AnythingOfType("*entities.PerformanceAlert"), emailChannel).Return(emailLog, nil)
		notificationService.On("SendNotification", ctx, mock.AnythingOfType("*entities.PerformanceAlert"), slackChannel).Return(slackLog, nil)

		// Execute the complete flow: Performance Data → Regression Detection → Alert Creation → Notification
		result, err := service.ProcessPerformanceData(ctx, currentData, baselineData)

		// Verify the complete flow worked
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.True(t, result.RegressionDetected, "Should detect performance regression")
		assert.Greater(t, len(result.AlertsGenerated), 0, "Should generate alerts")
		assert.Greater(t, len(result.NotificationsSent), 0, "Should send notifications")

		// Verify alert details
		alert := result.AlertsGenerated[0]
		assert.Equal(t, "high_latency_critical", alert.RuleID)
		assert.Equal(t, entities.SeverityCritical, alert.Severity)
		assert.Equal(t, entities.StatusActive, alert.Status)
		assert.Contains(t, alert.Message, "latency")

		// Verify notifications were sent
		assert.Len(t, result.NotificationsSent, 2, "Should send notifications to both channels")
		
		// Verify notification channels
		channelsSent := make(map[string]bool)
		for _, log := range result.NotificationsSent {
			channelsSent[log.ChannelID] = true
			assert.Equal(t, "sent", log.Status)
		}
		assert.True(t, channelsSent["email-ops"], "Should send email notification")
		assert.True(t, channelsSent["slack-critical"], "Should send slack notification")

		// Verify all mock expectations
		alertRepo.AssertExpectations(t)
		notificationService.AssertExpectations(t)
	})

	t.Run("no_alerts_for_good_performance", func(t *testing.T) {
		// Create test data with good performance (no regression)
		baselineData := []entities.BenchmarkResult{
			createTestBenchmarkResult("api_test", 300*time.Millisecond, 25.0, 0.1),
			createTestBenchmarkResult("api_test", 310*time.Millisecond, 24.5, 0.2),
			createTestBenchmarkResult("api_test", 295*time.Millisecond, 25.5, 0.1),
		}

		currentData := []entities.BenchmarkResult{
			createTestBenchmarkResult("api_test", 298*time.Millisecond, 25.1, 0.12), // Similar performance
			createTestBenchmarkResult("api_test", 305*time.Millisecond, 24.8, 0.15),
			createTestBenchmarkResult("api_test", 292*time.Millisecond, 25.0, 0.14),
		}

		// Reset mocks
		alertRepo.ExpectedCalls = nil
		notificationService.ExpectedCalls = nil

		// Setup mock - no rules trigger for good performance
		alertRepo.On("ListAlertRules", ctx, true).Return([]*entities.AlertRule{}, nil)

		result, err := service.ProcessPerformanceData(ctx, currentData, baselineData)

		require.NoError(t, err)
		require.NotNil(t, result)

		assert.False(t, result.RegressionDetected, "Should not detect regression with good performance")
		assert.Empty(t, result.AlertsGenerated, "Should not generate alerts for good performance")
		assert.Empty(t, result.NotificationsSent, "Should not send notifications for good performance")

		alertRepo.AssertExpectations(t)
	})
}

// TestIntegrationService_BaselineManagement tests automated baseline management
func TestIntegrationService_BaselineManagement(t *testing.T) {
	regressionDetector := &EnhancedRegressionDetectionService{}
	alertRepo := &MockAlertRepository{}
	notificationService := &MockNotificationService{}
	configLoader := &ConfigurationLoader{}
	
	service := NewIntegrationService(
		regressionDetector,
		alertRepo,
		notificationService,
		configLoader,
		zap.NewNop(),
	)

	ctx := context.Background()

	t.Run("create_baseline_from_good_performance", func(t *testing.T) {
		// Good performance data that should become the new baseline
		performanceData := []entities.BenchmarkResult{
			createTestBenchmarkResult("api_test", 280*time.Millisecond, 26.0, 0.08), // Better performance
			createTestBenchmarkResult("api_test", 285*time.Millisecond, 25.8, 0.09),
			createTestBenchmarkResult("api_test", 275*time.Millisecond, 26.2, 0.07),
			createTestBenchmarkResult("api_test", 290*time.Millisecond, 25.9, 0.08),
			createTestBenchmarkResult("api_test", 282*time.Millisecond, 26.1, 0.09),
		}

		err := service.UpdateBaseline(ctx, "api_test", performanceData)
		require.NoError(t, err)

		// Verify baseline was stored
		baseline, err := service.GetBaseline(ctx, "api_test")
		require.NoError(t, err)
		require.NotNil(t, baseline)
		assert.Len(t, baseline, 5, "Should store all baseline data points")
	})

	t.Run("baseline_quality_validation", func(t *testing.T) {
		// Poor quality data that should not become baseline
		poorQualityData := []entities.BenchmarkResult{
			createTestBenchmarkResult("api_test", 300*time.Millisecond, 25.0, 5.0),  // High error rate
			createTestBenchmarkResult("api_test", 1200*time.Millisecond, 15.0, 8.0), // Very high latency
		}

		err := service.UpdateBaseline(ctx, "api_test", poorQualityData)
		assert.Error(t, err, "Should reject poor quality baseline data")
		assert.Contains(t, err.Error(), "quality")
	})
}

// TestIntegrationService_RealTimeMonitoring tests real-time performance monitoring
func TestIntegrationService_RealTimeMonitoring(t *testing.T) {
	regressionDetector := &EnhancedRegressionDetectionService{}
	alertRepo := &MockAlertRepository{}
	notificationService := &MockNotificationService{}
	configLoader := &ConfigurationLoader{}
	
	service := NewIntegrationService(
		regressionDetector,
		alertRepo,
		notificationService,
		configLoader,
		zap.NewNop(),
	)

	ctx := context.Background()

	t.Run("real_time_performance_analysis", func(t *testing.T) {
		// Set up baseline data for the real-time test scenario
		baselineData := []entities.BenchmarkResult{
			createTestBenchmarkResult("real_time_test", 300*time.Millisecond, 25.0, 0.1),
			createTestBenchmarkResult("real_time_test", 285*time.Millisecond, 26.2, 0.08),
			createTestBenchmarkResult("real_time_test", 295*time.Millisecond, 25.8, 0.12),
		}
		err := service.UpdateBaseline(ctx, "real_time_test", baselineData)
		require.NoError(t, err, "Should set up baseline for real-time test")

		// Start real-time monitoring
		monitoringCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Channel to receive monitoring results
		resultsChan := make(chan *ProcessingResult, 10)

		// Start monitoring
		go func() {
			err := service.StartRealTimeMonitoring(monitoringCtx, resultsChan)
			assert.NoError(t, err)
		}()

		// Simulate incoming performance data
		go func() {
			time.Sleep(100 * time.Millisecond)
			
			// Send good performance data
			goodData := []entities.BenchmarkResult{
				createTestBenchmarkResult("real_time_test", 300*time.Millisecond, 25.0, 0.1),
			}
			service.SubmitPerformanceData(ctx, goodData)

			time.Sleep(200 * time.Millisecond)
			
			// Send regression data
			regressionData := []entities.BenchmarkResult{
				createTestBenchmarkResult("real_time_test", 600*time.Millisecond, 12.0, 2.0), // Clear regression
			}
			service.SubmitPerformanceData(ctx, regressionData)
		}()

		// Collect results
		var results []*ProcessingResult
		for {
			select {
			case result := <-resultsChan:
				results = append(results, result)
				if len(results) >= 2 { // Got both good and regression results
					cancel()
					return
				}
			case <-monitoringCtx.Done():
				break
			}
		}

		// Verify real-time monitoring detected the regression
		assert.Len(t, results, 2, "Should process both performance data submissions")
		
		// First result should be good performance (no alerts)
		assert.False(t, results[0].RegressionDetected)
		assert.Empty(t, results[0].AlertsGenerated)

		// Second result should detect regression
		assert.True(t, results[1].RegressionDetected)
		// Note: Actual alert generation would depend on configured rules
	})
} 
