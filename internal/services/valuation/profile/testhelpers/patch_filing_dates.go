// Filing-date patch helper for Tier 2 pin and forward-model tests.
//
// The synthetic ModelInput fixtures in fixtures.go deliberately stamp only
// FinancialData.AsOf (mirroring how real entity construction populates the
// reporting period), but several model layers — DDM, FFO, RevenueMultiple —
// resolve "latest period" via HistoricalFinancialData.GetLatestPeriod, which
// keys on FilingDate. A zero FilingDate causes GetLatestPeriod to return nil
// and the model to error with "no financial data available".
//
// PatchFilingDatesFromAsOf back-fills FilingDate from AsOf on every period
// whose FilingDate is the zero value. It is the single source of truth for
// this fixture patch, consolidating the previously-duplicated inline loops in
// tier2_regression_test.go / pin_capture_test.go / tier2_pin_inputs_test.go /
// ddm_multistage_test.go / ffo_forward_test.go (T2-P4-W2 item 11).
//
// The helper is nil-safe: it no-ops when input is nil, when HistoricalData is
// nil, or for any individual period entry that is nil. It only overwrites
// FilingDate when the existing value is zero, so fixtures that intentionally
// set distinct FilingDate values remain untouched.
package testhelpers

import (
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// PatchFilingDatesFromAsOf walks input.HistoricalData.Data and sets
// FilingDate = AsOf for every period whose FilingDate is the zero value.
// No-op when input or input.HistoricalData is nil; preserves any period that
// already has a non-zero FilingDate.
func PatchFilingDatesFromAsOf(input *models.ModelInput) {
	if input == nil || input.HistoricalData == nil {
		return
	}
	for _, period := range input.HistoricalData.Data {
		if period == nil {
			continue
		}
		if period.FilingDate.IsZero() {
			period.FilingDate = period.AsOf
		}
	}
}
