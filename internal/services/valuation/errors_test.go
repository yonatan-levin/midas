package valuation

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// TestHasCompanyFactsNotFoundError guards the classification rule that lets
// the valuation service return ErrInsufficientData (→ HTTP 422) for foreign
// private issuers with no SEC XBRL facts, instead of masking them as
// ErrTickerNotFound (→ HTTP 404) alongside genuinely unknown tickers.
//
// Regression ticker: XRTX (XORTX Therapeutics, Canadian 20-F filer — SEC
// returns HTTP 404 on /api/xbrl/companyfacts/CIK0001729431.json).
func TestHasCompanyFactsNotFoundError(t *testing.T) {
	tests := []struct {
		name string
		errs []entities.FetchError
		want bool
	}{
		{
			name: "empty_list_is_false",
			errs: nil,
			want: false,
		},
		{
			name: "unrelated_error_is_false",
			errs: []entities.FetchError{
				{Source: entities.MarketSource, RawErr: errors.New("market data down")},
			},
			want: false,
		},
		{
			name: "direct_sentinel_is_true",
			errs: []entities.FetchError{
				{Source: entities.SECSource, RawErr: ports.ErrCompanyFactsNotFound},
			},
			want: true,
		},
		{
			name: "wrapped_sentinel_via_fmt_errorf_w_is_true",
			errs: []entities.FetchError{
				{
					Source: entities.SECSource,
					RawErr: fmt.Errorf("failed to fetch SEC financial data: %w", ports.ErrCompanyFactsNotFound),
				},
			},
			want: true,
		},
		{
			name: "doubly_wrapped_sentinel_is_true",
			errs: []entities.FetchError{
				{
					Source: entities.SECSource,
					RawErr: fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ports.ErrCompanyFactsNotFound)),
				},
			},
			want: true,
		},
		{
			name: "rawerr_nil_with_message_only_is_false",
			errs: []entities.FetchError{
				{Source: entities.SECSource, Message: "company facts not found in SEC XBRL (404)"},
			},
			want: false,
		},
		{
			name: "sentinel_present_alongside_other_errors",
			errs: []entities.FetchError{
				{Source: entities.MacroSource, RawErr: errors.New("FRED unavailable")},
				{Source: entities.SECSource, RawErr: fmt.Errorf("wrap: %w", ports.ErrCompanyFactsNotFound)},
				{Source: entities.MarketSource, RawErr: errors.New("yahoo 500")},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasCompanyFactsNotFoundError(tt.errs)
			assert.Equal(t, tt.want, got)
		})
	}
}
