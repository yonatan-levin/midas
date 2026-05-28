package models

import (
	"context"
	"fmt"
	"math"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/thresholds"
)

// Minimum spread between cost of equity and dividend growth rate.
// Below this the Gordon Growth denominator blows up and the model is unreliable.
const ddmDenominatorEpsilon = 0.005

// Fraction of cost of equity to cap dividend growth at when g >= CoE.
// Keeps the model numerically stable for companies with temporarily high growth.
const ddmGrowthCapFraction = 0.7

// DDMModel implements the Gordon Growth / Dividend Discount Model for financial companies.
//
// Value = DPS * (1 + g) / (CoE - g)
//
// Where:
//   - DPS = Dividends per share
//   - g   = Dividend growth rate (from historical DPS growth or analyst estimates)
//   - CoE = Cost of equity (from CAPM)
//
// This model is appropriate for mature financial companies (banks, insurance)
// that pay regular dividends. It should NOT be used for growth companies
// with zero or irregular dividends.
//
// P/BV cross-check: compares implied P/BV against the ROE-justified P/BV
// = (ROE - g) / (CoE - g). Informational only — does not override DDM.
type DDMModel struct {
	logger *zap.Logger
}

// NewDDMModel creates a new Dividend Discount Model.
func NewDDMModel(logger *zap.Logger) *DDMModel {
	return &DDMModel{
		logger: logger.Named("ddm-model"),
	}
}

// ModelType returns the model identifier.
func (m *DDMModel) ModelType() string {
	return "ddm"
}

// Calculate is the Tier 2 P3 dispatcher. Routes legacy mature-large-bank
// (or nil-profile / horizon-0) requests to the verbatim-preserved
// single-stage Gordon path; routes multi-stage dividend profiles to
// calculateMultiStage. Spec §6.3, §7.1.
//
// Path discipline (CRITICAL): calculateLegacyGordon's body is BYTE-IDENTICAL
// to the pre-Tier-2 Calculate body (master HEAD 0324057). Verified at commit
// time via `diff git show 0324057:.../ddm.go HEAD:.../ddm.go`. Any reordering
// or extraction in that path will trip TestDDM_LegacyPath_BitForBit
// (JPM/BAC/WFC Float64bits equality).
func (m *DDMModel) Calculate(ctx context.Context, input *ModelInput) (*ModelResult, error) {
	// Defensive: nil input is allowed through so the legacy path can return
	// its canonical "model input is required" error (preserves pre-Tier-2
	// error surface).
	if input != nil && input.Profile.IsLegacyMatureLargeBankDDM() {
		return m.calculateLegacyGordon(ctx, input)
	}
	if input == nil || input.Profile == nil || input.Profile.DividendForecastHorizon == 0 {
		return m.calculateLegacyGordon(ctx, input)
	}
	return m.calculateMultiStage(ctx, input)
}

