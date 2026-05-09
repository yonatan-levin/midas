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
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/industry"
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
//
// CurrentPrice is set so handler-level tests can verify the
// market-price plumbing end-to-end: the valuation engine captures
// the live quote on the result struct, and the HTTP handler must
// copy it onto FairValueResponse so consumers don't need a second
// quote lookup to compute upside/downside.
func sampleValuationResult(ticker string) *entities.ValuationResult {
	return &entities.ValuationResult{
		Ticker:                ticker,
		WACC:                  0.092,
		GrowthRate:            0.045,
		TangibleValuePerShare: 24.73,
		DCFValuePerShare:      156.42,
		CurrentPrice:          190.25,
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
			// Foreign-private-issuer pin for the bulk endpoint — must produce
			// FOREIGN_PRIVATE_ISSUER_UNSUPPORTED, not INSUFFICIENT_DATA, in the
			// per-ticker BulkFailure record.
			name:         "foreign_private_issuer",
			ticker:       "TSM",
			err:          fmt.Errorf("ifrs-full taxonomy: %w", valuation.ErrForeignPrivateIssuer),
			wantCode:     "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED",
			wantContains: "Foreign private issuer", // post-Phase-B message identifies the case without the misleading "not yet supported" text.
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

	// Verify Content-Type is RFC 7807 compliant
	assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))

	var resp ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	// Standard RFC 7807 fields
	assert.Equal(t, "https://problems.midas.dev/INVALID_TICKER", resp.Type)
	assert.Equal(t, "Bad Request", resp.Title)
	assert.Equal(t, http.StatusBadRequest, resp.Status)
	assert.Equal(t, "Ticker format is wrong", resp.Detail)
	assert.Equal(t, "/api/v1/fair-value/TEST", resp.Instance)
	assert.Equal(t, "TEST", resp.Context["ticker"])

	// RFC 7807 extension fields (code, timestamp, method)
	assert.Equal(t, "INVALID_TICKER", resp.Code)
	assert.Equal(t, "GET", resp.Method)
	assert.NotEmpty(t, resp.Timestamp, "timestamp must be present")
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
				// CurrentPrice must round-trip from ValuationResult to the
				// HTTP response so consumers can compute the DCF/market
				// discount without a second quote lookup. Pinned to the
				// fixture value in sampleValuationResult.
				assert.InDelta(t, 190.25, resp.CurrentPrice, 1e-6)
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
			// Foreign-private-issuer pin: SEC returns ifrs-full taxonomy
			// instead of us-gaap (TSM, ASML, NVO, AZN, SAP, BABA, …). Must
			// produce a distinct 422 with code FOREIGN_PRIVATE_ISSUER_UNSUPPORTED
			// and MUST NOT be misclassified as INSUFFICIENT_DATA, otherwise the
			// handler error chain is checking the more-general sentinel first.
			name:   "foreign_private_issuer",
			ticker: "TSM",
			setupMock: func(m *mockValuationService) {
				m.On("CalculateValuation", mock.Anything, "TSM", (*valuation.ValuationOptions)(nil)).
					Return(nil, fmt.Errorf("SEC filing for TSM: %w", valuation.ErrForeignPrivateIssuer))
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "FOREIGN_PRIVATE_ISSUER_UNSUPPORTED", extractErrorCode(resp.Type))
				assert.Equal(t, http.StatusUnprocessableEntity, resp.Status)
				// Regression guard: must not fall through to the more-general
				// INSUFFICIENT_DATA branch even though FPI also wraps a 422
				// outcome conceptually.
				assert.NotEqual(t, "INSUFFICIENT_DATA", extractErrorCode(resp.Type),
					"FPI must not be misclassified as INSUFFICIENT_DATA")
				// Context payload should explicitly identify the taxonomy so the
				// user knows why we rejected (and so a future support article
				// can deep-link to the field).
				if assert.NotNil(t, resp.Context) {
					assert.Equal(t, "ifrs-full", resp.Context["taxonomy"])
					assert.Equal(t, "20-F", resp.Context["filing_type"])
					assert.Equal(t, "TSM", resp.Context["ticker"])
				}
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
		{
			name:       "override_beta_too_high",
			ticker:     "AAPL",
			query:      "override_beta=4.0",
			setupMock:  func(m *mockValuationService) { /* no call expected */ },
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_PARAMETER", extractErrorCode(resp.Type))
			},
		},
		{
			name:       "override_beta_negative",
			ticker:     "AAPL",
			query:      "override_beta=-0.5",
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_PARAMETER", extractErrorCode(resp.Type))
			},
		},
		{
			name:       "override_rf_too_high",
			ticker:     "AAPL",
			query:      "override_rf=0.25",
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_PARAMETER", extractErrorCode(resp.Type))
			},
		},
		{
			name:       "override_rf_negative",
			ticker:     "AAPL",
			query:      "override_rf=-0.01",
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_PARAMETER", extractErrorCode(resp.Type))
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
		{
			name:       "bulk_override_beta_too_high_400",
			body:       `{"tickers":["AAPL"],"override_beta":99.0}`,
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_PARAMETER", extractErrorCode(resp.Type))
			},
		},
		{
			name:       "bulk_override_beta_negative_400",
			body:       `{"tickers":["AAPL"],"override_beta":-1.0}`,
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_PARAMETER", extractErrorCode(resp.Type))
			},
		},
		{
			name:       "bulk_override_rf_too_high_400",
			body:       `{"tickers":["AAPL"],"override_rf":0.50}`,
			setupMock:  func(m *mockValuationService) {},
			wantStatus: http.StatusBadRequest,
			wantBody: func(t *testing.T, body []byte) {
				var resp ErrorResponse
				require.NoError(t, json.Unmarshal(body, &resp))
				assert.Equal(t, "INVALID_PARAMETER", extractErrorCode(resp.Type))
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
// E.g., "https://problems.midas.dev/INVALID_TICKER" -> "INVALID_TICKER"
func extractErrorCode(typeURI string) string {
	parts := strings.Split(typeURI, "/")
	if len(parts) == 0 {
		return typeURI
	}
	return parts[len(parts)-1]
}

// ---- Tests for Industry field on FairValueResponse (design spec 2026-04-23) ----
//
// These tests drive the additive Industry response-surface change. They assert
// the handler surfaces both the SIC-derived classification (used for valuation
// model selection) and the balance-sheet-heuristic classification (used by the
// datacleaner), with a Match flag when the two classifiers agree per the
// canonical SIC -> GICS mapping table in the spec.
//
// The mapping table is:
//   "TECH"   -> {"45"}
//   "MFG"    -> {"20", "45"}   // semiconductors/hardware are MFG by SIC, IT by GICS
//   "RETAIL" -> {"25"}
//   "UTIL"   -> {"55"}
//   "FINL"   -> {"40"}
//   "HEALTH" -> {"35"}

// industryResultFor builds a ValuationResult populated with the industry
// classification fields under test. All other fields come from
// sampleValuationResult so existing assertions keep working.
func industryResultFor(ticker, sicRaw, sic, heurCode, heurName string) *entities.ValuationResult {
	r := sampleValuationResult(ticker)
	r.SICCodeRaw = sicRaw
	r.IndustrySIC = sic
	r.IndustryHeuristicCode = heurCode
	r.IndustryHeuristicName = heurName
	return r
}

// TestFairValueResponse_Industry_BothPresent verifies both labels surface and
// Match=true when the SIC label ("TECH") cleanly maps to the heuristic GICS
// code ("45" — Information Technology).
func TestFairValueResponse_Industry_BothPresent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := new(mockValuationService)
	mockSvc.On("CalculateValuation", mock.Anything, "AAPL", (*valuation.ValuationOptions)(nil)).
		Return(industryResultFor("AAPL", "7372", "TECH", "45", "Information Technology"), nil)

	handler := newTestFairValueHandler(mockSvc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/AAPL", nil)
	c.Params = gin.Params{{Key: "ticker", Value: "AAPL"}}
	handler.GetFairValue(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp FairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Industry, "Industry field should be present")
	assert.Equal(t, "7372", resp.Industry.SICCode)
	assert.Equal(t, "TECH", resp.Industry.SIC)
	assert.Equal(t, "45", resp.Industry.HeuristicCode)
	assert.Equal(t, "Information Technology", resp.Industry.HeuristicName)
	assert.True(t, resp.Industry.Match, "TECH->45 is a canonical match")

	mockSvc.AssertExpectations(t)
}

// TestFairValueResponse_Industry_ClassifierMismatch verifies Match=false when
// the two classifiers disagree (SIC says manufacturing, heuristic says
// consumer discretionary). This is the drift-detection case.
func TestFairValueResponse_Industry_ClassifierMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := new(mockValuationService)
	mockSvc.On("CalculateValuation", mock.Anything, "AMD", (*valuation.ValuationOptions)(nil)).
		Return(industryResultFor("AMD", "3674", "MFG", "25", "Consumer Discretionary"), nil)

	handler := newTestFairValueHandler(mockSvc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/AMD", nil)
	c.Params = gin.Params{{Key: "ticker", Value: "AMD"}}
	handler.GetFairValue(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp FairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Industry)
	assert.Equal(t, "3674", resp.Industry.SICCode)
	assert.Equal(t, "MFG", resp.Industry.SIC)
	assert.Equal(t, "25", resp.Industry.HeuristicCode)
	assert.Equal(t, "Consumer Discretionary", resp.Industry.HeuristicName)
	assert.False(t, resp.Industry.Match, "MFG does not map to GICS 25")

	mockSvc.AssertExpectations(t)
}

// TestFairValueResponse_Industry_MissingSIC verifies that a missing SIC code
// (common for foreign private issuers) still surfaces the heuristic label,
// with Match=false (can't match one-sided data — conservative).
func TestFairValueResponse_Industry_MissingSIC(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := new(mockValuationService)
	mockSvc.On("CalculateValuation", mock.Anything, "FOO", (*valuation.ValuationOptions)(nil)).
		Return(industryResultFor("FOO", "", "", "45", "Information Technology"), nil)

	handler := newTestFairValueHandler(mockSvc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/FOO", nil)
	c.Params = gin.Params{{Key: "ticker", Value: "FOO"}}
	handler.GetFairValue(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp FairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Industry)
	assert.Empty(t, resp.Industry.SICCode, "SICCode omitted when SEC data lacks SIC")
	assert.Empty(t, resp.Industry.SIC, "SIC label omitted when no raw SIC is available")
	assert.Equal(t, "45", resp.Industry.HeuristicCode)
	assert.Equal(t, "Information Technology", resp.Industry.HeuristicName)
	assert.False(t, resp.Industry.Match, "one-sided classification cannot match")

	mockSvc.AssertExpectations(t)
}

// TestFairValueResponse_Industry_SemiHybrid verifies that the MFG -> {20, 45}
// hybrid mapping correctly reports Match=true for semiconductor/hardware
// manufacturers (SIC "MFG" + GICS "45" Information Technology).
func TestFairValueResponse_Industry_SemiHybrid(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := new(mockValuationService)
	mockSvc.On("CalculateValuation", mock.Anything, "AMD", (*valuation.ValuationOptions)(nil)).
		Return(industryResultFor("AMD", "3674", "MFG", "45", "Information Technology"), nil)

	handler := newTestFairValueHandler(mockSvc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/AMD", nil)
	c.Params = gin.Params{{Key: "ticker", Value: "AMD"}}
	handler.GetFairValue(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp FairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Industry)
	assert.Equal(t, "3674", resp.Industry.SICCode)
	assert.Equal(t, "MFG", resp.Industry.SIC)
	assert.Equal(t, "45", resp.Industry.HeuristicCode)
	assert.Equal(t, "Information Technology", resp.Industry.HeuristicName)
	assert.True(t, resp.Industry.Match,
		"MFG -> {20, 45} hybrid mapping: semiconductors are MFG by SIC, IT by GICS")

	mockSvc.AssertExpectations(t)
}

// TestFairValueResponse_Industry_RealClassifier drives the real
// IndustryClassifier (not a stub) with SIC codes that actually land in the
// production config and asserts Match=true against the heuristic GICS codes
// those profiles produce. This is the regression sentinel for:
//   - B-1 part 1: the "FINL" typo — the classifier emits "FIN", and a typo
//     in sicToGICS silently demoted every bank to Match=false.
//   - B-1 part 2: sub-industry labels like "TECH_SAAS" must normalize to
//     parent "TECH" before the map lookup, or software issuers silently
//     miss their canonical match.
//
// Stub-based tests that inject hand-picked label strings (TECH, MFG…) cannot
// catch either of these — by construction, the stub always matches the map.
// Only a test that goes through the real classifier exposes spec-vs-reality
// gaps.
func TestFairValueResponse_Industry_RealClassifier(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Real IndustryClassifier with the production industry_codes.json loaded
	// explicitly — NewIndustryClassifier's default path is relative to cwd,
	// which differs across packages. Mirror the pattern from
	// internal/services/datacleaner/industry/classifier_classify_test.go.
	classifier := industry.NewIndustryClassifier()
	configPaths := []string{
		"../../../../config/datacleaner/industry_codes.json",
		"./config/datacleaner/industry_codes.json",
	}
	var loaded bool
	for _, p := range configPaths {
		if err := classifier.LoadIndustryCodesConfig(p); err == nil {
			loaded = true
			break
		}
	}
	require.True(t, loaded, "industry_codes.json must load for this integration test")

	tests := []struct {
		name             string
		ticker           string
		sicCode          string
		companyName      string // optional — feeds into keyword match for sub-industry refinement
		heurCode         string
		heurName         string
		expectMatch      bool
		acceptableLabels []string // sicLabel set accepted (parent + any sub-industry refinement)
		matchExplanation string
	}{
		{
			name:             "semiconductor_MFG_to_GICS_45",
			ticker:           "AMD",
			sicCode:          "3674", // matches MFG_SEMI sub-industry under MFG (RM-2 P1)
			heurCode:         "45",
			heurName:         "Information Technology",
			expectMatch:      true,
			acceptableLabels: []string{"MFG", "MFG_SEMI"}, // accept either parent or the new sub-industry
			matchExplanation: "semiconductor: SIC MFG/MFG_SEMI + GICS 45 is a canonical hybrid match (sub normalizes to MFG parent for match)",
		},
		{
			name:             "commercial_bank_FIN_to_GICS_40",
			ticker:           "JPM",
			sicCode:          "6020", // matches FIN_BANK sub-industry under FIN (RM-2 P1)
			heurCode:         "40",
			heurName:         "Financials",
			expectMatch:      true,
			acceptableLabels: []string{"FIN", "FIN_BANK"}, // accept parent or sub-industry post-RM-2 P1
			matchExplanation: "commercial bank: SIC FIN/FIN_BANK + GICS 40; regression sentinel for B-1 FINL/FIN typo (sub normalizes to FIN parent)",
		},
		{
			name:             "prepackaged_software_TECH_to_GICS_45",
			ticker:           "MSFT",
			sicCode:          "7372", // explicit TECH entry; SIC-only path → parent "TECH"
			heurCode:         "45",
			heurName:         "Information Technology",
			expectMatch:      true,
			acceptableLabels: []string{"TECH", "TECH_SAAS"}, // either parent or sub-industry is valid
			matchExplanation: "software parent: SIC TECH + GICS 45",
		},
		{
			name:             "saas_subindustry_normalizes_to_TECH_parent",
			ticker:           "CRM",
			sicCode:          "7372",            // TECH parent
			companyName:      "Salesforce SaaS", // triggers TECH_SAAS sub-industry refinement
			heurCode:         "45",
			heurName:         "Information Technology",
			expectMatch:      true,
			acceptableLabels: []string{"TECH_SAAS"}, // must be the sub-industry code
			matchExplanation: "SaaS: SIC TECH_SAAS must normalize to TECH parent for GICS 45 match; regression sentinel for sub-industry normalization",
		},
		// VAL-3 P1+P4 REIT subsector regression sentinels (Stream-A V/R/Q
		// MEDIUM #1). The sicToGICS table got new full-code exact-match
		// entries — RETAIL_REIT, DATA_CENTER, CELLTOWER, HEALTHCARE_REIT,
		// OFFICE, RESIDENTIAL, INDUSTRIAL, SPECIALTY — all of which must
		// resolve to GICS 60 (Real Estate). Without explicit entries the
		// fallback parent-prefix strip would route RETAIL_REIT → RETAIL →
		// {25, 30}, DATA_CENTER → DATA → unknown, and CELLTOWER (no
		// underscore) → CELLTOWER → unknown — silently demoting every REIT
		// in those subsectors to match=false. These three cases pin the
		// exact-match-first lookup ordering enforced by matchSICToGICS.
		{
			name:             "retail_reit_RETAIL_REIT_to_GICS_60",
			ticker:           "SPG",
			sicCode:          "6798",                 // RESTATE parent
			companyName:      "Simon Property Group", // keyword "simon property" routes to RETAIL_REIT sub
			heurCode:         "60",
			heurName:         "Real Estate",
			expectMatch:      true,
			acceptableLabels: []string{"RETAIL_REIT"}, // sub-industry must be the produced label
			matchExplanation: "mall REIT: SIC RETAIL_REIT must full-code match GICS 60 (NOT collapse to RETAIL → {25, 30})",
		},
		{
			name:             "data_center_reit_DATA_CENTER_to_GICS_60",
			ticker:           "EQIX",
			sicCode:          "6798",         // RESTATE parent
			companyName:      "Equinix Inc.", // keyword "equinix" routes to DATA_CENTER sub
			heurCode:         "60",
			heurName:         "Real Estate",
			expectMatch:      true,
			acceptableLabels: []string{"DATA_CENTER"},
			matchExplanation: "data center REIT: SIC DATA_CENTER must full-code match GICS 60 (parent strip would yield DATA, unmapped)",
		},
		{
			name:    "cell_tower_reit_CELLTOWER_to_GICS_60",
			ticker:  "AMT",
			sicCode: "6798", // RESTATE parent
			// Exact name matches RESTATE parent's exact_names list AND the
			// CELLTOWER sub-industry's "american tower" keyword.
			companyName:      "American Tower Corporation",
			heurCode:         "60",
			heurName:         "Real Estate",
			expectMatch:      true,
			acceptableLabels: []string{"CELLTOWER"},
			matchExplanation: "cell tower REIT: SIC CELLTOWER (no underscore — pure subsector code) must have an explicit sicToGICS entry to resolve to 60",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Drive the real classifier with optional company name to exercise
			// the sub-industry refinement path.
			// ctx param added in Phase M (observability); harmless in tests.
			// Classify now returns ClassificationResult (M-1b); .Industry is
			// the most-specific code (parent or sub-industry).
			classifyResult, classifyErr := classifier.Classify(context.Background(), tc.sicCode, "", tc.companyName)
			require.NoError(t, classifyErr)
			sicLabel := classifyResult.Industry
			assert.Contains(t, tc.acceptableLabels, sicLabel,
				"real classifier produced label %q; acceptable set %v",
				sicLabel, tc.acceptableLabels)

			// Build the mock valuation result using the label the classifier
			// actually produced, not a hand-picked string. This is what makes
			// the test a true integration check for the handler↔classifier
			// contract.
			mockSvc := new(mockValuationService)
			mockSvc.On("CalculateValuation", mock.Anything, tc.ticker, (*valuation.ValuationOptions)(nil)).
				Return(industryResultFor(tc.ticker, tc.sicCode, sicLabel, tc.heurCode, tc.heurName), nil)

			handler := newTestFairValueHandler(mockSvc)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/"+tc.ticker, nil)
			c.Params = gin.Params{{Key: "ticker", Value: tc.ticker}}
			handler.GetFairValue(c)

			assert.Equal(t, http.StatusOK, w.Code)

			var resp FairValueResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.NotNil(t, resp.Industry)
			assert.Equal(t, tc.sicCode, resp.Industry.SICCode)
			assert.Equal(t, sicLabel, resp.Industry.SIC)
			assert.Equal(t, tc.heurCode, resp.Industry.HeuristicCode)
			assert.Equal(t, tc.heurName, resp.Industry.HeuristicName)
			assert.Equal(t, tc.expectMatch, resp.Industry.Match, tc.matchExplanation)

			mockSvc.AssertExpectations(t)
		})
	}
}

// TestFairValueResponse_Industry_REITSubsector_NegativeGICS pins the
// reverse-direction invariant for VAL-3 P1+P4 REIT subsectors: a REIT
// subsector code (RETAIL_REIT) paired with a non-Real-Estate GICS sector
// (25 = Consumer Discretionary) MUST yield Match=false.
//
// Without the explicit "RETAIL_REIT": {"60": true} entry in sicToGICS, the
// matchSICToGICS fallback would strip at the first underscore to "RETAIL",
// look up sicToGICS["RETAIL"] = {"25", "30"}, and return Match=true — the
// exact silent-demotion class the MEDIUM #1 finding flagged: every mall REIT
// labelled GICS 25 by the heuristic would falsely register as a "match"
// just because the SIC label happens to start with RETAIL_.
//
// The bug here is asymmetric: with the fix, RETAIL_REIT exact-match wins
// against GICS 60 (correct positive) and against GICS 25 (correct negative).
// Without the fix, the parent-strip fallback fires on both, breaking only
// the negative case while leaving the positive looking-correct-by-accident.
// Hence this test is the load-bearing regression sentinel — the positive
// counterpart in TestFairValueResponse_Industry_RealClassifier alone is
// not sufficient because RETAIL → {25, 30} also includes 30, and a tester
// who skipped this case might assume positive coverage equals correctness.
func TestFairValueResponse_Industry_REITSubsector_NegativeGICS(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := new(mockValuationService)
	// Mismatched pair: SIC subsector code RETAIL_REIT (Real Estate) vs
	// heuristic GICS 25 (Consumer Discretionary). The two classifiers
	// genuinely disagree — drift signal must surface as Match=false.
	mockSvc.On("CalculateValuation", mock.Anything, "SPG", (*valuation.ValuationOptions)(nil)).
		Return(industryResultFor("SPG", "6798", "RETAIL_REIT", "25", "Consumer Discretionary"), nil)

	handler := newTestFairValueHandler(mockSvc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/fair-value/SPG", nil)
	c.Params = gin.Params{{Key: "ticker", Value: "SPG"}}
	handler.GetFairValue(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp FairValueResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Industry)
	assert.Equal(t, "RETAIL_REIT", resp.Industry.SIC)
	assert.Equal(t, "25", resp.Industry.HeuristicCode)
	assert.False(t, resp.Industry.Match,
		"RETAIL_REIT must NOT match GICS 25; if this fails, the parent-strip "+
			"fallback is firing before the exact-match lookup and silently "+
			"demoting every Real-Estate-labelled REIT whose heuristic drifts "+
			"to Consumer Discretionary into a false-positive match (the exact "+
			"regression class MEDIUM #1 was filed to prevent)")

	mockSvc.AssertExpectations(t)
}

// TestBuildIndustryFromResult_NilResult verifies the helper returns nil for a
// nil ValuationResult — the handler relies on this so `omitempty` drops the
// field entirely when the engine produced no classification signal.
func TestBuildIndustryFromResult_NilResult(t *testing.T) {
	assert.Nil(t, BuildIndustryFromResult(nil))
}

// TestBuildIndustryFromResult_AllFieldsEmpty verifies the helper returns nil
// when a ValuationResult has no classification data populated at all.
// Prevents an empty `{match: false}` object from leaking into responses.
func TestBuildIndustryFromResult_AllFieldsEmpty(t *testing.T) {
	assert.Nil(t, BuildIndustryFromResult(&entities.ValuationResult{}))
}

// TestFairValueResponse_GrahamDiscountPct_OmitEmpty pins the JSON-marshal
// behaviour of the *float64 + omitempty pair on graham_discount_pct. The
// pointer is the only thing distinguishing "floor==0, ratio undefined"
// (key absent) from "price exactly equals floor" (key present, value 0).
// Plain float64 + omitempty would silently drop the &0.0 case.
func TestFairValueResponse_GrahamDiscountPct_OmitEmpty(t *testing.T) {
	zero := 0.0
	positive := 23.30

	tests := []struct {
		name         string
		discount     *float64
		wantKey      bool   // graham_discount_pct present in JSON?
		wantContains string // expected substring in marshalled JSON
	}{
		{name: "nil pointer omits the key", discount: nil, wantKey: false},
		{name: "&0.0 keeps the key with value 0", discount: &zero, wantKey: true, wantContains: `"graham_discount_pct":0`},
		{name: "&23.30 keeps the key with the positive value", discount: &positive, wantKey: true, wantContains: `"graham_discount_pct":23.3`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := FairValueResponse{
				Ticker:            "X",
				GrahamDiscountPct: tt.discount,
			}
			b, err := json.Marshal(r)
			require.NoError(t, err)
			j := string(b)

			if tt.wantKey {
				assert.Contains(t, j, "graham_discount_pct", "key should be present")
				if tt.wantContains != "" {
					assert.Contains(t, j, tt.wantContains)
				}
			} else {
				assert.NotContains(t, j, "graham_discount_pct", "key should be omitted")
			}
		})
	}
}

// TestFairValueResponse_GrahamFloorFields_OmitEmpty verifies that all four
// pointer fields drop from the JSON when nil (the unresolved-fallback shape
// per spec F-5), matching the "all four omit when total_liabilities can't be
// resolved" contract.
func TestFairValueResponse_GrahamFloorFields_OmitEmpty(t *testing.T) {
	r := FairValueResponse{Ticker: "X"} // all graham fields default-nil
	b, err := json.Marshal(r)
	require.NoError(t, err)
	j := string(b)

	for _, key := range []string{
		"current_assets_per_share",
		"ncav_per_share",
		"graham_floor_per_share",
		"graham_discount_pct",
	} {
		assert.NotContains(t, j, key, "expected %s to be omitted on nil default", key)
	}
}

// TestFairValueResponse_GrahamFloorFields_DeepDistress pins the contract that
// distinguishes the deep-distress shape (resolved + negative NCAV → floor
// clamped to 0) from the unresolved-fallback shape (all four absent). With
// pointer + omitempty, &0.0 stays in JSON; nil drops. A regression to plain
// float64 + omitempty would silently collapse these two semantically
// different states into the same wire output.
func TestFairValueResponse_GrahamFloorFields_DeepDistress(t *testing.T) {
	caps := 2.85
	ncav := -0.765
	floor := 0.0
	r := FairValueResponse{
		Ticker:                "MXL",
		CurrentAssetsPerShare: &caps,
		NCAVPerShare:          &ncav,
		GrahamFloorPerShare:   &floor,
		GrahamDiscountPct:     nil, // floor==0 → discount nil
	}
	b, err := json.Marshal(r)
	require.NoError(t, err)
	j := string(b)

	assert.Contains(t, j, `"current_assets_per_share":2.85`)
	assert.Contains(t, j, `"ncav_per_share":-0.765`)
	assert.Contains(t, j, `"graham_floor_per_share":0`, "&0.0 must stay in JSON, not be dropped by omitempty")
	assert.NotContains(t, j, "graham_discount_pct", "discount must be absent when floor==0")
}
