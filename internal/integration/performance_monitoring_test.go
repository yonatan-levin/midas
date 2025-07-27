package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/midas/dcf-valuation-api/internal/api/v1/handlers"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/services/alerting"
	"github.com/midas/dcf-valuation-api/internal/services/metrics"
)

// Mock implementations for integration testing

// MockRegressionDetector is a mock implementation of RegressionDetectionService
type MockRegressionDetector struct {
	mock.Mock
}

func (m *MockRegressionDetector) DetectStatisticalRegression(ctx context.Context, baseline, current []entities.BenchmarkResult, config entities.RegressionCondition) (*ports.RegressionAnalysis, error) {
	args := m.Called(ctx, baseline, current, config)
	return args.Get(0).(*ports.RegressionAnalysis), args.Error(1)
}

func (m *MockRegressionDetector) DetectTrendRegression(ctx context.Context, historicalData []entities.BenchmarkResult, config entities.RegressionCondition) (*ports.RegressionAnalysis, error) {
	args := m.Called(ctx, historicalData, config)
	return args.Get(0).(*ports.RegressionAnalysis), args.Error(1)
}

func (m *MockRegressionDetector) ComparePerformanceDatasets(ctx context.Context, baseline, current []entities.BenchmarkResult, confidenceLevel float64) (*ports.ComparisonResult, error) {
	args := m.Called(ctx, baseline, current, confidenceLevel)
	return args.Get(0).(*ports.ComparisonResult), args.Error(1)
}

func (m *MockRegressionDetector) CalculateStatisticalSignificance(ctx context.Context, data1, data2 []float64, testType string) (*ports.StatisticalTestResult, error) {
	args := m.Called(ctx, data1, data2, testType)
	return args.Get(0).(*ports.StatisticalTestResult), args.Error(1)
}

// MockAlertRepository is a mock implementation of AlertRepository
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

// MockNotificationService is a mock implementation of NotificationService
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

// MockConfigLoader is a mock implementation of ConfigurationLoader
type MockConfigLoader struct {
	mock.Mock
}

func (m *MockConfigLoader) LoadAlertRules(ctx context.Context, configPath string) ([]*entities.AlertRule, error) {
	args := m.Called(ctx, configPath)
	return args.Get(0).([]*entities.AlertRule), args.Error(1)
}

func (m *MockConfigLoader) LoadNotificationChannels(ctx context.Context, configPath string) ([]*entities.NotificationChannel, error) {
	args := m.Called(ctx, configPath)
	return args.Get(0).([]*entities.NotificationChannel), args.Error(1)
}

func (m *MockConfigLoader) LoadEscalationPolicies(ctx context.Context, configPath string) ([]*entities.EscalationPolicy, error) {
	args := m.Called(ctx, configPath)
	return args.Get(0).([]*entities.EscalationPolicy), args.Error(1)
}

func (m *MockConfigLoader) ValidateConfiguration(ctx context.Context, configPath string) error {
	args := m.Called(ctx, configPath)
	return args.Error(0)
}

func (m *MockConfigLoader) WatchConfiguration(ctx context.Context, configPath string) (<-chan ports.ConfigurationChange, error) {
	args := m.Called(ctx, configPath)
	return args.Get(0).(<-chan ports.ConfigurationChange), args.Error(1)
}

