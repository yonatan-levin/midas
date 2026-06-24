package valuation

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// fyShareHist builds an annual-period HistoricalFinancialData from a list of
// {year, dilutedShares, sbc} triples. FilingDate is spaced by year so the
// share-count CAGR derivation walks oldest→newest correctly.
func fyShareHist(years ...[3]float64) *entities.HistoricalFinancialData {
	h := &entities.HistoricalFinancialData{
		Ticker: "TEST",
		Data:   map[string]*entities.FinancialData{},
	}
	for _, y := range years {
		key := time.Date(int(y[0]), 1, 1, 0, 0, 0, 0, time.UTC).Format("2006") + "FY"
		h.Data[key] = &entities.FinancialData{
			Period:                   key,
			FilingPeriod:             key,
			FilingDate:               time.Date(int(y[0]), 3, 1, 0, 0, 0, 0, time.UTC),
			DilutedSharesOutstanding: y[1],
			StockBasedCompensation:   y[2],
		}
	}
	return h
}

func TestDeriveAnnualDilutionRate(t *testing.T) {
	tests := []struct {
		name         string
		hist         *entities.HistoricalFinancialData
		wantEligible bool
		// wantRate is checked only when wantEligible is true.
		wantRate float64
		rateTol  float64
	}{
		{
			// 1000 → 1102.5 over 2 years with SBC present: CAGR = sqrt(1.1025)-1 = 5%.
			name:         "rising share count with SBC -> positive rate",
			hist:         fyShareHist([3]float64{2022, 1000, 50}, [3]float64{2023, 1050, 55}, [3]float64{2024, 1102.5, 60}),
			wantEligible: true,
			wantRate:     0.05,
			rateTol:      1e-9,
		},
		{
			name:         "flat share count -> ineligible (rate <= 0)",
			hist:         fyShareHist([3]float64{2022, 1000, 50}, [3]float64{2023, 1000, 50}, [3]float64{2024, 1000, 50}),
			wantEligible: false,
		},
		{
			name:         "declining share count (buybacks) -> ineligible (never inflates)",
			hist:         fyShareHist([3]float64{2022, 1000, 50}, [3]float64{2023, 950, 50}, [3]float64{2024, 900, 50}),
			wantEligible: false,
		},
		{
			name:         "single FY period -> ineligible (<2 usable periods)",
			hist:         fyShareHist([3]float64{2024, 1000, 50}),
			wantEligible: false,
		},
		{
			name:         "zero SBC across series -> ineligible (eligibility gate)",
			hist:         fyShareHist([3]float64{2022, 1000, 0}, [3]float64{2023, 1100, 0}, [3]float64{2024, 1200, 0}),
			wantEligible: false,
		},
		{
			name:         "nil history -> ineligible",
			hist:         nil,
			wantEligible: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, eligible := deriveAnnualDilutionRate(tt.hist)
			assert.Equal(t, tt.wantEligible, eligible)
			if tt.wantEligible {
				assert.InDelta(t, tt.wantRate, rate, tt.rateTol)
			} else {
				assert.LessOrEqual(t, rate, 0.0)
			}
		})
	}
}

// enabledProfile is a high-SBC profile with the flag ON and the given cap.
func enabledProfile(cap float64) *profile.ResolvedProfile {
	return &profile.ResolvedProfile{
		AssumptionProfile: profile.AssumptionProfile{
			ProfileID:                  "test:high_sbc",
			DilutedShareForwardEnabled: true,
			MaxAnnualDilutionRate:      cap,
		},
	}
}

func TestApplyDilutedShareForward_NoOp(t *testing.T) {
	s := &Service{}
	eligibleHist := fyShareHist([3]float64{2022, 1000, 50}, [3]float64{2023, 1050, 55}, [3]float64{2024, 1102.5, 60})
	const current = 1102.5
	const horizon = 5

	tests := []struct {
		name string
		rp   *profile.ResolvedProfile
		hist *entities.HistoricalFinancialData
	}{
		{name: "nil profile", rp: nil, hist: eligibleHist},
		{
			name: "flag off",
			rp:   &profile.ResolvedProfile{AssumptionProfile: profile.AssumptionProfile{ProfileID: "x"}},
			hist: eligibleHist,
		},
		{
			name: "ineligible history (flat shares)",
			rp:   enabledProfile(0),
			hist: fyShareHist([3]float64{2022, 1000, 50}, [3]float64{2023, 1000, 50}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forward, rate, audit := s.applyDilutedShareForward(context.Background(), current, tt.rp, tt.hist, horizon)
			assert.Equal(t, current, forward, "no-op must return the current share count")
			assert.Equal(t, 0.0, rate)
			assert.Empty(t, audit)
		})
	}
}

func TestApplyDilutedShareForward_Fired(t *testing.T) {
	s := &Service{}
	// 5%/yr CAGR, SBC present, flag on, cap above the derived rate.
	hist := fyShareHist([3]float64{2022, 1000, 50}, [3]float64{2023, 1050, 55}, [3]float64{2024, 1102.5, 60})
	const current = 1102.5
	const horizon = 5

	forward, rate, audit := s.applyDilutedShareForward(context.Background(), current, enabledProfile(0.10), hist, horizon)

	assert.InDelta(t, 0.05, rate, 1e-9, "applied rate is the derived 5% CAGR (below the 10% cap)")
	want := current * math.Pow(1+0.05, horizon)
	assert.InDelta(t, want, forward, 1e-6)
	assert.Greater(t, forward, current, "forward diluted shares must exceed the current count")
	assert.NotEmpty(t, audit)
}

func TestApplyDilutedShareForward_Clamp(t *testing.T) {
	s := &Service{}
	// 50% CAGR (1000 → 2250 over 2 years), SBC present. Cap to 8%.
	hist := fyShareHist([3]float64{2022, 1000, 50}, [3]float64{2023, 1500, 55}, [3]float64{2024, 2250, 60})
	const current = 2250
	const horizon = 4

	forward, rate, audit := s.applyDilutedShareForward(context.Background(), current, enabledProfile(0.08), hist, horizon)

	assert.InDelta(t, 0.08, rate, 1e-9, "derived rate clamped to the 8% cap")
	want := current * math.Pow(1+0.08, horizon)
	assert.InDelta(t, want, forward, 1e-6)
	assert.NotEmpty(t, audit)
}

func TestApplyDilutedShareForward_DefaultCap(t *testing.T) {
	s := &Service{}
	// 50% CAGR, cap left at 0 -> code default 8%.
	hist := fyShareHist([3]float64{2022, 1000, 50}, [3]float64{2023, 1500, 55}, [3]float64{2024, 2250, 60})
	const current = 2250
	const horizon = 3

	forward, rate, _ := s.applyDilutedShareForward(context.Background(), current, enabledProfile(0), hist, horizon)

	assert.InDelta(t, defaultMaxAnnualDilutionRate, rate, 1e-9, "cap 0 falls back to the code default")
	want := current * math.Pow(1+defaultMaxAnnualDilutionRate, horizon)
	assert.InDelta(t, want, forward, 1e-6)
}
