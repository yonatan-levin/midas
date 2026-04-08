package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// ---- Mocks for PerformanceHandler dependencies ----

// mockAlertRepository implements ports.AlertRepository for testing.
type mockAlertRepository struct {
	mock.Mock
}

func (m *mockAlertRepository) CreateAlert(ctx context.Context, alert *entities.PerformanceAlert) error {
	args := m.Called(ctx, alert)
	return args.Error(0)
}

func (m *mockAlertRepository) GetAlert(ctx context.Context, id string) (*entities.PerformanceAlert, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.PerformanceAlert), args.Error(1)
}

func (m *mockAlertRepository) UpdateAlert(ctx context.Context, alert *entities.PerformanceAlert) error {
	args := m.Called(ctx, alert)
	return args.Error(0)
}

func (m *mockAlertRepository) DeleteAlert(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *mockAlertRepository) ListActiveAlerts(ctx context.Context) ([]*entities.PerformanceAlert, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.PerformanceAlert), args.Error(1)
}

func (m *mockAlertRepository) ListAlertsByRule(ctx context.Context, ruleID string) ([]*entities.PerformanceAlert, error) {
	args := m.Called(ctx, ruleID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.PerformanceAlert), args.Error(1)
}

func (m *mockAlertRepository) ListAlertsByStatus(ctx context.Context, status entities.AlertStatus) ([]*entities.PerformanceAlert, error) {
	args := m.Called(ctx, status)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.PerformanceAlert), args.Error(1)
}

func (m *mockAlertRepository) ListAlertsInTimeRange(ctx context.Context, start, end time.Time) ([]*entities.PerformanceAlert, error) {
	args := m.Called(ctx, start, end)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.PerformanceAlert), args.Error(1)
}

func (m *mockAlertRepository) CreateAlertRule(ctx context.Context, rule *entities.AlertRule) error {
	args := m.Called(ctx, rule)
	return args.Error(0)
}

func (m *mockAlertRepository) GetAlertRule(ctx context.Context, id string) (*entities.AlertRule, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.AlertRule), args.Error(1)
}

func (m *mockAlertRepository) UpdateAlertRule(ctx context.Context, rule *entities.AlertRule) error {
	args := m.Called(ctx, rule)
	return args.Error(0)
}

func (m *mockAlertRepository) DeleteAlertRule(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *mockAlertRepository) ListAlertRules(ctx context.Context, enabled bool) ([]*entities.AlertRule, error) {
	args := m.Called(ctx, enabled)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.AlertRule), args.Error(1)
}

func (m *mockAlertRepository) CreateNotificationChannel(ctx context.Context, channel *entities.NotificationChannel) error {
	args := m.Called(ctx, channel)
	return args.Error(0)
}

func (m *mockAlertRepository) GetNotificationChannel(ctx context.Context, id string) (*entities.NotificationChannel, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.NotificationChannel), args.Error(1)
}

func (m *mockAlertRepository) UpdateNotificationChannel(ctx context.Context, channel *entities.NotificationChannel) error {
	args := m.Called(ctx, channel)
	return args.Error(0)
}

func (m *mockAlertRepository) DeleteNotificationChannel(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *mockAlertRepository) ListNotificationChannels(ctx context.Context, enabled bool) ([]*entities.NotificationChannel, error) {
	args := m.Called(ctx, enabled)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.NotificationChannel), args.Error(1)
}

func (m *mockAlertRepository) SaveTrendAnalysis(ctx context.Context, analysis *entities.TrendAnalysis) error {
	args := m.Called(ctx, analysis)
	return args.Error(0)
}

func (m *mockAlertRepository) GetTrendAnalysis(ctx context.Context, id string) (*entities.TrendAnalysis, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.TrendAnalysis), args.Error(1)
}

func (m *mockAlertRepository) ListTrendAnalyses(ctx context.Context, scenario, metric string, limit int) ([]*entities.TrendAnalysis, error) {
	args := m.Called(ctx, scenario, metric, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.TrendAnalysis), args.Error(1)
}

func (m *mockAlertRepository) SaveAnomalyDetectionResult(ctx context.Context, result *entities.AnomalyDetectionResult) error {
	args := m.Called(ctx, result)
	return args.Error(0)
}

func (m *mockAlertRepository) GetAnomalyDetectionResult(ctx context.Context, id string) (*entities.AnomalyDetectionResult, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.AnomalyDetectionResult), args.Error(1)
}

