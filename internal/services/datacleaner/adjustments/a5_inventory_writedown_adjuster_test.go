package adjustments

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// productionObsoleteInventoryRule returns a CleaningRule whose ID matches the
// production rules.json entry ("obsolete_inventory") so the rule reaches the
// obsolete_inventory branch in ProcessAssetAdjustments. The existing
// createInventoryRule() helper uses ID="A5" which short-cuts via the rule.ID
// switch's `default` arm — fine for direct ProcessInventoryAdjustment tests
// but useless for dispatcher tests.
func productionObsoleteInventoryRule() *entities.CleaningRule {
	return &entities.CleaningRule{
		ID:          "obsolete_inventory",
		Name:        "Dead Inventory Adjustment",
		Category:    entities.AssetQuality,
		Adjustment:  entities.Writedown,
		Description: "Write down obsolete or slow-moving inventory",
		Threshold: &entities.ThresholdConfig{
			PercentageOfAssets: &[]float64{0.25}[0], // 25% threshold
			GrowthMultiple:     &[]float64{3.0}[0],  // Minimum turnover
			WritedownRate:      &[]float64{0.40}[0], // 40% writedown
		},
		Enabled: true,
	}
}

// TestA5InventoryWritedownAdjuster_Adjuster_Interface_Contract pins the
// DC-1 Phase 2 PR-2 Task 2.4 acceptance gate: a5InventoryWritedownAdjuster
// satisfies the Adjuster interface AND its AdjusterOutput matches the spec /
// plan §3.5 / §4 row A5 Restater + TaxShieldDTA contract for the fired path
// (both obsolescence + ratio-above-threshold trigger conditions) and both
// skipped paths.
//
// The compile-time assertion
// `var _ Adjuster = (*a5InventoryWritedownAdjuster)(nil)` in assets.go is the
// primary signature pin; this test exercises the runtime contract — every
// branch of ApplyA5InventoryWritedown produces an AdjusterOutput whose
// LedgerEntries (Restater-shaped, with TaxShieldDTA when EffectiveTaxRate>0)
// and Flags match the shape the orchestrator + Phase 3 view reconstruction
// will rely on.
//
// A5 is the FIRST PR-2 adjuster to populate LedgerEntry.TaxShieldDTA — see the
// "fired path TaxShieldDTA with EffectiveTaxRate=21%" subtest below for the
// CANONICAL PIN of the formula `writedownAmount * EffectiveTaxRate`.
func TestA5InventoryWritedownAdjuster_Adjuster_Interface_Contract(t *testing.T) {
	// SR-1 A3: the adapter struct was deleted; call ApplyA5InventoryWritedown
	// directly on the AssetAdjuster (the production dispatch path).
	adj := NewAssetAdjuster()
	require.NotNil(t, adj)

	rule := createInventoryRule()

	// retailCtx (GICS 25) has a 40% industry threshold per
	// getInventoryThresholdForIndustry. Used for the ratio-above-threshold path.
	retailCtx := createRetailContext()
	// techCtx (GICS 45) has a 5% industry threshold — easier to trigger the
	// ratio-above-threshold path with smaller inputs.
	techCtx := createTechContext()
	// defaultCtx (GICS 20, industrials) — 20% threshold. Healthy 15% ratio
	// fits "within threshold" for the skip test.
	defaultCtx := createDefaultContext()

	t.Run("fired path TaxShieldDTA with EffectiveTaxRate=21%", func(t *testing.T) {
		// CANONICAL PIN for the A5 TaxShieldDTA formula (plan §4 row A5 + §7
		// Task 2.4): EffectiveTaxRate=0.21 (typical US federal corporate
		// rate); inventoryRatio = 400k/1M = 40%, retail threshold = 40%,
		// turnover=2 → obsolescence trigger AND ratio at edge fires.
		// writedownAmount = 400_000 * 0.40 = 160_000;
		// TaxShieldDTA = 160_000 * 0.21 = 33_600.
		data := &entities.FinancialData{
			Inventory:         400_000.0,
			TotalAssets:       1_000_000.0,
			InventoryTurnover: 2.0, // low turnover → obsolete
			EffectiveTaxRate:  0.21,
		}

		out, err := adj.ApplyA5InventoryWritedown(context.Background(), data, rule, retailCtx)
		require.NoError(t, err, "Apply must not error on a well-formed fired-path input")

		require.Len(t, out.LedgerEntries, 1, "fired path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "A5 is a Restater — must NOT emit OverlaySpecs")
		require.Len(t, out.Flags, 1, "fired A5 emits exactly one FlagSeverityHigh flag")

		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "fired-path LedgerEntry must have Fired=true")
		assert.Equal(t, adjusterIDA5InventoryWritedown, entry.AdjusterID)
		assert.Equal(t, rule.ID, entry.RuleID)
		assert.Equal(t, "Inventory", entry.Component,
			"A5 is a Restater — Component must point at the mutated balance-sheet line")
		assert.InDelta(t, -160_000.0, entry.DeltaAmount, 1e-6,
			"DeltaAmount must equal -writedownAmount (signed reduction of Inventory)")
		assert.InDelta(t, -160_000.0, entry.EquityOffset, 1e-6,
			"EquityOffset must mirror DeltaAmount — writedowns reduce equity 1:1")
		// CANONICAL PIN: TaxShieldDTA = writedownAmount * EffectiveTaxRate.
		// 160_000 * 0.21 = 33_600. Any change to this formula must update
		// both the production code AND this assertion atomically.
		assert.InDelta(t, 33_600.0, entry.TaxShieldDTA, 1e-6,
			"TaxShieldDTA must equal writedownAmount * EffectiveTaxRate (160_000 * 0.21)")

		// Apply must NOT mutate working — the dispatcher owns the dual-write.
		assert.Equal(t, 400_000.0, data.Inventory, "Apply must NOT mutate data.Inventory")
		assert.Equal(t, 1_000_000.0, data.TotalAssets, "Apply must NOT mutate data.TotalAssets")
	})

	t.Run("fired path TaxShieldDTA=0 when EffectiveTaxRate=0", func(t *testing.T) {
		// When EffectiveTaxRate is 0 (e.g. data unavailable or NOL position),
		// no derived shield is computed — TaxShieldDTA stays at zero so
		// omitempty serialization keeps the field out of replay JSON.
		data := &entities.FinancialData{
			Inventory:         400_000.0,
			TotalAssets:       1_000_000.0,
			InventoryTurnover: 2.0, // obsolete trigger
			EffectiveTaxRate:  0.0,
		}

		out, err := adj.ApplyA5InventoryWritedown(context.Background(), data, rule, retailCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired)
		assert.InDelta(t, -160_000.0, entry.DeltaAmount, 1e-6,
			"DeltaAmount still reflects the writedown — only TaxShieldDTA goes to zero")
		assert.Zero(t, entry.TaxShieldDTA,
			"TaxShieldDTA must be 0 when EffectiveTaxRate is 0 (no shield to compute)")
	})

	t.Run("fired via obsolescence (low turnover)", func(t *testing.T) {
		// Healthy inventory ratio for industrials (15% < 20% threshold) BUT
		// turnover=2 < 3 triggers detectInventoryObsolescence. Even though
		// the ratio is "within threshold", A5 fires via the obsolescence
		// path — preserving the legacy two-condition firing logic.
		data := &entities.FinancialData{
			Inventory:         150_000.0, // 15% ratio
			TotalAssets:       1_000_000.0,
			InventoryTurnover: 2.0, // low turnover trips obsolescence
			EffectiveTaxRate:  0.21,
		}

		out, err := adj.ApplyA5InventoryWritedown(context.Background(), data, rule, defaultCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "low turnover must trigger A5 even when ratio is below threshold")
		assert.InDelta(t, -60_000.0, entry.DeltaAmount, 1e-6, "writedown = 150_000 * 0.40")
	})

	t.Run("fired via ratio above industry threshold", func(t *testing.T) {
		// Healthy turnover (8.0 — well above the 3.0 floor) AND inventory
		// ratio of 8% > tech industry threshold of 5%. Fires via the
		// ratio-above-threshold path only.
		data := &entities.FinancialData{
			Inventory:         80_000.0, // 8% ratio
			TotalAssets:       1_000_000.0,
			InventoryTurnover: 8.0, // healthy turnover
			EffectiveTaxRate:  0.21,
		}

		out, err := adj.ApplyA5InventoryWritedown(context.Background(), data, rule, techCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		entry := out.LedgerEntries[0]
		assert.True(t, entry.Fired, "ratio above industry threshold must trigger A5")
		assert.InDelta(t, -32_000.0, entry.DeltaAmount, 1e-6, "writedown = 80_000 * 0.40")
		assert.InDelta(t, 32_000.0*0.21, entry.TaxShieldDTA, 1e-6,
			"TaxShieldDTA must apply on this firing path too")
	})

	t.Run("no-inventory skip", func(t *testing.T) {
		data := &entities.FinancialData{
			Inventory:   0.0,
			TotalAssets: 1_000_000.0,
		}

		out, err := adj.ApplyA5InventoryWritedown(context.Background(), data, rule, retailCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1, "skip path emits exactly one LedgerEntry")
		assert.Empty(t, out.Overlays, "skip path emits no OverlaySpec")
		assert.Empty(t, out.Flags, "skip path emits no Flags")

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDA5InventoryWritedown, entry.AdjusterID)
		assert.Equal(t, "No inventory present to adjust", entry.SkipReason,
			"no-inventory skip path must use the canonical SkipReason string")
		assert.Empty(t, entry.SkipMetrics,
			"no-inventory skip path does not carry SkipMetrics — only the threshold-failed path does")
		// Plan §3.6.6: skipped entries carry zero monetary deltas.
		assert.Zero(t, entry.DeltaAmount)
		assert.Zero(t, entry.EquityOffset)
		assert.Zero(t, entry.TaxShieldDTA)
	})

	t.Run("within-threshold skip", func(t *testing.T) {
		// 15% ratio < 20% default-industrials threshold AND turnover=6 (>3,
		// no obsolescence) → both firing conditions fail → skip with
		// SkipMetrics populated.
		data := &entities.FinancialData{
			Inventory:         150_000.0,
			TotalAssets:       1_000_000.0,
			InventoryTurnover: 6.0,
		}

		out, err := adj.ApplyA5InventoryWritedown(context.Background(), data, rule, defaultCtx)
		require.NoError(t, err)

		require.Len(t, out.LedgerEntries, 1)
		assert.Empty(t, out.Overlays)
		assert.Empty(t, out.Flags)

		entry := out.LedgerEntries[0]
		assert.False(t, entry.Fired)
		assert.Equal(t, adjusterIDA5InventoryWritedown, entry.AdjusterID)
		assert.Contains(t, entry.SkipReason, "within threshold",
			"within-threshold SkipReason must explain why")
		require.NotNil(t, entry.SkipMetrics,
			"within-threshold skip path must carry SkipMetrics")
		// All four diagnostic keys present per Task 2.4 spec.
		assert.InDelta(t, 0.15, entry.SkipMetrics["inventory_ratio"], 1e-9)
		assert.InDelta(t, 0.20, entry.SkipMetrics["threshold"], 1e-9)
		assert.InDelta(t, 6.0, entry.SkipMetrics["inventory_turnover"], 1e-9)
		assert.InDelta(t, 0.0, entry.SkipMetrics["is_obsolete"], 1e-9,
			"is_obsolete encoded as 0.0 when detection returned false")
	})
}