// TestEndToEndPerformanceMonitoring tests the complete performance monitoring flow
func TestEndToEndPerformanceMonitoring(t *testing.T) {
	// Setup test environment
	logger := zaptest.NewLogger(t)
	metricsService := metrics.NewService(logger)

	// Create mock implementations for integration testing
	regressionDetector := &MockRegressionDetector{}
	alertRepo := &MockAlertRepository{}
	notificationService := &MockNotificationService{}
	configLoader := &MockConfigLoader{}

	// Create integration service
	integrationService := alerting.NewIntegrationService(
		regressionDetector,
		alertRepo,
		notificationService,
		configLoader,
		logger,
	)

	// Create performance handler
	performanceHandler := handlers.NewPerformanceHandler(
		logger,
		integrationService,
		alertRepo,
		metricsService,
	)

	// Setup Gin router
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Register performance endpoints
	v1 := router.Group("/api/v1")
	{
		performance := v1.Group("/performance")
		{
			performance.GET("/dashboard", performanceHandler.GetPerformanceDashboard)
			performance.GET("/alerts", performanceHandler.GetPerformanceAlerts)
			performance.GET("/baselines", performanceHandler.GetPerformanceBaselines)
		}
	}

	// Setup test data
	ctx := context.Background()

	t.Run("complete_performance_monitoring_workflow", func(t *testing.T) {
		// Step 1: Simulate some application metrics
		metricsService.RecordHTTPRequest("GET", "/api/v1/fair-value/AAPL", 200, 250*time.Millisecond, 1024)
		metricsService.RecordValuationRequest("AAPL", "single", "success", 200*time.Millisecond)
		metricsService.IncDCFCalculations()

		// Step 2: Create performance data that indicates regression
		baselineData := []entities.BenchmarkResult{
			createBenchmarkResult("api_performance_test", 300*time.Millisecond, 25.0, 0.1),
			createBenchmarkResult("api_performance_test", 285*time.Millisecond, 26.2, 0.08),
			createBenchmarkResult("api_performance_test", 295*time.Millisecond, 25.8, 0.12),
			createBenchmarkResult("api_performance_test", 310*time.Millisecond, 24.9, 0.09),
			createBenchmarkResult("api_performance_test", 290*time.Millisecond, 25.5, 0.11),
		}

		currentData := []entities.BenchmarkResult{
			createBenchmarkResult("api_performance_test", 520*time.Millisecond, 15.2, 1.8), // Clear regression
			createBenchmarkResult("api_performance_test", 535*time.Millisecond, 14.8, 2.1),
			createBenchmarkResult("api_performance_test", 510*time.Millisecond, 15.5, 1.6),
			createBenchmarkResult("api_performance_test", 545*time.Millisecond, 14.9, 2.3),
			createBenchmarkResult("api_performance_test", 525*time.Millisecond, 15.1, 1.9),
		}

		// Step 3: Setup mock expectations for regression detection
		regressionResult := &ports.RegressionAnalysis{
			HasRegression:   true,
			Severity:        entities.SeverityCritical,
			Method:          "statistical",
			ConfidenceLevel: 0.98,
			PValue:          0.001,
			EffectSize:      2.1,
			Details: map[string]interface{}{
				"latency_increase_percent":    75.2,
				"throughput_decrease_percent": 40.8,
				"error_rate_increase_factor":  18.5,
			},
			Recommendations: []string{
				"Investigate recent deployments",
				"Check database performance",
				"Review resource utilization",
			},
		}

		regressionDetector.On("DetectStatisticalRegression", ctx, baselineData, currentData, mock.AnythingOfType("entities.RegressionCondition")).Return(regressionResult, nil)

		// Step 4: Setup alert rule that should trigger
		alertRule := &entities.AlertRule{
			ID:          "critical_performance_degradation",
			Name:        "Critical Performance Degradation",
			Description: "Alerts when performance significantly degrades",
			Severity:    entities.SeverityCritical,
			Enabled:     true,
			Channels:    []string{"email-ops", "slack-critical", "pagerduty-oncall"},
			Conditions: entities.AlertConditions{
				LatencyThreshold: &entities.ThresholdCondition{
					Operator:    "gt",
					Value:       400.0, // 400ms threshold
					Duration:    "5m",
					Consecutive: 2,
				},
				RegressionDetection: &entities.RegressionCondition{
					Method:          "statistical",
					Threshold:       0.30, // 30% regression threshold
					ConfidenceLevel: 0.95,
					StatisticalTest: "t-test",
					MinSampleSize:   5,
				},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		alertRepo.On("ListAlertRules", ctx, true).Return([]*entities.AlertRule{alertRule}, nil)
		alertRepo.On("GetAlertRule", ctx, "critical_performance_degradation").Return(alertRule, nil)

		// Step 5: Setup notification channels
		emailChannel := createEmailChannel("email-ops")
		slackChannel := createSlackChannel("slack-critical")
		pagerDutyChannel := createPagerDutyChannel("pagerduty-oncall")

		alertRepo.On("GetNotificationChannel", ctx, "email-ops").Return(emailChannel, nil)
		alertRepo.On("GetNotificationChannel", ctx, "slack-critical").Return(slackChannel, nil)
		alertRepo.On("GetNotificationChannel", ctx, "pagerduty-oncall").Return(pagerDutyChannel, nil)

		// Step 6: Setup alert creation and notification expectations
		alertRepo.On("CreateAlert", ctx, mock.AnythingOfType("*entities.PerformanceAlert")).Return(nil)

		emailLog := createNotificationLog("email-log-1", "email-ops", "email", "sent")
		slackLog := createNotificationLog("slack-log-1", "slack-critical", "slack", "sent")
		pagerDutyLog := createNotificationLog("pagerduty-log-1", "pagerduty-oncall", "pagerduty", "sent")

		notificationService.On("SendNotification", ctx, mock.AnythingOfType("*entities.PerformanceAlert"), emailChannel).Return(emailLog, nil)
		notificationService.On("SendNotification", ctx, mock.AnythingOfType("*entities.PerformanceAlert"), slackChannel).Return(slackLog, nil)
		notificationService.On("SendNotification", ctx, mock.AnythingOfType("*entities.PerformanceAlert"), pagerDutyChannel).Return(pagerDutyLog, nil)

		// Step 7: Process performance data through the complete pipeline
		result, err := integrationService.ProcessPerformanceData(ctx, currentData, baselineData)

		// Step 8: Verify the complete flow worked correctly
		require.NoError(t, err, "Performance data processing should succeed")
		require.NotNil(t, result, "Processing result should not be nil")

		assert.True(t, result.RegressionDetected, "Should detect performance regression")
		assert.Greater(t, len(result.AlertsGenerated), 0, "Should generate at least one alert")
		assert.Len(t, result.NotificationsSent, 3, "Should send notifications to all 3 channels")

		// Verify alert details
		alert := result.AlertsGenerated[0]
		assert.Equal(t, "critical_performance_degradation", alert.RuleID)
		assert.Equal(t, entities.SeverityCritical, alert.Severity)
		assert.Equal(t, entities.StatusActive, alert.Status)
		assert.Contains(t, alert.Message, "Performance")
		assert.Contains(t, alert.Message, "Critical Performance Degradation")

		// Verify notification delivery
		sentChannels := make(map[string]bool)
		for _, log := range result.NotificationsSent {
			sentChannels[log.ChannelID] = true
			assert.Equal(t, "sent", log.Status, "All notifications should be successfully sent")
		}
		assert.True(t, sentChannels["email-ops"], "Should send email notification")
		assert.True(t, sentChannels["slack-critical"], "Should send Slack notification")
		assert.True(t, sentChannels["pagerduty-oncall"], "Should send PagerDuty notification")

		t.Logf("✅ Complete performance monitoring workflow succeeded:")
		t.Logf("   📊 Regression detected: %t", result.RegressionDetected)
		t.Logf("   🚨 %d alerts generated", len(result.AlertsGenerated))
		t.Logf("   📤 %d notifications sent", len(result.NotificationsSent))
		t.Logf("   ⏱️  Processing completed in %v", result.ProcessingDuration)
	})

	t.Run("performance_dashboard_integration", func(t *testing.T) {
		// Setup mock data for dashboard
		activeAlert := &entities.PerformanceAlert{
			ID:        "test-alert-1",
			RuleID:    "critical_performance_degradation",
			RuleName:  "Critical Performance Degradation",
			Severity:  entities.SeverityCritical,
			Status:    entities.StatusActive,
			Message:   "Performance has degraded significantly",
			CreatedAt: time.Now().Add(-30 * time.Minute),
			UpdatedAt: time.Now().Add(-30 * time.Minute),
			Context: entities.AlertContext{
				TestScenario:     "api_performance_test",
				Metric:           "latency",
				CurrentValue:     525.0,
				BaselineValue:    295.0,
				PercentageChange: 78.0,
				ConfidenceLevel:  0.98,
				PValue:           0.001,
			},
		}

		alertRepo.On("ListAlertsByStatus", ctx, entities.StatusActive).Return([]*entities.PerformanceAlert{activeAlert}, nil)
		alertRepo.On("ListAlertsInTimeRange", ctx, mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).Return([]*entities.PerformanceAlert{activeAlert}, nil)

		// Test performance dashboard endpoint
		req := httptest.NewRequest("GET", "/api/v1/performance/dashboard?hours=24", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Dashboard endpoint should return 200")

		var dashboard handlers.PerformanceDashboardResponse
		err := json.Unmarshal(w.Body.Bytes(), &dashboard)
		require.NoError(t, err, "Should unmarshal dashboard response")

		// Verify dashboard structure
		assert.NotZero(t, dashboard.Overview.CurrentLatency, "Should have current latency data")
		assert.Equal(t, 1, dashboard.Overview.ActiveAlertsCount, "Should show 1 active alert")
		assert.NotEmpty(t, dashboard.Overview.PerformanceGrade, "Should calculate performance grade")
		assert.Less(t, dashboard.Overview.SLACompliance, 100.0, "SLA compliance should be affected by active alerts")

		// Verify trends data
		assert.NotEmpty(t, dashboard.Trends.LatencyTrend.DataPoints, "Should have latency trend data")
		assert.NotEmpty(t, dashboard.Trends.ErrorRateTrend.DataPoints, "Should have error rate trend data")

		// Verify alerts data
		assert.Len(t, dashboard.RecentAlerts, 1, "Should show recent alerts")
		assert.Equal(t, "test-alert-1", dashboard.RecentAlerts[0].ID)

		// Verify baselines data
		assert.NotEmpty(t, dashboard.Baselines, "Should have baseline data")
		assert.Contains(t, dashboard.Baselines, "api_test", "Should have api_test baseline")

		// Verify real-time status
		assert.True(t, dashboard.RealTimeStatus.MonitoringActive, "Monitoring should be active")
		assert.Equal(t, 1, dashboard.RealTimeStatus.AlertsInLastHour, "Should show alerts in last hour")

		t.Logf("✅ Performance dashboard integration successful:")
		t.Logf("   📈 SLA Compliance: %.1f%%", dashboard.Overview.SLACompliance)
		t.Logf("   🎯 Performance Grade: %s", dashboard.Overview.PerformanceGrade)
		t.Logf("   🚨 Active Alerts: %d", dashboard.Overview.ActiveAlertsCount)
		t.Logf("   📊 Baselines Available: %d", len(dashboard.Baselines))
	})

	t.Run("alert_management_endpoints", func(t *testing.T) {
		// Test performance alerts endpoint
		req := httptest.NewRequest("GET", "/api/v1/performance/alerts?status=active&limit=10", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Alerts endpoint should return 200")

		var alertsResponse map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &alertsResponse)
		require.NoError(t, err, "Should unmarshal alerts response")

		assert.Contains(t, alertsResponse, "alerts", "Should contain alerts field")
		assert.Contains(t, alertsResponse, "count", "Should contain count field")
		assert.Contains(t, alertsResponse, "limit", "Should contain limit field")

		// Test baselines endpoint
		req = httptest.NewRequest("GET", "/api/v1/performance/baselines", nil)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Baselines endpoint should return 200")

		var baselinesResponse map[string]interface{}
		err = json.Unmarshal(w.Body.Bytes(), &baselinesResponse)
		require.NoError(t, err, "Should unmarshal baselines response")

		assert.Contains(t, baselinesResponse, "baselines", "Should contain baselines field")
		assert.Contains(t, baselinesResponse, "count", "Should contain count field")

		t.Logf("✅ Alert management endpoints working correctly")
	})

	// Verify all mocks were called as expected
	regressionDetector.AssertExpectations(t)
	alertRepo.AssertExpectations(t)
	notificationService.AssertExpectations(t)
}

// TestPerformanceMonitoringWithGoodData tests that no alerts are generated for good performance
func TestPerformanceMonitoringWithGoodData(t *testing.T) {
	logger := zaptest.NewLogger(t)

	regressionDetector := &MockRegressionDetector{}
	alertRepo := &MockAlertRepository{}
	notificationService := &MockNotificationService{}
	configLoader := &MockConfigLoader{}

	integrationService := alerting.NewIntegrationService(
		regressionDetector,
		alertRepo,
		notificationService,
		configLoader,
		logger,
	)

	ctx := context.Background()

	// Create performance data showing good performance (no regression)
	baselineData := []entities.BenchmarkResult{
		createBenchmarkResult("good_performance_test", 300*time.Millisecond, 25.0, 0.1),
		createBenchmarkResult("good_performance_test", 285*time.Millisecond, 26.2, 0.08),
		createBenchmarkResult("good_performance_test", 295*time.Millisecond, 25.8, 0.12),
	}

	currentData := []entities.BenchmarkResult{
		createBenchmarkResult("good_performance_test", 290*time.Millisecond, 25.5, 0.09), // Similar performance
		createBenchmarkResult("good_performance_test", 305*time.Millisecond, 24.8, 0.11),
		createBenchmarkResult("good_performance_test", 285*time.Millisecond, 26.1, 0.08),
	}

	// Setup mock to return no regression
	noRegressionResult := &ports.RegressionAnalysis{
		HasRegression:   false,
		Severity:        entities.SeverityInfo,
		Method:          "statistical",
		ConfidenceLevel: 0.15, // Low confidence = no significant change
		PValue:          0.45,
		EffectSize:      0.1,
		Details: map[string]interface{}{
			"latency_change_percent": -2.1, // Slight improvement
			"no_significant_change":  true,
		},
		Recommendations: []string{
			"Performance is stable",
			"No action required",
		},
	}

	regressionDetector.On("DetectStatisticalRegression", ctx, baselineData, currentData, mock.AnythingOfType("entities.RegressionCondition")).Return(noRegressionResult, nil)

	// Note: No alert rules are queried when no regression is detected

	// Process the good performance data
	result, err := integrationService.ProcessPerformanceData(ctx, currentData, baselineData)

	require.NoError(t, err)
	require.NotNil(t, result)

	assert.False(t, result.RegressionDetected, "Should not detect regression with good performance")
	assert.Empty(t, result.AlertsGenerated, "Should not generate alerts for good performance")
	assert.Empty(t, result.NotificationsSent, "Should not send notifications for good performance")

	t.Logf("✅ Good performance correctly identified - no false alerts generated")

	regressionDetector.AssertExpectations(t)
	alertRepo.AssertExpectations(t)
}

// Helper functions for test data creation

func createBenchmarkResult(testName string, avgLatency time.Duration, throughputRPS float64, errorRatePercent float64) entities.BenchmarkResult {
	return entities.BenchmarkResult{
		TestName:         testName,
		Timestamp:        time.Now(),
		Duration:         time.Minute,
		AvgLatency:       avgLatency,
		P95Latency:       avgLatency + (avgLatency / 4),
		ThroughputRPS:    throughputRPS,
		ErrorRatePercent: errorRatePercent,
		TotalRequests:    int(throughputRPS * 60),
		SuccessfulReqs:   int(throughputRPS * 60 * (1 - errorRatePercent/100)),
		FailedReqs:       int(throughputRPS * 60 * (errorRatePercent / 100)),
	}
}

func createEmailChannel(id string) *entities.NotificationChannel {
	return &entities.NotificationChannel{
		ID:      id,
		Name:    "Operations Email",
		Type:    "email",
		Enabled: true,
		Config: map[string]interface{}{
			"smtp_server": "smtp.company.com",
			"from_email":  "alerts@company.com",
			"to_emails":   []string{"ops@company.com", "devops@company.com"},
		},
	}
}

func createSlackChannel(id string) *entities.NotificationChannel {
	return &entities.NotificationChannel{
		ID:      id,
		Name:    "Critical Alerts Slack",
		Type:    "slack",
		Enabled: true,
		Config: map[string]interface{}{
			"webhook_url": "https://hooks.slack.com/services/test",
			"channel":     "#critical-alerts",
			"username":    "Performance Monitor",
		},
	}
}

func createPagerDutyChannel(id string) *entities.NotificationChannel {
	return &entities.NotificationChannel{
		ID:      id,
		Name:    "On-Call PagerDuty",
		Type:    "pagerduty",
		Enabled: true,
		Config: map[string]interface{}{
			"routing_key": "test-routing-key",
			"severity":    "critical",
		},
	}
}

func createNotificationLog(id, channelID, channelType, status string) *entities.NotificationLog {
	return &entities.NotificationLog{
		ID:          id,
		ChannelID:   channelID,
		ChannelType: channelType,
		Status:      status,
		SentAt:      time.Now(),
	}
}