func (m *mockAlertRepository) ListAnomalies(ctx context.Context, scenario, metric string, limit int) ([]*entities.AnomalyDetectionResult, error) {
	args := m.Called(ctx, scenario, metric, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.AnomalyDetectionResult), args.Error(1)
}

// mockMetricsService implements ports.MetricsService for testing.
type mockMetricsService struct {
	mock.Mock
}

func (m *mockMetricsService) RecordHTTPRequest(method, endpoint string, statusCode int, duration time.Duration, responseSize int) {
}
func (m *mockMetricsService) IncHTTPRequestsInFlight() {}
func (m *mockMetricsService) DecHTTPRequestsInFlight() {}
func (m *mockMetricsService) RecordValuationRequest(ticker, reqType, status string, duration time.Duration) {
}
func (m *mockMetricsService) RecordValuationError(ticker, errorType string)                 {}
func (m *mockMetricsService) IncDCFCalculations()                                           {}
func (m *mockMetricsService) IncWACCCalculations()                                          {}
func (m *mockMetricsService) RecordSECAPIRequest(endpoint, status string)                   {}
func (m *mockMetricsService) RecordMarketAPIRequest(provider, status string)                {}
func (m *mockMetricsService) RecordMacroAPIRequest(provider, status string)                 {}
func (m *mockMetricsService) RecordDataFetch(source, ticker string, duration time.Duration) {}
func (m *mockMetricsService) RecordCacheRequest(cacheType, operation, result string)        {}
func (m *mockMetricsService) SetCacheHitRatio(cacheType string, ratio float64)              {}
func (m *mockMetricsService) SetAverageWACC(wacc float64)                                   {}
func (m *mockMetricsService) SetAverageGrowthRate(rate float64)                             {}
func (m *mockMetricsService) HealthCheck() error                                            { return nil }

func (m *mockMetricsService) GetTotalRequests() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *mockMetricsService) GetActiveConnections() int {
	args := m.Called()
	return args.Get(0).(int)
}

func (m *mockMetricsService) GetAverageResponseTime() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *mockMetricsService) GetErrorRate() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *mockMetricsService) GetCacheHitRate() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *mockMetricsService) GetTotalValuations() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *mockMetricsService) GetSuccessfulValuations() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *mockMetricsService) GetFailedValuations() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

func (m *mockMetricsService) GetAverageWACC() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *mockMetricsService) GetAverageGrowthRate() float64 {
	args := m.Called()
	return args.Get(0).(float64)
}

func (m *mockMetricsService) GetUniqueTickersServed() int64 {
	args := m.Called()
	return args.Get(0).(int64)
}

// ---- Helper for PerformanceHandler construction ----

// setupDashboardMetrics configures only the metrics getters called by
// GetPerformanceDashboard (buildPerformanceOverview + buildPerformanceTrends).
// TotalValuations is set high enough (>36000) so throughput = totalValuations/3600 > 10 RPS,
// avoiding a throughput SLA penalty and yielding an overall "A" grade.
func setupDashboardMetrics(m *mockMetricsService) {
	m.On("GetAverageResponseTime").Return(120.5)
	m.On("GetErrorRate").Return(0.5)
	m.On("GetTotalRequests").Return(int64(50000))
	m.On("GetTotalValuations").Return(int64(50000))
}

// newTestPerformanceHandler creates a PerformanceHandler with mock dependencies.
func newTestPerformanceHandler(alertRepo *mockAlertRepository, metricsSvc *mockMetricsService) *PerformanceHandler {
	return &PerformanceHandler{
		logger:             zap.NewNop(),
		integrationService: nil, // Not used by the GET endpoints under test
		alertRepo:          alertRepo,
		metricsService:     metricsSvc,
	}
}

// ---- Tests for calculatePerformanceGrade (pure function) ----

func Test_calculatePerformanceGrade(t *testing.T) {
	tests := []struct {
		name          string
		slaCompliance float64
		wantGrade     string
	}{
		{name: "A_grade_100", slaCompliance: 100, wantGrade: "A"},
		{name: "A_grade_95", slaCompliance: 95, wantGrade: "A"},
		{name: "B_grade_90", slaCompliance: 90, wantGrade: "B"},
		{name: "B_grade_85", slaCompliance: 85, wantGrade: "B"},
		{name: "C_grade_80", slaCompliance: 80, wantGrade: "C"},
		{name: "C_grade_75", slaCompliance: 75, wantGrade: "C"},
		{name: "D_grade_70", slaCompliance: 70, wantGrade: "D"},
		{name: "D_grade_65", slaCompliance: 65, wantGrade: "D"},
		{name: "F_grade_60", slaCompliance: 60, wantGrade: "F"},
		{name: "F_grade_0", slaCompliance: 0, wantGrade: "F"},
		{name: "boundary_94.9", slaCompliance: 94.9, wantGrade: "B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculatePerformanceGrade(tt.slaCompliance)
			assert.Equal(t, tt.wantGrade, got)
		})
	}
}

