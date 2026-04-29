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

// TestHasForeignPrivateIssuerError guards the classification rule that lets
// the valuation service return ErrForeignPrivateIssuer (→ HTTP 422
// FOREIGN_PRIVATE_ISSUER_UNSUPPORTED) for tickers whose 20-F filings carry
// ifrs-full taxonomy, instead of misclassifying them as generic
// ErrInsufficientData alongside clinical-stage biotechs.
//
// Regression ticker: TSM (Taiwan Semiconductor Manufacturing — see
// artifacts/2026-04-26/_no-ticker/req_78653629… and
// docs/refactoring/ifrs-foreign-private-issuer-support-spec.md).
func TestHasForeignPrivateIssuerError(t *testing.T) {
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
			name: "company_facts_not_found_is_false_for_fpi_check",
			errs: []entities.FetchError{
				{Source: entities.SECSource, RawErr: ports.ErrCompanyFactsNotFound},
			},
			want: false, // CompanyFactsNotFound is a sibling sentinel, not an FPI
		},
		{
			name: "direct_fpi_sentinel_is_true",
			errs: []entities.FetchError{
				{Source: entities.SECSource, RawErr: ports.ErrForeignPrivateIssuer},
			},
			want: true,
		},
		{
			name: "wrapped_fpi_sentinel_via_fmt_errorf_w_is_true",
			errs: []entities.FetchError{
				{
					Source: entities.SECSource,
					RawErr: fmt.Errorf("failed to parse SEC financial data: %w", ports.ErrForeignPrivateIssuer),
				},
			},
			want: true,
		},
		{
			name: "doubly_wrapped_fpi_sentinel_is_true",
			errs: []entities.FetchError{
				{
					Source: entities.SECSource,
					RawErr: fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ports.ErrForeignPrivateIssuer)),
				},
			},
			want: true,
		},
		{
			name: "rawerr_nil_with_message_only_is_false",
			errs: []entities.FetchError{
				{Source: entities.SECSource, Message: "ifrs-full taxonomy not supported"},
			},
			want: false,
		},
		{
			name: "fpi_present_alongside_other_errors",
			errs: []entities.FetchError{
				{Source: entities.MacroSource, RawErr: errors.New("FRED unavailable")},
				{Source: entities.SECSource, RawErr: fmt.Errorf("wrap: %w", ports.ErrForeignPrivateIssuer)},
				{Source: entities.MarketSource, RawErr: errors.New("yahoo 500")},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasForeignPrivateIssuerError(tt.errs)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestErrForeignPrivateIssuer_IsExportedAndDistinct ensures the new sentinel
// is exported, non-nil, and chains correctly through fmt.Errorf("%w", ...) —
// guarding against accidental regressions to the unexported-error pattern
// used for errFallbackToDCF.
func TestErrForeignPrivateIssuer_IsExportedAndDistinct(t *testing.T) {
	assert.NotNil(t, ErrForeignPrivateIssuer)
	wrapped := fmt.Errorf("ticker TSM: %w", ErrForeignPrivateIssuer)
	assert.True(t, errors.Is(wrapped, ErrForeignPrivateIssuer),
		"wrapped error must satisfy errors.Is for the FPI sentinel")
	assert.False(t, errors.Is(wrapped, ErrInsufficientData),
		"FPI must not satisfy errors.Is for ErrInsufficientData (would mask 422 code mapping)")
	assert.False(t, errors.Is(wrapped, ErrTickerNotFound),
		"FPI must not satisfy errors.Is for ErrTickerNotFound (would degrade to 404)")
}