// calculateLegacyGordon is the verbatim pre-Tier-2 DDM body. DO NOT MODIFY
// — every statement, comment, and whitespace character must match the
// master HEAD 0324057 Calculate body. The lift from Calculate to this
// sibling is a rename + cut+paste; nothing else. Bit-for-bit pinned via
// TestDDM_LegacyPath_BitForBit. Spec §7.1.
//
// Uses the Gordon Growth Model for a single-stage DDM.
// Falls back to P/E based approach if DPS is not available but the company is a dividend payer.
func (m *DDMModel) calculateLegacyGordon(ctx context.Context, input *ModelInput) (*ModelResult, error) {
	if input == nil {
		return nil, fmt.Errorf("ddm: model input is required")
	}

	latest, _ := input.HistoricalData.GetLatestPeriod()
	if latest == nil {
		return nil, fmt.Errorf("ddm: no financial data available")
	}

	dps := latest.DividendsPerShare
	if dps <= 0 {
		return nil, fmt.Errorf("ddm: company does not pay dividends (DPS=%.2f); DDM not applicable", dps)
	}

	costOfEquity := input.CostOfEquity
	if costOfEquity <= 0 {
		return nil, fmt.Errorf("ddm: cost of equity must be positive (got %.4f)", costOfEquity)
	}

	// Estimate dividend growth rate from historical data or growth estimate
	dividendGrowth := m.estimateDividendGrowth(input)

	// Guard: dividend growth must be below cost of equity for Gordon model to work
	if dividendGrowth >= costOfEquity {
		originalGrowth := dividendGrowth
		dividendGrowth = costOfEquity * ddmGrowthCapFraction
		logctx.Or(ctx, m.logger).Warn("Dividend growth exceeds cost of equity, capping",
			zap.Float64("original_growth", originalGrowth),
			zap.Float64("capped_growth", dividendGrowth))
	}

	// Gordon Growth Model: Value = DPS * (1 + g) / (CoE - g)
	denominator := costOfEquity - dividendGrowth
	if denominator <= ddmDenominatorEpsilon {
		denominator = ddmDenominatorEpsilon
	}

	valuePerShare := dps * (1 + dividendGrowth) / denominator

	// Calculate implied equity and enterprise values.
	//
	// DC-1 Phase 5 (P5-C1): the EV↔equity bridge adds DebtLikeClaims (B1
	// operating-lease + B2 pension + B3 contingent overlays) so B-rule-firing
	// banks do not silently drop those claims from the reported EnterpriseValue.
	// DDM derives equity FROM dividends and then derives EV FROM equity
	// (EV = equity + debt − cash), so DebtLikeClaims are ADDED — the opposite
	// sign from the DCF / revenue_multiple bridges which derive equity FROM EV
	// and therefore SUBTRACT DebtLikeClaims. DDM's IntrinsicValuePerShare +
	// EquityValue are unaffected by this correction (they are dividend-derived,
	// independent of debt terms). For the JPM/BAC/WFC bit-for-bit fixtures
	// DebtLikeClaims=0 ⇒ the +0 term preserves EnterpriseValue bits. Spec §3.2.
	equityValue := valuePerShare * input.SharesOutstanding
	enterpriseValue := equityValue + input.InterestBearingDebt + input.DebtLikeClaims - input.CashAndCashEquivalents

	warnings, confidence := m.runDividendDiagnostics(ctx, latest, input.LatestRestatedView, input, dps, dividendGrowth, costOfEquity, valuePerShare, nil)

	logctx.Or(ctx, m.logger).Info("DDM valuation completed",
		zap.Float64("dps", dps),
		zap.Float64("dividend_growth", dividendGrowth),
		zap.Float64("cost_of_equity", costOfEquity),
		zap.Float64("value_per_share", valuePerShare))

	return &ModelResult{
		IntrinsicValuePerShare: valuePerShare,
		EnterpriseValue:        enterpriseValue,
		EquityValue:            equityValue,
		ModelType:              "ddm",
		Warnings:               warnings,
		Confidence:             confidence,
	}, nil
}