// ---- Tests for GetPerformanceDashboard ----

func TestPerformanceHandler_GetPerformanceDashboard(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		query      string
		setupMocks func(ar *mockAlertRepository, ms *mockMetricsService)
		wantStatus int
		wantBody   func(t *testing.T, body []byte)
	}{
		{
			name:  "success_default_hours",
			query: "",
			setupMocks: func(ar *mockAlertRepository, ms *mockMetricsService) {
				setupDashboardMetrics(ms)
				// buildPerformanceOverview calls ListAlertsByStatus for active alerts
				ar.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
					Return([]*entities.PerformanceAlert{}, nil)
				// getRecentAlerts calls ListAlertsInTimeRange
				ar.On("ListAlertsInTimeRange", mock.Anything, mock.Anything, mock.Anything).
					Return([]*entities.PerformanceAlert{}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp PerformanceDashboardResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.NotZero(t, resp.Timestamp)
				assert.Equal(t, "A", resp.Overview.PerformanceGrade)
				assert.InDelta(t, 120.5, resp.Overview.CurrentLatency, 1e-6)
				assert.InDelta(t, 0.5, resp.Overview.CurrentErrorRate, 1e-6)
				assert.NotNil(t, resp.Baselines)
				assert.True(t, resp.RealTimeStatus.MonitoringActive)
			},
		},
		{
			name:  "success_custom_hours",
			query: "hours=48",
			setupMocks: func(ar *mockAlertRepository, ms *mockMetricsService) {
				setupDashboardMetrics(ms)
				ar.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
					Return([]*entities.PerformanceAlert{}, nil)
				ar.On("ListAlertsInTimeRange", mock.Anything, mock.Anything, mock.Anything).
					Return([]*entities.PerformanceAlert{}, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name:  "success_with_active_alerts",
			query: "",
			setupMocks: func(ar *mockAlertRepository, ms *mockMetricsService) {
				setupDashboardMetrics(ms)
				activeAlerts := []*entities.PerformanceAlert{
					{
						ID:        "alert-1",
						RuleName:  "latency_threshold",
						Severity:  entities.SeverityWarning,
						Status:    entities.StatusActive,
						Message:   "High latency detected",
						CreatedAt: time.Now().Add(-30 * time.Minute),
						Context:   entities.AlertContext{TestScenario: "api_test"},
					},
				}
				ar.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
					Return(activeAlerts, nil)
				ar.On("ListAlertsInTimeRange", mock.Anything, mock.Anything, mock.Anything).
					Return(activeAlerts, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp PerformanceDashboardResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, 1, resp.Overview.ActiveAlertsCount)
				assert.True(t, resp.Overview.RegressionDetected)
				assert.Len(t, resp.RecentAlerts, 1)
				assert.Equal(t, "latency_threshold", resp.RecentAlerts[0].RuleName)
			},
		},
		{
			name:  "invalid_hours_param_uses_default",
			query: "hours=abc",
			setupMocks: func(ar *mockAlertRepository, ms *mockMetricsService) {
				setupDashboardMetrics(ms)
				ar.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
					Return([]*entities.PerformanceAlert{}, nil)
				ar.On("ListAlertsInTimeRange", mock.Anything, mock.Anything, mock.Anything).
					Return([]*entities.PerformanceAlert{}, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name:  "hours_exceeding_max_clamped",
			query: "hours=999",
			setupMocks: func(ar *mockAlertRepository, ms *mockMetricsService) {
				setupDashboardMetrics(ms)
				ar.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
					Return([]*entities.PerformanceAlert{}, nil)
				ar.On("ListAlertsInTimeRange", mock.Anything, mock.Anything, mock.Anything).
					Return([]*entities.PerformanceAlert{}, nil)
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alertRepo := new(mockAlertRepository)
			metricsSvc := new(mockMetricsService)
			tt.setupMocks(alertRepo, metricsSvc)
			handler := newTestPerformanceHandler(alertRepo, metricsSvc)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			url := "/api/v1/performance/dashboard"
			if tt.query != "" {
				url += "?" + tt.query
			}
			c.Request = httptest.NewRequest("GET", url, nil)

			handler.GetPerformanceDashboard(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantBody != nil {
				tt.wantBody(t, w.Body.Bytes())
			}

			alertRepo.AssertExpectations(t)
			metricsSvc.AssertExpectations(t)
		})
	}
}

// ---- Tests for GetPerformanceAlerts ----

func TestPerformanceHandler_GetPerformanceAlerts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		query      string
		setupMocks func(ar *mockAlertRepository)
		wantStatus int
		wantBody   func(t *testing.T, body []byte)
	}{
		{
			name:  "default_returns_active_alerts",
			query: "",
			setupMocks: func(ar *mockAlertRepository) {
				ar.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
					Return([]*entities.PerformanceAlert{
						{ID: "a1", RuleName: "rule1", Status: entities.StatusActive},
						{ID: "a2", RuleName: "rule2", Status: entities.StatusActive},
					}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				alerts := resp["alerts"].([]interface{})
				assert.Len(t, alerts, 2)
				assert.Equal(t, float64(2), resp["count"])
				assert.Equal(t, float64(50), resp["limit"])
			},
		},
		{
			name:  "filter_by_status",
			query: "status=resolved",
			setupMocks: func(ar *mockAlertRepository) {
				ar.On("ListAlertsByStatus", mock.Anything, entities.AlertStatus("resolved")).
					Return([]*entities.PerformanceAlert{}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, float64(0), resp["count"])
			},
		},
		{
			name:  "custom_limit",
			query: "limit=5",
			setupMocks: func(ar *mockAlertRepository) {
				ar.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
					Return([]*entities.PerformanceAlert{}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, float64(5), resp["limit"])
			},
		},
		{
			name:  "invalid_limit_uses_default",
			query: "limit=abc",
			setupMocks: func(ar *mockAlertRepository) {
				ar.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
					Return([]*entities.PerformanceAlert{}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, float64(50), resp["limit"])
			},
		},
		{
			name:  "repository_error",
			query: "",
			setupMocks: func(ar *mockAlertRepository) {
				ar.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
					Return(nil, errors.New("database error"))
			},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alertRepo := new(mockAlertRepository)
			metricsSvc := new(mockMetricsService)
			tt.setupMocks(alertRepo)
			handler := newTestPerformanceHandler(alertRepo, metricsSvc)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			url := "/api/v1/performance/alerts"
			if tt.query != "" {
				url += "?" + tt.query
			}
			c.Request = httptest.NewRequest("GET", url, nil)

			handler.GetPerformanceAlerts(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantBody != nil {
				tt.wantBody(t, w.Body.Bytes())
			}

			alertRepo.AssertExpectations(t)
		})
	}
}

// ---- Tests for GetPerformanceBaselines ----

func TestPerformanceHandler_GetPerformanceBaselines(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantBody   func(t *testing.T, body []byte)
	}{
		{
			name:       "all_baselines",
			query:      "",
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, float64(2), resp["count"])
				baselines := resp["baselines"].(map[string]interface{})
				assert.Contains(t, baselines, "api_test")
				assert.Contains(t, baselines, "load_test")
			},
		},
		{
			name:       "specific_scenario_found",
			query:      "scenario=api_test",
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp BaselineSummary
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "api_test", resp.Scenario)
				assert.Equal(t, "excellent", resp.Quality)
				assert.InDelta(t, 285.5, resp.AvgLatency, 1e-6)
			},
		},
		{
			name:       "specific_scenario_not_found",
			query:      "scenario=nonexistent",
			wantStatus: http.StatusNotFound,
			wantBody: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Contains(t, resp["error"], "not found")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alertRepo := new(mockAlertRepository)
			metricsSvc := new(mockMetricsService)
			handler := newTestPerformanceHandler(alertRepo, metricsSvc)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			url := "/api/v1/performance/baselines"
			if tt.query != "" {
				url += "?" + tt.query
			}
			c.Request = httptest.NewRequest("GET", url, nil)

			handler.GetPerformanceBaselines(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantBody != nil {
				tt.wantBody(t, w.Body.Bytes())
			}
		})
	}
}

