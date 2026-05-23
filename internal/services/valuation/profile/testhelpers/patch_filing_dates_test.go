package testhelpers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

// TestPatchFilingDatesFromAsOf_NilInput covers the safety guard at the top
// of the helper: a nil *ModelInput must no-op without panicking.
func TestPatchFilingDatesFromAsOf_NilInput(t *testing.T) {
	assert.NotPanics(t, func() {
		PatchFilingDatesFromAsOf(nil)
	})
}

// TestPatchFilingDatesFromAsOf_NilHistoricalData covers the second safety
// guard: a non-nil input with a nil HistoricalData pointer must no-op.
func TestPatchFilingDatesFromAsOf_NilHistoricalData(t *testing.T) {
	input := &models.ModelInput{HistoricalData: nil}
	assert.NotPanics(t, func() {
		PatchFilingDatesFromAsOf(input)
	})
}

// TestPatchFilingDatesFromAsOf_NilPeriodEntry covers the per-iteration nil
// guard: a map with a nil *FinancialData value must be skipped.
func TestPatchFilingDatesFromAsOf_NilPeriodEntry(t *testing.T) {
	input := &models.ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Data: map[string]*entities.FinancialData{"2026FY": nil},
		},
	}
	assert.NotPanics(t, func() {
		PatchFilingDatesFromAsOf(input)
	})
}

// TestPatchFilingDatesFromAsOf_ZeroFilingDate_PatchedFromAsOf is the
// happy-path assertion: any period with a zero FilingDate is patched from
// AsOf in place.
func TestPatchFilingDatesFromAsOf_ZeroFilingDate_PatchedFromAsOf(t *testing.T) {
	asOf := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	input := &models.ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Data: map[string]*entities.FinancialData{
				"2026FY": {AsOf: asOf},
			},
		},
	}

	PatchFilingDatesFromAsOf(input)

	assert.Equal(t, asOf, input.HistoricalData.Data["2026FY"].FilingDate)
}

// TestPatchFilingDatesFromAsOf_NonZeroFilingDate_Preserved is the converse:
// a fixture that already sets a distinct FilingDate must not be clobbered.
// This protects the tier2_pin_inputs_test.go PLD builder pattern where
// FilingDate is stamped intentionally per-period.
func TestPatchFilingDatesFromAsOf_NonZeroFilingDate_Preserved(t *testing.T) {
	asOf := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	filedAt := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	input := &models.ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Data: map[string]*entities.FinancialData{
				"2026FY": {AsOf: asOf, FilingDate: filedAt},
			},
		},
	}

	PatchFilingDatesFromAsOf(input)

	assert.Equal(t, filedAt, input.HistoricalData.Data["2026FY"].FilingDate,
		"PatchFilingDatesFromAsOf must not overwrite a pre-set FilingDate")
}

// TestPatchFilingDatesFromAsOf_MixedPeriods exercises a multi-period
// fixture with one zero and one non-zero FilingDate. The zero entry should
// be patched; the non-zero entry must remain untouched.
func TestPatchFilingDatesFromAsOf_MixedPeriods(t *testing.T) {
	asOfA := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	asOfB := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	filedB := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	input := &models.ModelInput{
		HistoricalData: &entities.HistoricalFinancialData{
			Data: map[string]*entities.FinancialData{
				"2025FY": {AsOf: asOfA},
				"2026FY": {AsOf: asOfB, FilingDate: filedB},
			},
		},
	}

	PatchFilingDatesFromAsOf(input)

	assert.Equal(t, asOfA, input.HistoricalData.Data["2025FY"].FilingDate,
		"zero-FilingDate period should be patched from AsOf")
	assert.Equal(t, filedB, input.HistoricalData.Data["2026FY"].FilingDate,
		"non-zero FilingDate period should be preserved")
}