// runDividendDiagnostics emits the ROE / payout-ratio / P/BV cross-check
// warnings and computes the warning-count-adjusted confidence score that
// both DDM branches share. Per T2-P4-W2 item 5, lifting these diagnostics
// out of the legacy path lets the multi-stage path achieve parity without
// duplicating the rules.
//
// Path discipline: the legacy single-stage Gordon path's bit-for-bit
// invariant (TestDDM_LegacyPath_BitForBit) asserts equality on
// ModelResult.Warnings (slice content + order) and Confidence in addition
// to the three load-bearing floats. The warning STRINGS, append order, and
// confidence ladder are unchanged from pre-Tier-2 master HEAD 0324057.
// Modifying the strings or reordering the appends here will trip the
// bit-for-bit test.
//
// DC-1 Phase 5 (P5-C2): the StockholdersEquity / NetIncome reads are
// migrated from `latest.X` (the entity field via GetLatestPeriod()) to the
// `view.X` (cleaneddata.Restated() view) when `view` is non-nil. Bit-for-bit
// safety: these fields are CARRIED in Restated() — SE = identityCopy + Σ
// EquityOffset (sum is zero when no Restater fires; identity bits otherwise),
// NI is pure identity-copy. So on the JPM/BAC/WFC fixtures (zero Restaters
// fire) view.X == latest.X bit-for-bit ⇒ the / and > operations produce
// identical Float64 bits ⇒ ROE, warning order, P/BV crosscheck, and
// confidence ladder are byte-identical. Spec §3.3 + §7.
//
// The `view` may be nil on test / no-cleaner paths; callers MUST still
// supply `latest` for the nil-fallback. When view is nil the helper
// reads the entity (pre-P5-C2 behavior).
//
// The initialWarnings slice lets callers seed the diagnostics with a
// preamble (e.g. the multi-stage path's "DDM multi-stage: ..." note);
// pass nil to start clean (the legacy path does this).
func (m *DDMModel) runDividendDiagnostics(
	ctx context.Context,
	latest *entities.FinancialData,
	view *cleaneddata.FinancialDataView,
	input *ModelInput,
	dps, dividendGrowth, costOfEquity, valuePerShare float64,
	initialWarnings []string,
) ([]string, string) {
	warnings := initialWarnings
	if warnings == nil {
		warnings = []string{}
	}

	// DC-1 Phase 5 (P5-C2): select view fields when populated, else fall
	// back to entity. Assignment preserves Float64 bits exactly, so the
	// downstream comparisons / divisions produce identical bits when
	// view.X == latest.X (the bit-for-bit safety case for JPM/BAC/WFC).
	effSE := latest.StockholdersEquity
	effNI := latest.NetIncome
	if view != nil {
		effSE = view.StockholdersEquity
		effNI = view.NetIncome
	}

	// Compute ROE once — reused by the ROE sanity check and the P/BV cross-check (V4.1-N7).
	hasROE := effSE > 0 && effNI > 0
	var roe float64
	if hasROE {
		roe = effNI / effSE
	}

	// Validate ROE reasonableness for financials
	if hasROE {
		if roe < 0.05 {
			warnings = append(warnings, fmt.Sprintf("Low ROE (%.1f%%) may indicate stressed financials", roe*100))
		}
		if roe > 0.25 {
			warnings = append(warnings, fmt.Sprintf("High ROE (%.1f%%) may be unsustainable", roe*100))
		}
	}

	// Payout ratio check
	if effNI > 0 && input.SharesOutstanding > 0 {
		eps := effNI / input.SharesOutstanding
		if eps > 0 {
			payoutRatio := dps / eps
			if payoutRatio > 0.9 {
				warnings = append(warnings, fmt.Sprintf("High payout ratio (%.0f%%) leaves little room for growth", payoutRatio*100))
			}
		}
	}

	// P/BV cross-check: implied P/BV (DDM value / book value per share) vs
	// ROE-justified P/BV (= (ROE - g) / (CoE - g)). Flags >2x or <0.5x divergence
	// as a signal that the DDM value may be inconsistent with fundamentals.
	// Flattened with early-return guards (V4.1-N4).
	pbvCheck := func() {
		if !hasROE || input.SharesOutstanding <= 0 {
			return
		}
		bookValuePerShare := effSE / input.SharesOutstanding
		if bookValuePerShare <= 0 {
			return
		}
		impliedPBV := valuePerShare / bookValuePerShare
		coeMinusG := costOfEquity - dividendGrowth
		if coeMinusG <= ddmDenominatorEpsilon {
			return
		}
		roeMinusG := roe - dividendGrowth
		if roeMinusG <= 0 {
			return
		}
		roeJustifiedPBV := roeMinusG / coeMinusG
		if roeJustifiedPBV <= 0 || impliedPBV <= 0 {
			return
		}
		ratio := impliedPBV / roeJustifiedPBV
		if ratio > thresholds.DeviationHigh || ratio < thresholds.DeviationLow {
			warnings = append(warnings,
				fmt.Sprintf("Implied P/BV (%.2fx) diverges from ROE-justified P/BV (%.2fx); ratio=%.2fx",
					impliedPBV, roeJustifiedPBV, ratio))
		}
		logctx.Or(ctx, m.logger).Debug("P/BV cross-check",
			zap.Float64("implied_pbv", impliedPBV),
			zap.Float64("book_value_per_share", bookValuePerShare),
			zap.Float64("roe", roe),
			zap.Float64("dividend_growth", dividendGrowth))
	}
	pbvCheck()

	confidence := "medium"
	if len(warnings) == 0 && dividendGrowth > 0 {
		confidence = "high"
	}
	if len(warnings) > 1 {
		confidence = "low"
	}

	return warnings, confidence
}

