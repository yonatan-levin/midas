package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// ---- Mock for ValuationCalculator interface ----

// mockValuationService implements ValuationCalculator for unit testing.
type mockValuationService struct {
	mock.Mock
}

func (m *mockValuationService) CalculateValuation(
	ctx context.Context,
	ticker string,
	opts *valuation.ValuationOptions,
) (*entities.ValuationResult, error) {
	args := m.Called(ctx, ticker, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.ValuationResult), args.Error(1)
}

// ---- Helpers ----

// newTestFairValueHandler creates a FairValueHandler wired to the given mock.
func newTestFairValueHandler(svc *mockValuationService) *FairValueHandler {
	return NewFairValueHandler(svc, zap.NewNop())
}

// sampleValuationResult returns a realistic ValuationResult for AAPL.
func sampleValuationResult(ticker string) *entities.ValuationResult {
	return &entities.ValuationResult{
		Ticker:                ticker,
		WACC:                  0.092,
		GrowthRate:            0.045,
		TangibleValuePerShare: 24.73,
		DCFValuePerShare:      156.42,
		CalculatedAt:          time.Date(2025, 8, 13, 22, 15, 34, 0, time.UTC),
		DataQualityScore:      85.5,
		DataQualityGrade:      "B",
	}
}

// ---- Tests for isValidTicker (pure function) ----

func Test_isValidTicker(t *testing.T) {
	tests := []struct {
		name   string
		ticker string
		want   bool
	}{
		// Valid tickers
		{name: "single_char", ticker: "A", want: true},
		{name: "two_chars", ticker: "GE", want: true},
		{name: "three_chars", ticker: "IBM", want: true},
		{name: "four_chars", ticker: "AAPL", want: true},
		{name: "five_chars", ticker: "GOOGL", want: true},
		{name: "digit_prefix_ticker", ticker: "3M", want: true}, // digits and uppercase letters are both valid
		{name: "all_digits", ticker: "12345", want: true},
		{name: "mixed_alpha_digit", ticker: "A1B2C", want: true},

		// Invalid tickers
		{name: "empty_string", ticker: "", want: false},
		{name: "too_long", ticker: "TOOLON", want: false},
		{name: "lowercase", ticker: "aapl", want: false},
		{name: "mixed_case", ticker: "Aapl", want: false},
		{name: "contains_hyphen", ticker: "BRK-A", want: false},
		{name: "contains_dot", ticker: "BRK.A", want: false},
		{name: "contains_space", ticker: "AA L", want: false},
		{name: "special_chars", ticker: "A@PL", want: false},
		{name: "unicode", ticker: "A\u00e4PL", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidTicker(tt.ticker)
			assert.Equal(t, tt.want, got, "isValidTicker(%q)", tt.ticker)
		})
	}
}

// ---- Tests for parseFloatParam ----

func Test_parseFloatParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		query   string // full query string, e.g. "override_beta=1.2"
		param   string // param name to parse
		wantNil bool
		wantVal float64
	}{
		{name: "present_valid", query: "override_beta=1.2", param: "override_beta", wantNil: false, wantVal: 1.2},
		{name: "present_zero", query: "override_beta=0", param: "override_beta", wantNil: false, wantVal: 0},
		{name: "present_negative", query: "override_rf=-0.01", param: "override_rf", wantNil: false, wantVal: -0.01},
		{name: "absent_param", query: "", param: "override_beta", wantNil: true},
		{name: "invalid_value", query: "override_beta=abc", param: "override_beta", wantNil: true},
		{name: "empty_value", query: "override_beta=", param: "override_beta", wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			url := "/test"
			if tt.query != "" {
				url += "?" + tt.query
			}
			c.Request = httptest.NewRequest("GET", url, nil)

			result := parseFloatParam(c, tt.param)
			if tt.wantNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.InDelta(t, tt.wantVal, *result, 1e-9)
			}
		})
	}
}

// ---- Tests for classifyBulkError ----