// TestProcessInventoryAdjustment_TaxShieldDTA_PopulatedWhenEffectiveTaxRateNonZero
// is the REQUIRED-EXACT-NAME regression test per plan §7 Task 2.4 acceptance
// signal: when the adapter Adjuster path runs with EffectiveTaxRate > 0, the
// fired LedgerEntry's TaxShieldDTA is positive (not zero, not negative). This
// pins the first-population invariant for A5 — A1/A2/A4 leave TaxShieldDTA at
// zero, so the test surface for "is the formula wired correctly?" lives here.
func TestProcessInventoryAdjustment_TaxShieldDTA_PopulatedWhenEffectiveTaxRateNonZero(t *testing.T) {
	adj := NewAssetAdjuster()

	data := &entities.FinancialData{
		Inventory:         400_000.0,
		TotalAssets:       1_000_000.0,
		InventoryTurnover: 2.0, // obsolete trigger
		EffectiveTaxRate:  0.25,
	}
	rule := createInventoryRule()
	cleaningCtx := createRetailContext()

	out, err := adj.ApplyA5InventoryWritedown(context.Background(), data, rule, cleaningCtx)
	require.NoError(t, err)
	require.Len(t, out.LedgerEntries, 1)

	entry := out.LedgerEntries[0]
	assert.True(t, entry.Fired, "must fire on this input")
	// CANONICAL PIN: TaxShieldDTA is strictly positive when EffectiveTaxRate>0.
	assert.Greater(t, entry.TaxShieldDTA, 0.0,
		"TaxShieldDTA must be > 0 when EffectiveTaxRate > 0")
	// And matches the formula: writedown (160_000) * 0.25 = 40_000.
	assert.InDelta(t, 40_000.0, entry.TaxShieldDTA, 1e-6,
		"TaxShieldDTA = writedownAmount * EffectiveTaxRate (160_000 * 0.25)")
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeA5Emission pins the
// dispatcher's contract for the obsolete_inventory rule: when present in the
// input rules AND A5 fires, ProcessAssetAdjustments populates
// NativeLedgerEntries with the A5 fired entry AND mutates data.Inventory /
// data.TotalAssets exactly as the legacy ProcessInventoryAdjustment did
// (dual-write preserved).
func TestAssetAdjuster_ProcessAssetAdjustments_NativeA5Emission(t *testing.T) {
	aa := NewAssetAdjuster()
	// inventoryRatio = 400_000 / 1_000_000 = 40% — at retail threshold edge;
	// turnover=2 trips obsolescence anyway. writedown = 160_000.
	data := &entities.FinancialData{
		Inventory:         400_000.0,
		TotalAssets:       1_000_000.0,
		InventoryTurnover: 2.0,
		EffectiveTaxRate:  0.21,
	}
	rules := []*entities.CleaningRule{productionObsoleteInventoryRule()}
	cleaningCtx := createRetailContext()

	result := aa.ProcessAssetAdjustments(context.Background(), data, rules, cleaningCtx)
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: the legacy *AdjustmentResult fields were deleted.
	// The fired magnitude / FromAccount / ToAccount / Type / fixed-40%
	// Percentage are now asserted via the native Restater LedgerEntry below +
	// the projection metadata table (A5 → percentageConstant 40.0), covered
	// end-to-end by the basket-parity golden.

	// Phase 2 PR-2 Task 2.4 native emission contract:
	require.Len(t, result.NativeLedgerEntries, 1,
		"ProcessAssetAdjustments must surface the A5 native LedgerEntry")
	assert.Empty(t, result.NativeOverlays, "A5 is a Restater — no OverlaySpec native emission")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["obsolete_inventory"],
		"obsolete_inventory must appear in NativelyEmittedRuleIDs so the shim skips it")

	// Restater + TaxShieldDTA shape on the native entry:
	nativeEntry := result.NativeLedgerEntries[0]
	assert.True(t, nativeEntry.Fired)
	assert.Equal(t, adjusterIDA5InventoryWritedown, nativeEntry.AdjusterID)
	assert.Equal(t, "Inventory", nativeEntry.Component)
	assert.InDelta(t, -160_000.0, nativeEntry.DeltaAmount, 1e-6)
	assert.InDelta(t, -160_000.0, nativeEntry.EquityOffset, 1e-6)
	assert.InDelta(t, 160_000.0*0.21, nativeEntry.TaxShieldDTA, 1e-6,
		"A5 TaxShieldDTA flows through the dispatcher path unchanged")

	// DC-1 Phase 4 (C-2, §8.2.1 Option A): the dispatcher applies the fired
	// LedgerEntry's COMPONENT delta only. data.Inventory is reduced by the
	// writedown (originalInventory 400_000 + DeltaAmount(-160_000) = 240_000).
	// The legacy umbrella dual-write is DELETED, so data.TotalAssets stays at
	// its pre-clean value; Restated() recomputes the umbrella from components.
	assert.InDelta(t, 240_000.0, data.Inventory, 1e-6,
		"dispatcher must apply the component delta to data.Inventory")
	assert.InDelta(t, 1_000_000.0, data.TotalAssets, 1e-6,
		"Phase 4 §8.2.1 Option A: dispatcher must NOT mutate the data.TotalAssets umbrella")
}

