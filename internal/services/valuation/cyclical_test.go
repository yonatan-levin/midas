package valuation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// fyPeriod builds an FY *FinancialData with the given effective-OI inputs.
// FilingDate ordering only affects which 3 years GetRecentYears keeps; the
// mean is order-independent over the kept set, so we space them by year.
func fyPeriod(year int, normalizedOI, operatingIncome float64) (string, *entities.FinancialData) {
	key := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006") + "FY"
	return key, &entities.FinancialData{
		Period:                    key,
		FilingPeriod:              key,
		FilingDate:                time.Date(year, 3, 1, 0, 0, 0, 0, time.UTC),
		NormalizedOperatingIncome: normalizedOI,
		OperatingIncome:           operatingIncome,
	}
}

func mkHist(t *testing.T, years ...[3]float64) *entities.HistoricalFinancialData {
	t.Helper()
	// each entry: {year, normalizedOI, operatingIncome}
	h := &entities.HistoricalFinancialData{
		Ticker: "TEST",
		Data:   map[string]*entities.FinancialData{},
	}
	for _, y := range years {
		key, data := fyPeriod(int(y[0]), y[1], y[2])
		h.Data[key] = data
	}
	return h
}

func TestNormalizeCyclicalBaseOI(t *testing.T) {
	tests := []struct {
		name       string
		baseOI     float64
		hist       *entities.HistoricalFinancialData
		wantOI     float64
		wantMethod string
	}{
		{
			name:   "trough: latest below 3y mean -> floor to mean",
			baseOI: 100,
			// FY OIs 100, 400, 700 -> mean 400 > 100
			hist:       mkHist(t, [3]float64{2024, 100, 0}, [3]float64{2023, 400, 0}, [3]float64{2022, 700, 0}),
			wantOI:     400,
			wantMethod: "3y_mean",
		},
		{
			name:   "peak: latest above 3y mean -> keep latest",
			baseOI: 700,
			// FY OIs 700, 400, 100 -> mean 400 < 700
			hist:       mkHist(t, [3]float64{2024, 700, 0}, [3]float64{2023, 400, 0}, [3]float64{2022, 100, 0}),
			wantOI:     700,
			wantMethod: "latest",
		},
		{
			name:   "NormalizedOperatingIncome precedence: zero normalized falls back to OperatingIncome",
			baseOI: 100,
			// effective OIs: 300 (norm=0,opinc=300), 500, 400 -> mean 400 > 100
			hist:       mkHist(t, [3]float64{2024, 0, 300}, [3]float64{2023, 500, 0}, [3]float64{2022, 400, 0}),
			wantOI:     400,
			wantMethod: "3y_mean",
		},
		{
			name:       "single FY period -> no-op latest",
			baseOI:     100,
			hist:       mkHist(t, [3]float64{2024, 999, 0}),
			wantOI:     100,
			wantMethod: "latest",
		},
		{
			name:       "zero FY periods -> no-op latest",
			baseOI:     100,
			hist:       mkHist(t),
			wantOI:     100,
			wantMethod: "latest",
		},
		{
			name:   "TTM-annualized base vs FY mean (apples-to-apples)",
			baseOI: 120, // TTM-annualized latest
			// FY mean 400 > 120
			hist:       mkHist(t, [3]float64{2024, 120, 0}, [3]float64{2023, 400, 0}, [3]float64{2022, 680, 0}),
			wantOI:     400,
			wantMethod: "3y_mean",
		},
		{
			name:       "nil hist -> no-op latest",
			baseOI:     100,
			hist:       nil,
			wantOI:     100,
			wantMethod: "latest",
		},
		{
			name:       "latest exactly equals 3y mean -> latest (no value drift)",
			baseOI:     400,
			hist:       mkHist(t, [3]float64{2024, 400, 0}, [3]float64{2023, 400, 0}, [3]float64{2022, 400, 0}),
			wantOI:     400,
			wantMethod: "latest",
		},
		{
			name:   "negative FY year drags the mean down (peak no-op)",
			baseOI: 300,
			// effective OIs: 300, 600, -300 -> mean 200 < 300 -> latest
			hist:       mkHist(t, [3]float64{2024, 300, 0}, [3]float64{2023, 600, 0}, [3]float64{2022, 0, -300}),
			wantOI:     300,
			wantMethod: "latest",
		},
		{
			name:   "only 2 FY periods is a valid mean",
			baseOI: 100,
			// effective OIs: 100, 500 -> mean 300 > 100
			hist:       mkHist(t, [3]float64{2024, 100, 0}, [3]float64{2023, 500, 0}),
			wantOI:     300,
			wantMethod: "3y_mean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOI, gotMethod := normalizeCyclicalBaseOI(tt.baseOI, tt.hist)
			assert.InDelta(t, tt.wantOI, gotOI, 1e-9)
			assert.Equal(t, tt.wantMethod, gotMethod)
		})
	}
}

// TestNormalizeCyclicalBaseOI_NoMutation guards that the helper never mutates
// the supplied history (it is a pure read).
func TestNormalizeCyclicalBaseOI_NoMutation(t *testing.T) {
	h := mkHist(t, [3]float64{2024, 100, 0}, [3]float64{2023, 400, 0}, [3]float64{2022, 700, 0})
	before := h.Data["2024FY"].NormalizedOperatingIncome
	_, _ = normalizeCyclicalBaseOI(100, h)
	assert.Equal(t, before, h.Data["2024FY"].NormalizedOperatingIncome)
}