func Test_classifyBulkError(t *testing.T) {
	tests := []struct {
		name         string
		ticker       string
		err          error
		wantCode     string
		wantContains string // substring in Message
	}{
		{
			name:         "ticker_not_found",
			ticker:       "XYZ",
			err:          fmt.Errorf("lookup failed: %w", valuation.ErrTickerNotFound),
			wantCode:     "TICKER_NOT_FOUND",
			wantContains: "not found",
		},
		{
			name:         "insufficient_data",
			ticker:       "TSLA",
			err:          valuation.ErrInsufficientData,
			wantCode:     "INSUFFICIENT_DATA",
			wantContains: "Not enough",
		},
		{
			name:         "model_not_applicable",
			ticker:       "RIVN",
			err:          fmt.Errorf("negative OI: %w", valuation.ErrModelNotApplicable),
			wantCode:     "MODEL_NOT_APPLICABLE",
			wantContains: "DCF",
		},
		{
			name:         "generic_error",
			ticker:       "ERR",
			err:          errors.New("unexpected failure"),
			wantCode:     "CALCULATION_ERROR",
			wantContains: "failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failure := classifyBulkError(tt.ticker, tt.err)
			assert.Equal(t, tt.ticker, failure.Ticker)
			assert.Equal(t, tt.wantCode, failure.ErrorCode)
			assert.Contains(t, failure.Message, tt.wantContains)
		})
	}
}

// ---- Tests for sendError ----

func TestFairValueHandler_sendError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := newTestFairValueHandler(&mockValuationService{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/TEST", nil)

	handler.sendError(c, http.StatusBadRequest, "INVALID_TICKER",
		"Bad Request", "Ticker format is wrong",
		map[string]interface{}{"ticker": "TEST"})

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "https://api.dcf-valuation.com/errors/INVALID_TICKER", resp.Type)
	assert.Equal(t, "Bad Request", resp.Title)
	assert.Equal(t, http.StatusBadRequest, resp.Status)
	assert.Equal(t, "Ticker format is wrong", resp.Detail)
	assert.Equal(t, "/api/v1/fair-value/TEST", resp.Instance)
	assert.Equal(t, "TEST", resp.Context["ticker"])
}

// ---- Tests for GetFairValue ----