// TestAssetAdjuster_ProcessAssetAdjustments_NativeA5SkipPath confirms that
// even on the skip path (no inventory present), ProcessAssetAdjustments
// surfaces the Fired:false LedgerEntry through NativeLedgerEntries and
// performs NO mutation — the shim then skips emitting its own generic skip
// entry for the same rule.
func TestAssetAdjuster_ProcessAssetAdjustments_NativeA5SkipPath(t *testing.T) {
	aa := NewAssetAdjuster()
	data := &entities.FinancialData{
		Inventory:   0.0,
		TotalAssets: 1_000_000.0,
	}
	rules := []*entities.CleaningRule{productionObsoleteInventoryRule()}

	result := aa.ProcessAssetAdjustments(context.Background(), data, rules, createRetailContext())
	require.NotNil(t, result)

	// DC-1 Phase 5 P5-C4: skip contract asserted natively — no fired entry.

	// Native emission contract — skip path still emits a Fired:false entry.
	require.Len(t, result.NativeLedgerEntries, 1,
		"skip path must still surface a Fired:false native LedgerEntry")
	assert.False(t, result.NativeLedgerEntries[0].Fired)
	assert.Empty(t, result.NativeOverlays, "skip path emits no Overlays")
	require.NotNil(t, result.NativelyEmittedRuleIDs)
	assert.True(t, result.NativelyEmittedRuleIDs["obsolete_inventory"],
		"obsolete_inventory must appear in NativelyEmittedRuleIDs even on skip path")

	// Dual-write contract — skip path must NOT mutate balance-sheet fields.
	assert.Equal(t, 0.0, data.Inventory)
	assert.Equal(t, 1_000_000.0, data.TotalAssets)
}
