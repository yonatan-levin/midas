package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/services/alerting"
)

// PerformanceHandler handles performance monitoring and analytics endpoints
type PerformanceHandler struct {
	// logger is retained for non-request contexts; request-path log sites use logctx.From(ctx)
	logger             *zap.Logger
	integrationService *alerting.IntegrationService
	alertRepo          ports.AlertRepository
	metricsService     ports.MetricsService
}

// NewPerformanceHandler creates a new performance handler
func NewPerformanceHandler(
	logger *zap.Logger,
	integrationService *alerting.IntegrationService,
	alertRepo ports.AlertRepository,
	metricsService ports.MetricsService,
) *PerformanceHandler {
	return &PerformanceHandler{
		logger:             logger,
		integrationService: integrationService,
		alertRepo:          alertRepo,
		metricsService:     metricsService,
	}
}

// PerformanceDashboardResponse represents the complete performance dashboard data
type PerformanceDashboardResponse struct {
	Overview       PerformanceOverview        `json:"overview"`
	Trends         PerformanceTrends          `json:"trends"`
	RecentAlerts   []AlertSummary             `json:"recent_alerts"`
	Baselines      map[string]BaselineSummary `json:"baselines"`
	RealTimeStatus RealTimeStatus             `json:"real_time_status"`
	Timestamp      time.Time                  `json:"timestamp"`
}

// PerformanceOverview provides high-level performance statistics
type PerformanceOverview struct {
	CurrentLatency     float64 `json:"current_latency_ms"`
	CurrentThroughput  float64 `json:"current_throughput_rps"`
	CurrentErrorRate   float64 `json:"current_error_rate_percent"`
	SLACompliance      float64 `json:"sla_compliance_percent"`
	ActiveAlertsCount  int     `json:"active_alerts_count"`
	RegressionDetected bool    `json:"regression_detected"`
	PerformanceGrade   string  `json:"performance_grade"` // A, B, C, D, F
}

// PerformanceTrends provides trend analysis over time
type PerformanceTrends struct {
	LatencyTrend      TrendData `json:"latency_trend"`
	ThroughputTrend   TrendData `json:"throughput_trend"`
	ErrorRateTrend    TrendData `json:"error_rate_trend"`
	AvailabilityTrend TrendData `json:"availability_trend"`
}

// TrendData represents trend information for a specific metric
type TrendData struct {
	Direction  string      `json:"direction"`   // improving, degrading, stable
	Strength   float64     `json:"strength"`    // 0-1 scale
	ChangeRate float64     `json:"change_rate"` // percentage change per period
	Confidence float64     `json:"confidence"`  // statistical confidence
	DataPoints []DataPoint `json:"data_points"`
	Forecast   []DataPoint `json:"forecast,omitempty"`
}

// DataPoint represents a single data point in a trend
type DataPoint struct {
	Timestamp  time.Time `json:"timestamp"`
	Value      float64   `json:"value"`
	SampleSize int       `json:"sample_size,omitempty"`
}

// AlertSummary provides a summary of alert information
type AlertSummary struct {
	ID           string                 `json:"id"`
	RuleName     string                 `json:"rule_name"`
	Severity     entities.AlertSeverity `json:"severity"`
	Status       entities.AlertStatus   `json:"status"`
	Message      string                 `json:"message"`
	CreatedAt    time.Time              `json:"created_at"`
	TestScenario string                 `json:"test_scenario"`
}

// BaselineSummary provides information about performance baselines
type BaselineSummary struct {
	Scenario      string    `json:"scenario"`
	LastUpdated   time.Time `json:"last_updated"`
	SampleCount   int       `json:"sample_count"`
	AvgLatency    float64   `json:"avg_latency_ms"`
	AvgThroughput float64   `json:"avg_throughput_rps"`
	AvgErrorRate  float64   `json:"avg_error_rate_percent"`
	Quality       string    `json:"quality"` // excellent, good, acceptable, poor
}