func TestFairValueHandler_GetFairValue(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		ticker     string // path param
		query      string // query string
		setupMock  func(m *mockValuationService)
		wantStatus int
		wantBody   func(t *testing.T, body []byte) // assertion on response body
	}{
		{
			name:   "success_valid_ticker",
			ticker: "AAPL",
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
					Return(sampleValuationResult("AAPL"), nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp FairValueResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "AAPL", resp.Ticker)
				assert.InDelta(t, 0.092, resp.WACC, 1e-6)
				assert.InDelta(t, 0.045, resp.GrowthRate, 1e-6)
				assert.InDelta(t, 24.73, resp.TangibleValuePerShare, 1e-6)
				assert.InDelta(t, 156.42, resp.DCFValuePerShare, 1e-6)
				assert.NotEmpty(t, resp.AsOf)
				assert.InDelta(t, 85.5, resp.DataQualityScore, 1e-6)
				assert.Equal(t, "B", resp.DataQualityGrade)
			},
		},
		{
			name:   "success_lowercase_ticker_uppercased",
			ticker: "msft",
			setupMock: func(m *mockValuationService) {
				// Handler uppercases the ticker before calling the service
				m.On("CalculateValuation", mock.Anything, "MSFT", (*valuation.ValuationOptions)(nil)).
					Return(sampleValuationResult("MSFT"), nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp FairValueResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "MSFT", resp.Ticker)
			},
		},
		{
			name:       "invalid_ticker_empty",
			ticker:     "",
			setupMock:  func(m *mockValuationService) { /* no call expected */ },
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_TICKER", extractErrorCode(resp.Type))
			},
		},
		{
			name:       "invalid_ticker_too_long",
			ticker:     "TOOLONG",
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_TICKER", extractErrorCode(resp.Type))
			},
		},
		{
			name:       "invalid_ticker_special_chars",
			ticker:     "BR-A",
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:   "ticker_not_found",
			ticker: "ZZZZ",
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "ZZZZ", (*valuation.ValuationOptions)(nil)).
					Return(nil, fmt.Errorf("lookup: %w", valuation.ErrTickerNotFound))
			},
			wantStatus: http.StatusNotFound,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "TICKER_NOT_FOUND", extractErrorCode(resp.Type))
				assert.Equal(t, http.StatusNotFound, resp.Status)
			},
		},
		{
			name:   "insufficient_data",
			ticker: "SNAP",
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "SNAP", (*valuation.ValuationOptions)(nil)).
					Return(nil, valuation.ErrInsufficientData)
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INSUFFICIENT_DATA", extractErrorCode(resp.Type))
			},
		},
		{
			name:   "model_not_applicable",
			ticker: "RIVN",
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "RIVN", (*valuation.ValuationOptions)(nil)).
					Return(nil, fmt.Errorf("negative OI: %w", valuation.ErrModelNotApplicable))
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "MODEL_NOT_APPLICABLE", extractErrorCode(resp.Type))
			},
		},
		{
			name:   "internal_server_error",
			ticker: "BOOM",
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "BOOM", (*valuation.ValuationOptions)(nil)).
					Return(nil, errors.New("database connection lost"))
			},
			wantStatus: http.StatusInternalServerError,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "CALCULATION_ERROR", extractErrorCode(resp.Type))
			},
		},
		{
			name:   "with_override_beta",
			ticker: "AAPL",
			query:  "override_beta=1.5",
			setupMock: func(m *mockValuationService) {
				// The handler should pass overrides when query params are present
				m.On("CalculateValuation", mock.Anything, "AAPL", mock.MatchedBy(func(opts *valuation.ValuationOptions) bool {
					return opts != nil && opts.OverrideBeta != nil && *opts.OverrideBeta == 1.5 && opts.OverrideRiskFree == nil
				})).Return(sampleValuationResult("AAPL"), nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name:   "with_override_risk_free",
			ticker: "AAPL",
			query:  "override_rf=0.045",
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "AAPL", mock.MatchedBy(func(opts *valuation.ValuationOptions) bool {
					return opts != nil && opts.OverrideRiskFree != nil && *opts.OverrideRiskFree == 0.045 && opts.OverrideBeta == nil
				})).Return(sampleValuationResult("AAPL"), nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name:   "with_both_overrides",
			ticker: "AAPL",
			query:  "override_beta=1.3&override_rf=0.05",
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "AAPL", mock.MatchedBy(func(opts *valuation.ValuationOptions) bool {
					return opts != nil &&
						opts.OverrideBeta != nil && *opts.OverrideBeta == 1.3 &&
						opts.OverrideRiskFree != nil && *opts.OverrideRiskFree == 0.05
				})).Return(sampleValuationResult("AAPL"), nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name:   "with_invalid_override_ignored",
			ticker: "AAPL",
			query:  "override_beta=notanumber",
			setupMock: func(m *mockValuationService) {
				// Invalid override is ignored (parseFloatParam returns nil), so opts == nil
				m.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
					Return(sampleValuationResult("AAPL"), nil)
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := new(mockValuationService)
			tt.setupMock(mockSvc)
			handler := newTestFairValueHandler(mockSvc)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			url := "/api/v1/fair-value/" + tt.ticker
			if tt.query != "" {
				url += "?" + tt.query
			}
			c.Request = httptest.NewRequest("GET", url, nil)
			c.Params = gin.Params{{Key: "ticker", Value: tt.ticker}}

			handler.GetFairValue(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantBody != nil {
				tt.wantBody(t, w.Body.Bytes())
			}

			mockSvc.AssertExpectations(t)
		})
	}
}

// ---- Tests for GetBulkFairValue ----

func TestFairValueHandler_GetBulkFairValue(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       string // raw JSON body
		setupMock  func(m *mockValuationService)
		wantStatus int
		wantBody   func(t *testing.T, body []byte)
	}{
		{
			name: "all_tickers_succeed_200",
			body: `{"tickers":["AAPL","MSFT"]}`,
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
					Return(sampleValuationResult("AAPL"), nil)
				m.On("CalculateValuation", mock.Anything, "MSFT", (*valuation.ValuationOptions)(nil)).
					Return(sampleValuationResult("MSFT"), nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp BulkFairValueResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Len(t, resp.Results, 2)
				assert.Empty(t, resp.Failures)
				assert.Equal(t, 2, resp.Summary.TotalRequested)
				assert.Equal(t, 2, resp.Summary.Successful)
				assert.Equal(t, 0, resp.Summary.Failed)
			},
		},
		{
			name: "partial_success_207",
			body: `{"tickers":["AAPL","ZZZZ"]}`,
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
					Return(sampleValuationResult("AAPL"), nil)
				m.On("CalculateValuation", mock.Anything, "ZZZZ", (*valuation.ValuationOptions)(nil)).
					Return(nil, valuation.ErrTickerNotFound)
			},
			wantStatus: http.StatusMultiStatus, // 207
			wantBody: func(t *testing.T, body []byte) {
				var resp BulkFairValueResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Len(t, resp.Results, 1)
				assert.Len(t, resp.Failures, 1)
				assert.Equal(t, "ZZZZ", resp.Failures[0].Ticker)
				assert.Equal(t, "TICKER_NOT_FOUND", resp.Failures[0].ErrorCode)
				assert.Equal(t, 1, resp.Summary.Successful)
				assert.Equal(t, 1, resp.Summary.Failed)
			},
		},
		{
			name: "all_fail_422",
			body: `{"tickers":["SNAP","RIVN"]}`,
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "SNAP", (*valuation.ValuationOptions)(nil)).
					Return(nil, valuation.ErrInsufficientData)
				m.On("CalculateValuation", mock.Anything, "RIVN", (*valuation.ValuationOptions)(nil)).
					Return(nil, valuation.ErrModelNotApplicable)
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody: func(t *testing.T, body []byte) {
				var resp BulkFairValueResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Empty(t, resp.Results)
				assert.Len(t, resp.Failures, 2)
				assert.Equal(t, 0, resp.Summary.Successful)
				assert.Equal(t, 2, resp.Summary.Failed)
			},
		},
		{
			name:       "invalid_json_400",
			body:       `{not valid json}`,
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_REQUEST", extractErrorCode(resp.Type))
			},
		},
		{
			name:       "missing_tickers_field_400",
			body:       `{}`,
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty_tickers_array_400",
			body:       `{"tickers":[]}`,
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "invalid_ticker_in_bulk_skipped",
			body: `{"tickers":["AAPL","!!!"]}`,
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
					Return(sampleValuationResult("AAPL"), nil)
			},
			wantStatus: http.StatusMultiStatus,
			wantBody: func(t *testing.T, body []byte) {
				var resp BulkFairValueResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Len(t, resp.Results, 1)
				assert.Len(t, resp.Failures, 1)
				assert.Equal(t, "INVALID_TICKER", resp.Failures[0].ErrorCode)
			},
		},
		{
			name: "with_override_params",
			body: `{"tickers":["AAPL"],"override_beta":1.5,"override_rf":0.04}`,
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "AAPL", mock.MatchedBy(func(opts *valuation.ValuationOptions) bool {
					return opts != nil &&
						opts.OverrideBeta != nil && *opts.OverrideBeta == 1.5 &&
						opts.OverrideRiskFree != nil && *opts.OverrideRiskFree == 0.04
				})).Return(sampleValuationResult("AAPL"), nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "lowercase_tickers_uppercased",
			body: `{"tickers":["aapl"]}`,
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
					Return(sampleValuationResult("AAPL"), nil)
			},
			wantStatus: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				var resp BulkFairValueResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "AAPL", resp.Results[0].Ticker)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockSvc := new(mockValuationService)
			tt.setupMock(mockSvc)
			handler := newTestFairValueHandler(mockSvc)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/v1/fair-value/bulk", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			handler.GetBulkFairValue(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantBody != nil {
				tt.wantBody(t, w.Body.Bytes())
			}

			mockSvc.AssertExpectations(t)
		})
	}
}

// extractErrorCode extracts the trailing segment from an error type URI.
// E.g., "https://api.dcf-valuation.com/errors/INVALID_TICKER" -> "INVALID_TICKER"
func extractErrorCode(typeURI string) string {
	parts := strings.Split(typeURI, "/")
	if len(parts) == 0 {
		return typeURI
	}
	return parts[len(parts)-1]
}
