# BUG-010: Growth rate not capped by dcf_max_growth_rate config — produces unrealistic valuations

| Field | Value |
|-------|-------|
| **ID** | BUG-010 |
| **Title** | DCF growth rate exceeds configured max (50%) for companies with large revenue jumps — produces inflated valuations |
| **Severity** | MEDIUM |
| **Status** | Open |
| **Component** | Valuation Service / DCF Calculation |
| **Reported** | 2026-04-05 |

## Summary

The config defines `dcf_max_growth_rate: 0.5` (50%) and `dcf_min_growth_rate: -0.3` (-30%) as bounds for growth rates, but these caps are never enforced in the valuation pipeline. When a company has a large revenue jump (e.g., J&J post-acquisition), the calculated growth rate can exceed 50%, producing unrealistic DCF valuations.

## Observed Behavior

```bash
curl -H "X-API-Key: <key>" http://localhost:8080/api/v1/fair-value/JNJ
```

Response:
```json
{
  "ticker": "JNJ",
  "wacc": 0.0573,
  "growth_rate": 0.6466,
  "dcf_value_per_share": 3942.87,
  "data_quality_score": 100,
  "data_quality_grade": "A"
}
```

- **Growth rate**: 64.7% — exceeds the configured max of 50%
- **DCF value**: $3,942/share — JNJ trades at ~$150, this is 26x the market price
- **Data quality**: Grade A despite producing a clearly unrealistic valuation

## Expected Behavior

- Growth rate should be clamped to `[dcf_min_growth_rate, dcf_max_growth_rate]` range ([-30%, +50%])
- With capped 50% growth, the DCF would be significantly lower and more realistic
- Data quality score should be penalized when growth rate required capping (indicates unusual data)

## Root Cause

The config values `DCFMaxGrowthRate` and `DCFMinGrowthRate` are defined at `internal/config/config.go:125-126` and have defaults set at lines 279-280, but are never referenced in the valuation calculation path:

- `valuation/service.go:performValuation()` passes `growthResult.GrowthRate` directly to DCF without bounds checking
- `pkg/finance/dcf/dcf.go:CalculateDCF()` uses the growth rate as-is
- Neither location reads or enforces the config bounds

## Affected Files

| File | Lines | Role |
|------|-------|------|
| `internal/services/valuation/service.go` | ~220-230 | Growth rate passed to DCF inputs without capping |
| `internal/config/config.go` | 125-126 | Config fields `DCFMaxGrowthRate`, `DCFMinGrowthRate` defined but unused |
| `pkg/finance/dcf/dcf.go` | ~40-50 | DCF calculation accepts any growth rate |

## Proposed Fix

In `performValuation()`, after computing the growth rate and before passing to DCF:

```go
// Cap growth rate to configured bounds
if growthResult.GrowthRate > s.config.Valuation.DCFMaxGrowthRate {
    s.logger.Warn("Growth rate capped at maximum",
        zap.Float64("calculated", growthResult.GrowthRate),
        zap.Float64("max", s.config.Valuation.DCFMaxGrowthRate))
    growthResult.GrowthRate = s.config.Valuation.DCFMaxGrowthRate
}
if growthResult.GrowthRate < s.config.Valuation.DCFMinGrowthRate {
    s.logger.Warn("Growth rate capped at minimum",
        zap.Float64("calculated", growthResult.GrowthRate),
        zap.Float64("min", s.config.Valuation.DCFMinGrowthRate))
    growthResult.GrowthRate = s.config.Valuation.DCFMinGrowthRate
}
```

Also consider penalizing the data quality score when capping is applied — it indicates the historical data contains anomalies (M&A, one-time events).

## Acceptance Criteria

- [ ] JNJ growth rate is capped at 50% (not 64.7%)
- [ ] JNJ DCF value is significantly lower (order of magnitude more reasonable)
- [ ] Capping is logged as a warning with original and capped values
- [ ] Config values `dcf_max_growth_rate` and `dcf_min_growth_rate` are respected
- [ ] Existing tests pass (AAPL, MSFT growth rates are under 50% so unaffected)
- [ ] New test: verify a growth rate > max is capped to max

## References

- Config defaults: `dcf_max_growth_rate: 0.5`, `dcf_min_growth_rate: -0.3` (config.go:279-280)
- JNJ's high growth likely due to the 2023 Kenvue spin-off or acquisition activity causing a revenue discontinuity
- Standard DCF practice: growth rates above GDP+inflation (3-5%) for terminal value, and above 30-40% for explicit forecast, should raise red flags