// estimateDividendGrowth calculates the expected dividend growth rate.
// Priority: historical DPS CAGR > sustainable growth (ROE * retention) > growth estimate.
func (m *DDMModel) estimateDividendGrowth(input *ModelInput) float64 {
	// Try to calculate historical DPS growth from multi-year data
	recentYears := input.HistoricalData.GetRecentYears(5)
	if len(recentYears) >= 2 {
		// Find oldest and newest DPS values
		var oldestDPS, newestDPS float64
		for i := len(recentYears) - 1; i >= 0; i-- {
			if recentYears[i].DividendsPerShare > 0 {
				oldestDPS = recentYears[i].DividendsPerShare
				break
			}
		}
		if recentYears[0].DividendsPerShare > 0 {
			newestDPS = recentYears[0].DividendsPerShare
		}

		if oldestDPS > 0 && newestDPS > 0 && newestDPS != oldestDPS {
			years := float64(len(recentYears) - 1)
			if years > 0 {
				cagr := math.Pow(newestDPS/oldestDPS, 1.0/years) - 1
				// Cap to reasonable range for dividend growth
				if cagr > 0.15 {
					cagr = 0.15 // Max 15% dividend growth
				}
				if cagr < -0.05 {
					cagr = -0.05 // Min -5% (declining dividends)
				}
				return cagr
			}
		}
	}

	// Fallback: sustainable growth = ROE * retention ratio.
	//
	// DC-1 Phase 5 (P5-C2): SE/NI/DPS reads migrated from latest.X to the
	// Restated view (input.LatestRestatedView) when populated, with nil-
	// fallback to the entity for test/no-cleaner paths. All three fields are
	// carried (identity + EquityOffset for SE; identity-copied for NI/DPS)
	// in Restated() — so on the JPM/BAC/WFC bit-for-bit fixtures
	// view.X == latest.X exactly. Assignment to local scalars preserves
	// Float64 bits, so divisions/comparisons produce identical bits.
	latest, _ := input.HistoricalData.GetLatestPeriod()
	if latest != nil {
		effSE, effNI, effDPS := latest.StockholdersEquity, latest.NetIncome, latest.DividendsPerShare
		if v := input.LatestRestatedView; v != nil {
			effSE, effNI, effDPS = v.StockholdersEquity, v.NetIncome, v.DividendsPerShare
		}
		if effSE > 0 && effNI > 0 {
			roe := effNI / effSE
			// Estimate retention ratio from payout ratio
			retentionRatio := 0.5 // default 50% retention
			if effDPS > 0 && input.SharesOutstanding > 0 {
				eps := effNI / input.SharesOutstanding
				if eps > 0 {
					payoutRatio := effDPS / eps
					retentionRatio = 1 - payoutRatio
					if retentionRatio < 0 {
						retentionRatio = 0
					}
				}
			}
			sustainableGrowth := roe * retentionRatio
			if sustainableGrowth > 0 && sustainableGrowth < 0.15 {
				return sustainableGrowth
			}
		}
	}

	// Final fallback: use the growth estimate's terminal rate
	if input.GrowthEstimate != nil {
		return input.GrowthEstimate.TerminalGrowthRate
	}

	return 0.03 // 3% default
}