// TestNewPerformanceHandler verifies the constructor wires all dependencies correctly.
func TestNewPerformanceHandler(t *testing.T) {
	logger := zap.NewNop()
	alertRepo := new(mockAlertRepository)
	metricsSvc := new(mockMetricsService)

	handler := NewPerformanceHandler(logger, nil, alertRepo, metricsSvc)
	require.NotNil(t, handler)
	assert.Equal(t, logger, handler.logger)
}

// TestPerformanceHandler_GetPerformanceDashboard_OverviewAlertRepoError verifies
// the dashboard still renders when the alert repo returns an error for active alerts.
func TestPerformanceHandler_GetPerformanceDashboard_OverviewAlertRepoError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	alertRepo := new(mockAlertRepository)
	metricsSvc := new(mockMetricsService)
	setupDashboardMetrics(metricsSvc)

	// Alert repo fails for active alerts — handler should still succeed with 0 active alerts
	alertRepo.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
		Return(nil, errors.New("alert repo error"))
	alertRepo.On("ListAlertsInTimeRange", mock.Anything, mock.Anything, mock.Anything).
		Return([]*entities.PerformanceAlert{}, nil)

	handler := newTestPerformanceHandler(alertRepo, metricsSvc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/performance/dashboard", nil)

	handler.GetPerformanceDashboard(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp PerformanceDashboardResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Overview.ActiveAlertsCount)
}