// RealTimeStatus provides real-time monitoring status
type RealTimeStatus struct {
	MonitoringActive  bool      `json:"monitoring_active"`
	LastDataReceived  time.Time `json:"last_data_received"`
	ProcessingLatency float64   `json:"processing_latency_ms"`
	QueueDepth        int       `json:"queue_depth"`
	AlertsInLastHour  int       `json:"alerts_in_last_hour"`
}

// GetPerformanceDashboard handles GET /api/v1/performance/dashboard
func (h *PerformanceHandler) GetPerformanceDashboard(c *gin.Context) {
	ctx := c.Request.Context()

	// Get query parameters for time range
	hoursParam := c.DefaultQuery("hours", "24")
	hours, err := strconv.Atoi(hoursParam)
	if err != nil || hours < 1 || hours > 168 { // Max 1 week
		hours = 24
	}

	timeRange := time.Duration(hours) * time.Hour
	startTime := time.Now().Add(-timeRange)

	logctx.From(c.Request.Context()).Info("Generating performance dashboard",
		zap.Int("hours", hours),
		zap.Time("start_time", startTime),
	)

	// Build dashboard response
	dashboard := PerformanceDashboardResponse{
		Timestamp: time.Now(),
	}

	// Get performance overview
	overview, err := h.buildPerformanceOverview(ctx)
	if err != nil {
		logctx.From(c.Request.Context()).Error("Failed to build performance overview", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate performance overview"})
		return
	}
	dashboard.Overview = overview

	// Get performance trends (simplified for now)
	trends, err := h.buildPerformanceTrends(ctx, startTime)
	if err != nil {
		logctx.From(c.Request.Context()).Error("Failed to build performance trends", zap.Error(err))
		// Continue with empty trends rather than failing
		trends = PerformanceTrends{}
	}
	dashboard.Trends = trends

	// Get recent alerts
	alerts, err := h.getRecentAlerts(ctx, startTime)
	if err != nil {
		logctx.From(c.Request.Context()).Error("Failed to get recent alerts", zap.Error(err))
		alerts = []AlertSummary{}
	}
	dashboard.RecentAlerts = alerts

	// Get baseline summaries
	baselines := h.getBaselineSummaries(ctx)
	dashboard.Baselines = baselines

	// Get real-time status
	realTimeStatus := h.getRealTimeStatus(ctx)
	dashboard.RealTimeStatus = realTimeStatus

	c.JSON(http.StatusOK, dashboard)
}

// buildPerformanceOverview creates the performance overview section
func (h *PerformanceHandler) buildPerformanceOverview(ctx context.Context) (PerformanceOverview, error) {
	overview := PerformanceOverview{}

	// Get current metrics from metrics service
	overview.CurrentLatency = h.metricsService.GetAverageResponseTime()
	overview.CurrentErrorRate = h.metricsService.GetErrorRate()

	// Calculate throughput (simplified)
	totalRequests := h.metricsService.GetTotalRequests()
	totalValuations := h.metricsService.GetTotalValuations()
	if totalRequests > 0 {
		// Approximate throughput based on recent activity
		overview.CurrentThroughput = float64(totalValuations) / 3600 // Rough RPS approximation
	}

	// Get active alerts count
	activeAlerts, err := h.alertRepo.ListAlertsByStatus(ctx, entities.StatusActive)
	if err != nil {
		logctx.From(ctx).Warn("Failed to get active alerts", zap.Error(err))
		overview.ActiveAlertsCount = 0
	} else {
		overview.ActiveAlertsCount = len(activeAlerts)
	}

	// Calculate SLA compliance (≤500ms latency, ≤1% error rate, ≥10 RPS baseline)
	slaCompliance := 100.0
	if overview.CurrentLatency > 500 {
		slaCompliance -= 40 // Major penalty for latency SLA breach
	}
	if overview.CurrentErrorRate > 1.0 {
		slaCompliance -= 30 // Major penalty for error rate SLA breach
	}
	if overview.CurrentThroughput < 10.0 {
		slaCompliance -= 20 // Penalty for low throughput
	}
	if overview.ActiveAlertsCount > 0 {
		slaCompliance -= float64(overview.ActiveAlertsCount) * 5 // 5% penalty per active alert
	}
	if slaCompliance < 0 {
		slaCompliance = 0
	}
	overview.SLACompliance = slaCompliance

	// Determine regression status (simplified)
	overview.RegressionDetected = overview.ActiveAlertsCount > 0

	// Calculate performance grade
	overview.PerformanceGrade = calculatePerformanceGrade(slaCompliance)

	return overview, nil
}

// buildPerformanceTrends creates trend analysis (simplified implementation)
func (h *PerformanceHandler) buildPerformanceTrends(ctx context.Context, startTime time.Time) (PerformanceTrends, error) {
	// TODO: Implement comprehensive trend analysis
	// For now, return placeholder trends based on current metrics

	currentLatency := h.metricsService.GetAverageResponseTime()
	currentErrorRate := h.metricsService.GetErrorRate()

	now := time.Now()

	// Create simplified trend data with some sample points
	trends := PerformanceTrends{
		LatencyTrend: TrendData{
			Direction:  "stable",
			Strength:   0.3,
			ChangeRate: 0.0,
			Confidence: 0.85,
			DataPoints: []DataPoint{
				{Timestamp: startTime, Value: currentLatency * 0.95},
				{Timestamp: startTime.Add(time.Hour * 6), Value: currentLatency * 0.98},
				{Timestamp: startTime.Add(time.Hour * 12), Value: currentLatency * 1.02},
				{Timestamp: startTime.Add(time.Hour * 18), Value: currentLatency * 0.99},
				{Timestamp: now, Value: currentLatency},
			},
		},
		ErrorRateTrend: TrendData{
			Direction:  "improving",
			Strength:   0.6,
			ChangeRate: -0.1,
			Confidence: 0.92,
			DataPoints: []DataPoint{
				{Timestamp: startTime, Value: currentErrorRate * 1.2},
				{Timestamp: startTime.Add(time.Hour * 6), Value: currentErrorRate * 1.1},
				{Timestamp: startTime.Add(time.Hour * 12), Value: currentErrorRate * 1.05},
				{Timestamp: startTime.Add(time.Hour * 18), Value: currentErrorRate * 1.02},
				{Timestamp: now, Value: currentErrorRate},
			},
		},
		ThroughputTrend: TrendData{
			Direction:  "stable",
			Strength:   0.4,
			ChangeRate: 0.05,
			Confidence: 0.78,
			DataPoints: []DataPoint{
				{Timestamp: startTime, Value: 22.5},
				{Timestamp: startTime.Add(time.Hour * 6), Value: 24.1},
				{Timestamp: startTime.Add(time.Hour * 12), Value: 23.8},
				{Timestamp: startTime.Add(time.Hour * 18), Value: 25.2},
				{Timestamp: now, Value: 24.7},
			},
		},
		AvailabilityTrend: TrendData{
			Direction:  "stable",
			Strength:   0.2,
			ChangeRate: 0.0,
			Confidence: 0.99,
			DataPoints: []DataPoint{
				{Timestamp: startTime, Value: 99.9},
				{Timestamp: startTime.Add(time.Hour * 6), Value: 99.95},
				{Timestamp: startTime.Add(time.Hour * 12), Value: 99.92},
				{Timestamp: startTime.Add(time.Hour * 18), Value: 99.98},
				{Timestamp: now, Value: 99.96},
			},
		},
	}

	return trends, nil
}

// getRecentAlerts gets recent alerts for the dashboard
func (h *PerformanceHandler) getRecentAlerts(ctx context.Context, startTime time.Time) ([]AlertSummary, error) {
	alerts, err := h.alertRepo.ListAlertsInTimeRange(ctx, startTime, time.Now())
	if err != nil {
		return nil, err
	}

	var summaries []AlertSummary
	for _, alert := range alerts {
		summary := AlertSummary{
			ID:           alert.ID,
			RuleName:     alert.RuleName,
			Severity:     alert.Severity,
			Status:       alert.Status,
			Message:      alert.Message,
			CreatedAt:    alert.CreatedAt,
			TestScenario: alert.Context.TestScenario,
		}
		summaries = append(summaries, summary)
	}

	return summaries, nil
}

// getBaselineSummaries gets summaries of all available baselines
func (h *PerformanceHandler) getBaselineSummaries(ctx context.Context) map[string]BaselineSummary {
	// TODO: Implement proper baseline retrieval from storage
	// For now, return placeholder baselines

	return map[string]BaselineSummary{
		"api_test": {
			Scenario:      "api_test",
			LastUpdated:   time.Now().Add(-2 * time.Hour),
			SampleCount:   25,
			AvgLatency:    285.5,
			AvgThroughput: 24.8,
			AvgErrorRate:  0.12,
			Quality:       "excellent",
		},
		"load_test": {
			Scenario:      "load_test",
			LastUpdated:   time.Now().Add(-6 * time.Hour),
			SampleCount:   15,
			AvgLatency:    420.2,
			AvgThroughput: 18.5,
			AvgErrorRate:  0.28,
			Quality:       "good",
		},
	}
}

// getRealTimeStatus gets the current real-time monitoring status
func (h *PerformanceHandler) getRealTimeStatus(ctx context.Context) RealTimeStatus {
	// Get alerts from the last hour
	oneHourAgo := time.Now().Add(-time.Hour)
	recentAlerts, err := h.alertRepo.ListAlertsInTimeRange(ctx, oneHourAgo, time.Now())
	alertsInLastHour := 0
	if err == nil {
		alertsInLastHour = len(recentAlerts)
	}

	return RealTimeStatus{
		MonitoringActive:  true,                              // TODO: Get actual status from integration service
		LastDataReceived:  time.Now().Add(-30 * time.Second), // TODO: Get actual timestamp
		ProcessingLatency: 25.3,                              // TODO: Get actual processing latency
		QueueDepth:        0,                                 // TODO: Get actual queue depth
		AlertsInLastHour:  alertsInLastHour,
	}
}

// calculatePerformanceGrade calculates a letter grade based on SLA compliance
func calculatePerformanceGrade(slaCompliance float64) string {
	switch {
	case slaCompliance >= 95:
		return "A"
	case slaCompliance >= 85:
		return "B"
	case slaCompliance >= 75:
		return "C"
	case slaCompliance >= 65:
		return "D"
	default:
		return "F"
	}
}

// GetPerformanceAlerts handles GET /api/v1/performance/alerts
func (h *PerformanceHandler) GetPerformanceAlerts(c *gin.Context) {
	ctx := c.Request.Context()

	// Get query parameters
	status := c.Query("status")
	limitParam := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitParam)
	if err != nil || limit < 1 || limit > 1000 {
		limit = 50
	}

	var alerts []*entities.PerformanceAlert

	if status != "" {
		// Parse status
		alertStatus := entities.AlertStatus(status)
		alerts, err = h.alertRepo.ListAlertsByStatus(ctx, alertStatus)
	} else {
		// Get all active alerts by default
		alerts, err = h.alertRepo.ListAlertsByStatus(ctx, entities.StatusActive)
	}

	if err != nil {
		logctx.From(c.Request.Context()).Error("Failed to get performance alerts", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve alerts"})
		return
	}

	// Limit results
	if len(alerts) > limit {
		alerts = alerts[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"alerts": alerts,
		"count":  len(alerts),
		"limit":  limit,
	})
}

// GetPerformanceBaselines handles GET /api/v1/performance/baselines
func (h *PerformanceHandler) GetPerformanceBaselines(c *gin.Context) {
	ctx := c.Request.Context()
	scenario := c.Query("scenario")

	baselines := h.getBaselineSummaries(ctx)

	if scenario != "" {
		if baseline, exists := baselines[scenario]; exists {
			c.JSON(http.StatusOK, baseline)
		} else {
			c.JSON(http.StatusNotFound, gin.H{"error": "Baseline not found"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"baselines": baselines,
		"count":     len(baselines),
	})
}