// calculateMultiStage is the Tier 2 P3 multi-stage DDM path for non-mature
// dividend payers (profile.DividendForecastHorizon > 0). Discount cash
// flows over the explicit dividend-forecast horizon at cost of equity,
// applying the profile's per-year DPS growth cap and rising payout-path
// multiplier. Tail value is a Gordon perpetuity at StableDividendGrowth.
// Spec §6.3.
//
// Dispatcher guarantees Profile + DividendForecastHorizon > 0 here, but
// the engine-growth slice + cost-of-equity preconditions can still
// invalidate the math at runtime and are surfaced as errors.
func (m *DDMModel) calculateMultiStage(ctx context.Context, input *ModelInput) (*ModelResult, error) {
	latest, _ := input.HistoricalData.GetLatestPeriod()
	if latest == nil {
		return nil, fmt.Errorf("ddm_multistage: no financial data")
	}
	dps := latest.DividendsPerShare
	if dps <= 0 {
		return nil, fmt.Errorf("ddm_multistage: company does not pay dividends")
	}
	costOfEquity := input.CostOfEquity
	if costOfEquity <= 0 {
		return nil, fmt.Errorf("ddm_multistage: cost of equity must be positive")
	}

	p := &input.Profile.AssumptionProfile
	horizon := p.DividendForecastHorizon

	// Engine growth curve must cover at least the explicit horizon; otherwise
	// the forecast would have to extrapolate beyond the engine's confidence
	// window and the result is no longer faithful to the profile.
	if input.GrowthEstimate == nil {
		return nil, fmt.Errorf("ddm_multistage: growth estimate is required")
	}
	growthRates := input.GrowthEstimate.ProjectedGrowthRates
	if len(growthRates) < horizon {
		return nil, fmt.Errorf("ddm_multistage: growth horizon %d shorter than profile %d",
			len(growthRates), horizon)
	}

	// Explicit period: project DPS forward using the engine growth curve,
	// optionally amplified by a rising payout ratio. Each year is then
	// discounted at cost of equity (cumulative discount factor `discount`).
	explicitPV := 0.0
	projectedDPS := dps
	discount := 1.0
	for i := 0; i < horizon; i++ {
		g := growthRates[i]
		// Cap the per-year DPS growth (only when the profile sets a positive
		// cap — zero means uncapped, which is consistent with the JSON
		// schema's omitempty semantics for profiles that don't care).
		if p.DPSGrowthCap > 0 && g > p.DPSGrowthCap {
			g = p.DPSGrowthCap
		}
		projectedDPS *= 1 + g
		// Rising payout path: scale year i's projected DPS by payout[i]/payout[i-1].
		// The first projected year keeps the trailing payout (no ratio to apply),
		// matching the plan's `i > 0` guard.
		if i > 0 && i < len(p.PayoutPath) && p.PayoutPath[i-1] > 0 {
			payoutMultiplier := p.PayoutPath[i] / p.PayoutPath[i-1]
			projectedDPS *= payoutMultiplier
		}
		discount *= 1 + costOfEquity
		explicitPV += projectedDPS / discount
	}

	// Terminal: Gordon perpetuity at the profile's stable dividend growth.
	// Reuses the legacy Gordon denominator floor (ddmDenominatorEpsilon)
	// so the multi-stage tail handles CoE≈g degeneracy the same way the
	// legacy path does.
	terminalGrowth := p.StableDividendGrowth
	denominator := costOfEquity - terminalGrowth
	if denominator <= ddmDenominatorEpsilon {
		denominator = ddmDenominatorEpsilon
	}
	terminalDPS := projectedDPS * (1 + terminalGrowth)
	terminalValue := terminalDPS / denominator
	terminalPV := terminalValue / discount

	valuePerShare := explicitPV + terminalPV
	equityValue := valuePerShare * input.SharesOutstanding
	// DC-1 Phase 5 (P5-C1): see calculateLegacyGordon for the sign rationale.
	// Multi-stage DDM uses the same EV=equity+debt+DebtLikeClaims−cash bridge.
	enterpriseValue := equityValue + input.InterestBearingDebt + input.DebtLikeClaims - input.CashAndCashEquivalents

	// Per T2-P4-W2 item 5, the multi-stage path uses the same shared
	// dividend-diagnostics helper as the legacy path so the two branches
	// emit ROE / payout / P/BV warnings and warning-count-adjusted
	// confidence on equal footing. The multi-stage preamble warning is
	// seeded in first so the diagnostics output remains diagnostic.
	//
	// Diagnostics use the terminal Gordon growth rate (the stable rate that
	// drives the perpetuity tail) as the dividend-growth input to the
	// P/BV cross-check — this is the dividend trajectory that anchors the
	// resulting value, just as it does in the legacy single-stage path.
	preamble := []string{fmt.Sprintf("DDM multi-stage: %dy explicit + Gordon terminal (g=%.1f%%)",
		horizon, terminalGrowth*100)}
	warnings, confidence := m.runDividendDiagnostics(ctx, latest, input.LatestRestatedView, input, dps, terminalGrowth, costOfEquity, valuePerShare, preamble)

	logctx.Or(ctx, m.logger).Info("DDM multi-stage valuation completed",
		zap.Float64("dps", dps),
		zap.Int("horizon", horizon),
		zap.Float64("cost_of_equity", costOfEquity),
		zap.Float64("stable_growth", terminalGrowth),
		zap.Float64("value_per_share", valuePerShare))

	return &ModelResult{
		IntrinsicValuePerShare: valuePerShare,
		EnterpriseValue:        enterpriseValue,
		EquityValue:            equityValue,
		ModelType:              "ddm",
		Confidence:             confidence,
		HorizonSelected:        horizon,
		Warnings:               warnings,
	}, nil
}