// TestPerformanceHandler_GetPerformanceDashboard_RecentAlertsError verifies
// the dashboard continues rendering when getRecentAlerts fails.
func TestPerformanceHandler_GetPerformanceDashboard_RecentAlertsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	alertRepo := new(mockAlertRepository)
	metricsSvc := new(mockMetricsService)
	setupDashboardMetrics(metricsSvc)

	alertRepo.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
		Return([]*entities.PerformanceAlert{}, nil)
	// Recent alerts fails — handler continues with empty array
	alertRepo.On("ListAlertsInTimeRange", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("time range query error"))

	handler := newTestPerformanceHandler(alertRepo, metricsSvc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/performance/dashboard", nil)

	handler.GetPerformanceDashboard(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp PerformanceDashboardResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.RecentAlerts)
}

// TestPerformanceHandler_GetPerformanceDashboard_SLAPenalties verifies the SLA
// compliance calculation: high latency, high error rate, and active alerts all
// deduct from the 100% baseline.
func TestPerformanceHandler_GetPerformanceDashboard_SLAPenalties(t *testing.T) {
	gin.SetMode(gin.TestMode)

	alertRepo := new(mockAlertRepository)
	metricsSvc := new(mockMetricsService)

	// Simulate bad metrics: high latency, high error rate, low throughput
	metricsSvc.On("GetAverageResponseTime").Return(600.0) // > 500ms threshold
	metricsSvc.On("GetErrorRate").Return(2.0)             // > 1% threshold
	metricsSvc.On("GetTotalRequests").Return(int64(100))
	metricsSvc.On("GetTotalValuations").Return(int64(5)) // throughput = 5/3600 < 10 RPS

	// 2 active alerts = -10% additional
	activeAlerts := []*entities.PerformanceAlert{
		{ID: "a1", Status: entities.StatusActive},
		{ID: "a2", Status: entities.StatusActive},
	}
	alertRepo.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
		Return(activeAlerts, nil)
	alertRepo.On("ListAlertsInTimeRange", mock.Anything, mock.Anything, mock.Anything).
		Return(activeAlerts, nil)

	handler := newTestPerformanceHandler(alertRepo, metricsSvc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/performance/dashboard", nil)

	handler.GetPerformanceDashboard(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp PerformanceDashboardResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// SLA = 100 - 40 (latency) - 30 (error rate) - 20 (throughput) - 10 (2 alerts) = 0
	assert.InDelta(t, 0, resp.Overview.SLACompliance, 1e-6)
	assert.Equal(t, "F", resp.Overview.PerformanceGrade)
	assert.True(t, resp.Overview.RegressionDetected)
}

// TestPerformanceHandler_GetPerformanceAlerts_LimitTruncation verifies that
// results exceeding the limit parameter are truncated.
func TestPerformanceHandler_GetPerformanceAlerts_LimitTruncation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	alertRepo := new(mockAlertRepository)
	metricsSvc := new(mockMetricsService)

	// Return 5 alerts but limit to 2
	alerts := make([]*entities.PerformanceAlert, 5)
	for i := range alerts {
		alerts[i] = &entities.PerformanceAlert{ID: "a" + string(rune('0'+i)), Status: entities.StatusActive}
	}
	alertRepo.On("ListAlertsByStatus", mock.Anything, entities.StatusActive).
		Return(alerts, nil)

	handler := newTestPerformanceHandler(alertRepo, metricsSvc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/performance/alerts?limit=2", nil)

	handler.GetPerformanceAlerts(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(2), resp["count"])
	assert.Equal(t, float64(2), resp["limit"])
}
