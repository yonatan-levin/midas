# DC-1 Phase 2 — Implementation Plan

> **For implementer (BACKEND):** This plan is the source of truth for Phase 2. Read sections in order. Do NOT scope-creep into Phase 3 (`CleanedFinancialData` views) or Phase 4 (consumer migration). When in doubt, file as Phase 3+ follow-up and ship the smaller diff.

**Phase:** 2 — Unified `Adjuster` interface + `AdjustmentLedger` (single Go interface; Restater / Overlay / Hybrid roles emerge from output shape, not interface multiplication)
**Authored:** 2026-05-19 by ARCH (`/plan-and-create`)
**Master HEAD at authoring:** `987ec31` (post Phase 1 followup `b8e9c77` + tier2-baseline refresh `7a08506`)
**Prerequisites SHIPPED:** Phase 0 (`computePlugs`) at `1640394`; Phase 1 (`recomputeUmbrellas` shadow shim) at `2d916a7` + followup `b8e9c77`
**Estimated effort:** ~4 agent shifts split across 4 PRs (see §8). Original human-engineer estimate of ~2.5 weeks has been revised for AI-agent pacing — wall-clock figures below are illustrative, not load-bearing.
**Spec anchor:** [datacleaner-component-primitive-and-parallel-views-spec.md](../spec/datacleaner-component-primitive-and-parallel-views-spec.md) — "Solution shape", "Data model § Adjuster output", "Adjuster reclassification", "Pipeline flow", "Phasing & implementation sequence" Phase 2 row
**Handoff anchor:** [datacleaner-component-primitive-and-parallel-views-phase-2-handoff.md](datacleaner-component-primitive-and-parallel-views-phase-2-handoff.md)
**Shadow-analysis anchor (the punch list):** [datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md](datacleaner-component-primitive-and-parallel-views-phase-1-shadow-analysis.md) §4 Clusters B1, B1-PARSER-TL-ZERO, A1-A5, A-FY-NULL, CL-NULL, F-MSFT-LARGE-PLUG, ZERO-DIVERGENCE

---

## 1. Summary

### Goals

- Introduce a **single** `Adjuster` interface at `internal/services/datacleaner/adjustments/adjuster.go` that every existing adjuster (8 firing + 2 flag-only review functions) implements. Restater / Overlay / Hybrid roles are derived from the output shape (`LedgerEntries` vs `Overlays` populated), NOT from interface multiplication. (Spec §"Adjuster output", lines 70-92.)
- Add `LedgerEntry`, `AdjustmentLedger`, `OverlaySpec`, `AdjusterOutput`, and `AmountSemantics` entities at `internal/core/entities/adjustment_ledger.go`. Append `AdjustmentLedger []LedgerEntry` and `Overlays []OverlaySpec` fields to `entities.FinancialData`. (Spec §"Ledger entry" lines 95-118, §"Overlay specification" lines 124-152.)
- Refactor the 8 firing adjusters (A1, A2, A4, A5 in `assets.go`; B1, B2, B3 in `liabilities.go`; the 7 C-rule adjusters in `earnings.go`) so each one emits the new `AdjusterOutput`. Wire the orchestrator at `service.go:430-501` so it appends `LedgerEntries` and `Overlays` onto `FinancialData` after each adjuster runs. (Spec §"Pipeline flow" lines 194-238.)
- Preserve **bit-for-bit downstream behavior**. Phase 2 ships dual-write: existing `data.TotalAssets -= X` / `data.TotalDebt += X` mutations remain in place AND the new `LedgerEntry`/`OverlaySpec` records are appended. Phase 3 will flip the canonical surface to the view reconstruction; Phase 2 is observable-but-inert. (Spec §"Phasing & implementation sequence" line 440 — "Pipeline collects ledger + overlays but still also mutates input pointer to preserve current behavior.")
- Snapshot diffs in `internal/integration/testdata/recompute-shadow/<TICKER>.json` are the regression signal. The Phase 1 `recomputeUmbrellas` shim stays in place unmodified; its WARN stream now also includes adjuster-name and ledger-entry-id correlations sourced from the new `AdjustmentLedger` field (Phase 2 enrichment is optional and zero-cost when ledger is empty — see §3.6).

### Non-goals (explicit Phase 3 / Phase 4 carve-outs — DO NOT scope-creep)

- **No `CleanedFinancialData` struct.** Phase 3 introduces `CleanedFinancialData{AsReported, Ledger, Overlays}` with lazy `.Restated()` / `.InvestedCapital()` accessors. Phase 2 ships the ledger and overlays as fields on the existing `entities.FinancialData` struct, NOT as a new wrapping type. If you find yourself wanting to add `func (c *CleanedFinancialData) Restated()`, STOP — that's Phase 3.
- **No consumer migration.** None of the 13 read sites in the spec's Consumer Migration Map (spec §"Consumer migration map" lines 390-408) change. WACC still reads `data.TotalDebt`; ROIC still reads `data.StockholdersEquity`; Graham still reads `data.TotalLiabilities`. Phase 4 migrates them. If you find yourself editing `internal/services/valuation/*.go`, STOP — that's Phase 4.
- **No SQLite schema change.** The `adjustment_ledger TEXT` column in spec §"Persistence" is Phase 3+. Phase 2's ledger lives only in in-flight `*FinancialData` instances; not persisted, not cached, not loaded from `financial_data_repository`. If you find yourself editing `internal/infra/database/schema.sql` or `internal/infra/repositories/sqlite/financial_data_repository.go`, STOP.
- **No WACCInputs boundary type.** The new `WACCInputs` struct at spec §"WACCInputs compile-time boundary" lines 410-426 is Phase 4. WACC signature stays `ComputeWACC(data *entities.FinancialData, ...)` in Phase 2.
- **No new observability artifacts.** The `09-parse-provenance.json`, `13-cleaner-audit.json`, `14-overlay-config.json` artifacts from spec §"Observability — four layers" are Phase 3+. Phase 2's only observability surface is the existing `99-narrate.jsonl` + `99-debug-trace.jsonl` streams. If you want to add a new artifact bundle file, file it as Phase 3 follow-up.
- **No B3 routing flip.** The spec's substantive accuracy change — B3 contingent liabilities flowing into `DebtLikeClaims` instead of `TotalDebt` (spec §"B3 routing correction" lines 181-189) — is Phase 4. In Phase 2 B3 still does `data.TotalDebt += result.Amount` exactly as it does today AND emits an `OverlaySpec{Field: "DebtLikeClaims", ...}` ledger entry in parallel. The overlay record is inert.
- **No parser-side fixes.** The T2-BS-3 disposition (§2) chooses Option B specifically to keep parser scope OUT of Phase 2. Do NOT edit `internal/infra/gateways/sec/parser.go`.

### Headline risks (top 3 + mitigation)

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| **R1** | **Cascading break of Tier 2 DDM bit-for-bit invariant.** `TestDDM_LegacyPath_BitForBit` at `internal/services/valuation/models/ddm_bitforbit_test.go` asserts `math.Float64bits` equality on JPM/BAC/WFC. Phase 2 touches `liabilities.go` (B-rule orchestrator at lines 87-88) — even a no-op refactor that re-orders an addition can change float64 bit-pattern via different summation order. | MEDIUM | CRITICAL — load-bearing per CLAUDE.md DC-1 gotcha | Run `go test ./internal/services/valuation/models/ -run TestDDM_LegacyPath_BitForBit -count=1` after **every** PR's BACKEND completion, before VERIFIER. If it fails, REVERT — never update goldens. The `data.TotalDebt += result.Amount` lines at `liabilities.go:87-88` MUST execute in identical order in Phase 2 (dual-write preserves them verbatim). |
| **R2** | **Snapshot drift is intentional in Phase 2 but predicted snapshot diff cluster mapping is fragile.** REVIEWER's mental model from Phase 1 was "any drift = regression"; in Phase 2 drift IS the success signal. A reviewer who didn't re-read the cluster predictions will reject good drift. | HIGH | MEDIUM — slows merge but recoverable | Per-PR §4 punch-list table maps each touched adjuster to its predicted snapshot diff cluster. REVIEWER checklist requires explicit confirmation: "does this snapshot diff match the cluster prediction for the adjuster(s) refactored in this PR? If yes, approve drift; if no, REJECT." The PR description includes the cluster name (e.g., "Cluster A1-A5 reroute"). |
| **R3** | **Master drift during multi-week phase.** Phase 2 is 2.5 weeks. Tier 2 P1-P4 worktrees are live on `internal/services/valuation/`; sibling DC-1 work could land on `internal/services/datacleaner/recompute.go` or `service.go`. Phase 1's followup hit this; recovery cost was 1 day. | MEDIUM | LOW-MEDIUM | BACKEND runs `git fetch && git merge origin/master` weekly in the worktree. PR-1 (interface skeleton) merges first and fast (~3 days) to give downstream PRs a stable base; PR-2 through PR-4 rebase atop PR-1's master state. If a Tier 2 / sibling DC-1 PR lands during Phase 2 and conflicts on `service.go`, use the Phase 1 followup conflict-resolution pattern documented in merge `b8e9c77`. |

---

## 2. T2-BS-3 Disposition Decision (LOAD-BEARING GATE)

### Chosen option: **B — Carve-out via the `LedgerEntry`/`Overlays` shape; document AMD/KO as `AsReported`-untrustworthy for `TotalLiabilities`. Phase 2 does NOT touch `internal/infra/gateways/sec/parser.go`.**

### Justification

1. **Phase 2's scope is `internal/services/datacleaner/`, not `internal/infra/gateways/sec/`.** Option A would require editing `parser.go::findValue` and re-capturing every bundle in `artifacts/tier2-baseline/2026-05-19/` (10 tickers × 12 periods). Bundle re-capture invalidates the `tier2-baseline/2026-05-19/` artifacts that Tier 2 P1-P4 worktrees may still be consuming for their per-archetype validation (CLAUDE.md notes those worktrees are pending rebase + merge). Cross-cutting scope makes Phase 2 a 4-week effort instead of 2.5.
2. **The shadow-analysis Cluster B1-PARSER-TL-ZERO (lines 91-117) is explicit:** "Phase 2's `Adjuster` interface refactor cannot fix this because the missing value comes from the SEC parser, BEFORE the cleaner sees the data." The bug is structurally upstream of Phase 2's surface area.
3. **Existing Graham-floor fallback at `internal/services/valuation/service.go::resolveTotalLiabilities` already handles the AMD/KO zero gracefully** by deriving `TotalLiabilities = TotalAssets − StockholdersEquity` (CLAUDE.md "Graham-floor diagnostic fields" gotcha). No live production consumer breaks under Option B.
4. **Phase 2's `Restated`-equivalent surface is the `recomputeUmbrellas` reconstruction itself**, which already produces the correct AMD/KO TotalLiabilities (~$8.7B for AMD, ~$70B for KO per shadow snapshot recomputed values). When Phase 3 introduces `CleanedFinancialData.Restated()`, it consumes the same `sum(components) + plug` formula and gets the right answer for AMD/KO automatically. Option B preserves the "fix it in `Restated`" path that Phase 3 was going to take anyway.

### Cost the alternative (Option A) would have added

- ~5-7 days parser scope (investigate AMD/KO XBRL fact shape; add fallback rule `if Liabilities missing → LiabilitiesCurrent + LiabilitiesNoncurrent`; unit-test the fallback against AMD CIK 0000002488 + KO CIK 0000021344 raw companyfacts; integration-test against the basket).
- ~1 day to re-capture `artifacts/tier2-baseline/2026-05-19/` bundles after parser fix lands.
- Cross-team coordination cost with Tier 2 P1-P4 worktrees (they pin against pre-fix baseline).
- Phase 2 BACKEND becomes blocked on the parser PR landing first.

### Contingency: Option B Restated-view carve-out shape

Because Phase 2 does NOT yet build the `Restated` view (that's Phase 3), Option B's carve-out is **documentation + a defensive comment**, not code. Specifically:

1. The `LedgerEntry` struct gets a `SourceReliability` field (string enum: `"high"`, `"medium"`, `"parser_known_dropout"`) — see §3.2. Adjusters that fire against an `AsReported` field known to be parser-dropout-affected (e.g., a future Phase 3 consumer of AMD/KO `TotalLiabilities`) can stamp the entry with `SourceReliability: "parser_known_dropout"` and Phase 3's `Restated` accessor can fall back to recomputed components.
2. In Phase 2, no adjuster fires against `TotalLiabilities` (all the B-rule adjusters mutate `TotalDebt`, not `TotalLiabilities`), so the field is documented but unused. It's a hook for Phase 3.
3. **CLAUDE.md gets a new gotcha** noting that AMD/KO `AsReported.TotalLiabilities == 0` is a known parser dropout (T2-BS-3) and the carve-out path: "consumers should use `Restated.TotalLiabilities` post-Phase-3, or the Graham-floor `resolveTotalLiabilities` fallback today." See §9.
4. **T2-BS-3 tracker `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md` gets a status update**: "Disposition: B (Carve-out). Phase 2 ARCH 2026-05-19. Parser fix deferred until Phase 4 closeout or a separate parser-side initiative requests it." Tracker stays OPEN.

### Acceptance signal

A REVIEWER can verify Option B was honored by:
- `git diff` shows zero changes to `internal/infra/gateways/sec/parser.go`, `internal/infra/gateways/sec/plugs.go`, or `internal/infra/gateways/sec/*_test.go`.
- The AMD.json and KO.json shadow snapshots in `internal/integration/testdata/recompute-shadow/` show **unchanged** TotalLiabilities divergence records (12 each, `reported_TL=0`, `clamp_suspected=true`) — Phase 2 must not silently reduce these.
- The new `CLAUDE.md` gotcha entry exists and cites T2-BS-3.
- `LedgerEntry.SourceReliability` field is present in `internal/core/entities/adjustment_ledger.go`.

---

## 3. Adjuster Interface Design

### 3.1 Interface signature

```go
// internal/services/datacleaner/adjustments/adjuster.go (NEW)

// Adjuster is the unified contract every cleaner-side adjustment rule implements.
// Restater / Overlay / Hybrid roles emerge from the *shape* of the returned
// AdjusterOutput, NOT from a self-declared role enum:
//
//   - Pure Restater:      Output.LedgerEntries non-empty, Output.Overlays empty.
//   - Pure OverlayEmitter: Output.LedgerEntries empty, Output.Overlays non-empty.
//   - Hybrid:              Both non-empty.
//   - No-op (skipped):     A single LedgerEntry with Fired=false (records why).
//
// This matches the spec §"Adjuster output" lines 70-92 design intent: ONE
// interface, not three. The role distinction is observable but not declared.
//
// Implementations:
//
//   - MUST NOT panic. Return an error or a no-op LedgerEntry.
//   - MAY mutate `working` (dual-write Phase 2 invariant — existing in-place
//     mutations remain). Phase 3 will switch to no-mutation; do NOT preemptively
//     stop mutating in Phase 2.
//   - MUST be deterministic given (working snapshot at entry, rule, ctx).
//   - MUST be idempotent: calling Apply twice on identical (working, rule, ctx)
//     produces two LedgerEntries with identical fields except Timestamp.
//     Mutation effect on `working` is NOT idempotent (the second call sees the
//     mutated state) — this is intentional dual-write behavior.
//   - MUST NOT read or write `working.AdjustmentLedger` or `working.Overlays`.
//     Those fields are owned by the orchestrator; the adjuster only returns
//     entries.
type Adjuster interface {
    // Name returns the stable adjuster identifier (e.g. "A1_goodwill_exclusion").
    // Used as LedgerEntry.AdjusterID and as the WARN-log enrichment field for
    // the Phase 1 recomputeUmbrellas observer (when ledger is non-empty).
    Name() string

    // Apply runs the adjuster against `working` and returns the ledger entries
    // + overlays + flags it produced. ctx carries request-scoped state
    // (request_id propagated via logctx.From(ctx)).
    //
    // Phase 2 dual-write: implementations also mutate `working` in place
    // exactly as today (data.TotalAssets -= X, data.TotalDebt += X, etc.) to
    // preserve bit-for-bit downstream behavior. Phase 3 deletes the mutations.
    Apply(ctx context.Context, working *entities.FinancialData, rule *entities.CleaningRule, cleaningCtx *entities.CleaningContext) (AdjusterOutput, error)
}

// AdjusterOutput is the return shape. Zero-value is a valid no-op: empty
// LedgerEntries, empty Overlays, empty Flags, no error.
type AdjusterOutput struct {
    LedgerEntries []entities.LedgerEntry
    Overlays      []entities.OverlaySpec
    Flags         []entities.Flag
}
```

**Why one interface, not three:** Spec §"Adjuster output" line 87-92 explicitly says "One interface, three roles emerging from the output shape." GPT-5.5-pro deep-analysis pass (cited in spec change log 2026-05-15) corrected an earlier multi-interface design. Multi-interface duplicates the orchestrator dispatch logic; output-shape role inference does not. Re-litigating is not in scope.

**Why `error` return:** Today `ProcessOperatingLeaseAdjustment` at `liabilities.go:107-114` has a fallback path on calculator failure. Lifting the error to the interface lets callers decide whether to no-op or fall back. The B1 fallback at `liabilities.go:226-284` (`fallbackToSimpleCapitalization`) becomes either a separate Adjuster (B1-fallback) or stays inline as a private method; the implementer chooses, document the choice in PR-3.

**Why `Name() string` separately:** the orchestrator at `service.go:430-501` selects adjusters by `rule.ID` today (e.g. `case "goodwill_exclusion":` at `assets.go:369`). The new pattern moves to `adjusterByID map[string]Adjuster` — `Name()` is the registry key. It also stamps every `LedgerEntry.AdjusterID`, which is the WARN-log correlation field.

### 3.2 `LedgerEntry` struct

```go
// internal/core/entities/adjustment_ledger.go (NEW)

// LedgerEntry records a single adjuster decision — fired OR skipped. Skipped
// entries (Fired=false) preserve observability for "why didn't A1 fire on this
// ticker?" questions without requiring code reading.
//
// Field invariants:
//
//   - Timestamp, AdjusterID, RuleID, Fired: always populated.
//   - Component, DeltaAmount, EquityOffset, TaxShieldDTA: populated ONLY when
//     Fired=true AND the entry represents a Restater (component mutation).
//     Overlay-only entries (e.g., A1 goodwill exclusion in Phase 2 dual-write
//     mode where it ALSO mutates TotalAssets) leave these zero.
//   - SkipReason, SkipMetrics: populated ONLY when Fired=false.
//   - Reasoning: free-text audit string, always non-empty (mirrors today's
//     Adjustment.Reasoning).
//   - SourceReliability: defaults to "high"; set to "parser_known_dropout"
//     when the adjuster touches a field known to be parser-dropout-affected
//     (Phase 2 hook for T2-BS-3 carve-out; see §2 Option B contingency).
//
// LedgerEntry intentionally does NOT carry a Role enum. The role (Restater /
// Overlay / Hybrid) is observable by inspecting which fields are populated
// AND whether the AdjusterOutput that produced this entry also carried
// OverlaySpec entries. The role is a runtime classification of the
// AdjusterOutput as a whole, not a self-declared property of each entry.
//
// Spec anchor: spec §"Ledger entry" lines 95-118.
type LedgerEntry struct {
    // Always populated:
    Timestamp    time.Time `json:"timestamp"`
    AdjusterID   string    `json:"adjuster_id"`   // e.g. "A5_inventory_writedown"
    RuleID       string    `json:"rule_id"`       // e.g. "obsolete_inventory"
    Fired        bool      `json:"fired"`
    Reasoning    string    `json:"reasoning"`

    // Populated when Fired=true (component mutation):
    Component    string    `json:"component,omitempty"`     // e.g. "Inventory"
    DeltaAmount  float64   `json:"delta_amount,omitempty"`  // signed; e.g. -34_336_000
    EquityOffset float64   `json:"equity_offset,omitempty"` // signed; flows to Restated.StockholdersEquity (Phase 3)
    TaxShieldDTA float64   `json:"tax_shield_dta,omitempty"`// signed; flows to DeferredTaxAssets (Phase 3)

    // Populated when Fired=false (observability):
    SkipReason   string                 `json:"skip_reason,omitempty"`  // e.g. "goodwill_ratio=4.2% below 5% threshold"
    SkipMetrics  map[string]float64     `json:"skip_metrics,omitempty"` // e.g. {"goodwill_ratio": 0.042, "threshold": 0.05}

    // T2-BS-3 carve-out hook (§2 Option B):
    SourceReliability string `json:"source_reliability,omitempty"` // "high" | "medium" | "parser_known_dropout"; defaults to "high"
}
```

**Why `Fired bool` instead of `Status string` enum:** Spec §"Ledger entry" line 120 explicitly chose bool: "Fired is a bool (per design discussion): skipped rules still receive a ledger entry so observability can answer 'why didn't A1 fire on this ticker?' without code reading." Re-litigating is not in scope.

**Why no `Role` enum on LedgerEntry:** see §3.4 (Role taxonomy is derived, not declared).

**Why `EquityOffset` and `TaxShieldDTA` are populated in Phase 2 even though no consumer reads them yet:** Phase 3's `Restated()` accessor consumes them. Populating in Phase 2 means Phase 3 BACKEND can ship the accessor without re-walking every adjuster. Empty fields are zero-cost (JSON `omitempty`).

### 3.3 `OverlaySpec` and `AdjustmentLedger`

```go
// AdjustmentLedger is a slice newtype — NOT a struct — to keep the surface
// minimal in Phase 2. Phase 3 may wrap it in a struct when adding methods
// like `Restated()`; for now ordering invariance is the only contract.
type AdjustmentLedger []LedgerEntry

// AmountSemantics distinguishes how an OverlaySpec's Amount interacts with
// the target Field's current value. Spec §"Overlay specification" lines
// 137-142.
type AmountSemantics string

const (
    AmountIncremental AmountSemantics = "incremental" // amount adds on top of current value
    AmountReplacement AmountSemantics = "replacement" // amount replaces current value
    AmountDelta       AmountSemantics = "delta"       // amount is a relative delta vs current value
)

// OverlaySpec is a declarative analytical-overlay record. Spec §"Overlay
// specification" lines 124-152. Phase 2 collects these but does NOT apply them
// to any view (no Restated/InvestedCapital exists yet). Phase 3's view
// reconstruction consumes them.
//
// AIProvenance is populated only when the AI service produced the value
// (B3 contingent liability AI path at liabilities.go:622-701). Phase 2 leaves
// the struct exposed but uses only the existing analyzeContingentLiabilityWithAI
// metadata propagation — no new AI-side work in Phase 2.
type OverlaySpec struct {
    OverlayID       string          `json:"overlay_id"`       // e.g. "B1_operating_lease_capitalization"
    RuleID          string          `json:"rule_id"`
    Field           string          `json:"field"`            // e.g. "TotalDebt", "DebtLikeClaims"
    Operation       string          `json:"operation"`        // "add" | "subtract" | "zero"
    Amount          float64         `json:"amount"`
    AmountSemantics AmountSemantics `json:"amount_semantics"`
    Reasoning       string          `json:"reasoning"`
    AIProvenance    *AIProvenance   `json:"ai_provenance,omitempty"` // nil unless AI-derived (B3 only)
}

// AIProvenance captures the deterministic hash trail for AI-derived overlay
// amounts so replay golden bundles stay reproducible. Populated only by the
// B3 contingent-liability AI path. Phase 2 stamps the fields from the
// existing ai.AnalyzeFootnote metadata; no new AI integration work.
//
// Spec §"Overlay specification" lines 144-152.
type AIProvenance struct {
    ModelName     string    `json:"model_name"`      // "claude-haiku-4-5-20251001"
    PromptHash    string    `json:"prompt_hash"`     // sha256 of prompt template
    SourceDocHash string    `json:"source_doc_hash"` // sha256 of footnote text consumed
    ExtractedSpan string    `json:"extracted_span"`  // exact text span the AI processed
    Probability   float64   `json:"probability"`     // AI's output
    Confidence    float64   `json:"confidence"`      // AI's confidence
    Timestamp     time.Time `json:"timestamp"`
}
```

### 3.4 `FinancialData` field additions

```go
// internal/core/entities/financial_data.go — INSERT after line 137 (after the
// existing Phase 0 plug fields, before the TotalLiabilities field at line 145):

// AdjustmentLedger captures the chronological sequence of adjuster decisions
// (fired + skipped) applied to this FinancialData instance during cleaning.
// DC-1 Phase 2 — see docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md.
//
// Phase 2 invariant: the ledger is POPULATED but NO downstream consumer reads
// it yet. Phase 3 introduces the CleanedFinancialData.Restated() accessor that
// consumes ledger entries to reconstruct equity offsets and tax shields.
//
// Ordering: entries are appended in adjuster execution order
// (assetAdjuster → liabilityAdjuster → earningsAdjuster, mirroring
// service.go::applyActiveAdjustments). Within each adjuster, entries follow
// rule order from the rules engine. The ordering IS the contract — Phase 3's
// view reconstruction depends on chronological replay.
//
// Mutation: only `applyActiveAdjustments` appends to this slice; no adjuster
// reads or writes it directly. The pointer-to-slice on FinancialData makes
// Phase 3's "deep copy preserves ledger" semantics trivial.
//
// JSON serialization: emitted as `adjustment_ledger` (snake_case per project
// convention). Replay-bundle consumers and SQLite persistence (Phase 3+) read
// this shape directly.
AdjustmentLedger AdjustmentLedger `json:"adjustment_ledger,omitempty"`

// Overlays captures declarative overlay specifications emitted by Overlay-role
// adjusters (A1 goodwill exclusion, B1 operating-lease capitalization, B2
// pension underfunding, B3 contingent liabilities). DC-1 Phase 2.
//
// Phase 2 invariant: POPULATED but INERT. No consumer applies these overlays;
// Phase 3's InvestedCapital() accessor will. Phase 2 still routes B1/B2/B3
// amounts into data.TotalDebt directly (dual-write) so today's WACC and
// EV→Equity bridge math is bit-for-bit unchanged.
//
// Ordering: same as AdjustmentLedger.
Overlays []OverlaySpec `json:"overlays,omitempty"`
```

**Why slice on `FinancialData` and not a wrapping struct:** wrapping would require migrating every consumer to a new type signature. That's Phase 3's job. Phase 2 just adds two fields. JSON `omitempty` keeps the existing serialization shape for callers that don't care.

**Why two separate fields (not one combined `Audit` struct):** spec §"Adjuster output" lines 81-92 explicitly separates `LedgerEntries` and `Overlays` because their downstream semantic destinations differ (`LedgerEntries` flow to `Restated`, `Overlays` flow to `InvestedCapital`). Combining them now would require splitting them in Phase 3. KISS — keep them separate from day one.

**Why insert BEFORE `TotalLiabilities`:** ordering match the Phase 0 plug field layout (lines 105-137). Existing `TotalLiabilities` stays at line 145+. No struct-field-offset reordering risks for existing JSON/SQL serializers.

### 3.5 Role taxonomy (derived from output shape)

The role of an `AdjusterOutput` is computed at the orchestrator, not declared by the adjuster. Decision table:

| `LedgerEntries` non-empty? | `Overlays` non-empty? | Role |
|---|---|---|
| Yes | No  | Pure Restater |
| No  | Yes | Pure OverlayEmitter |
| Yes | Yes | Hybrid |
| No  | No  | No-op (adjuster evaluated, decided not to fire). Output has one synthetic `LedgerEntry{Fired:false, SkipReason:...}` per spec §"Ledger entry" line 120. So this row in the table is unreachable in practice — every evaluated adjuster emits at least one entry. |

Pseudocode at the orchestrator (this lives in `service.go::applyActiveAdjustments` post-refactor):

```go
// (Pseudocode — NOT production code. Production wiring lives in §7 Task 4.)
output, err := adjuster.Apply(ctx, data, rule, cleaningCtx)
if err != nil {
    return nil, nil, 0, fmt.Errorf("adjuster %s failed: %w", adjuster.Name(), err)
}
// Append in execution order. Role is derived purely from output shape.
data.AdjustmentLedger = append(data.AdjustmentLedger, output.LedgerEntries...)
data.Overlays         = append(data.Overlays,         output.Overlays...)
allFlags              = append(allFlags,              output.Flags...)
// LedgerEntry-Overlay correlation is implicit via shared (AdjusterID, RuleID, Timestamp).
```

### 3.6 Invariants

1. **Ledger ordering is execution order.** assets → liabilities → earnings; within each, rule-engine order. Phase 3 view reconstruction depends on this. Do NOT sort by AdjusterID alphabetically or any other key.
2. **Adjusters MUST NOT read or write `data.AdjustmentLedger` or `data.Overlays`.** The orchestrator owns these fields. Adjusters return entries; the orchestrator appends. Violating this breaks Phase 3's "ledger as authoritative replay log" property.
3. **No mutation of inputs (other than `working`).** `rule *entities.CleaningRule` is passed by pointer for compatibility with existing call sites at `assets.go:38,112,197,272` etc.; adjusters MUST NOT mutate rule fields. `cleaningCtx *entities.CleaningContext` may be mutated only via the existing `AIMetadata` channel (existing behavior at `liabilities.go:394-399`).
4. **Idempotency on the OUTPUT, not on `working`.** Two calls to `Apply(ctx, working, rule, cleaningCtx)` with identical inputs produce two identical `AdjusterOutput`s modulo `Timestamp`. The `working` mutation effect IS NOT idempotent — second call sees already-mutated `working` — this is intentional and matches today's behavior (calling `ProcessGoodwillAdjustment` twice on the same `data` zeros `Goodwill` once and then no-ops because `data.Goodwill <= 0` on second call). DO NOT add idempotency guards.
5. **Dual-write preservation.** The existing `data.X = Y` / `data.X += Z` mutations stay in place. New `LedgerEntry`/`OverlaySpec` records are emitted IN ADDITION. Phase 3 deletes the mutations after `Restated()` ships.
6. **`Fired=false` entries carry no monetary deltas.** A skipped adjuster MUST emit a `LedgerEntry` with `Fired:false, SkipReason:..., SkipMetrics:...` and zero `DeltaAmount`/`EquityOffset`/`TaxShieldDTA`. Tested by §5.
7. **`recomputeUmbrellas` (Phase 1 shim) is not modified in Phase 2.** Its WARN stream remains the regression signal. Optionally — but NOT required for Phase 2 close — the WARN can be enriched with `recent_adjusters []string` listing the AdjusterIDs of the last N ledger entries; this is a 3-line change to `recompute.go::emitIfDiverged` and is OFFERED in PR-1 as an opt-in enhancement (see §7 Task 1.6). Phase 1's `TestRecomputeUmbrellas_NoMutation` test stays green either way.

### 3.7 Observability / logging surface

- **Phase tag:** Phase 1 uses `phase: "DC-1-P1-shadow"` in WARN logs. Phase 2 does NOT introduce a new phase tag for the WARN stream — the shim is untouched. New logs emitted by the orchestrator (e.g., when appending a ledger entry) use `phase: "DC-1-P2-ledger"` IF AND ONLY IF the orchestrator emits any DEBUG/INFO line at all. Default: silent. Don't add log spam for every ledger entry; the ledger itself is the audit trail.
- **Adjuster-side logging:** existing log lines in `liabilities.go::ProcessOperatingLeaseAdjustment` (e.g. the calculator-failure path) remain unchanged. Adjusters use `logctx.From(ctx)` per CLAUDE.md request-path convention.
- **Phase 1 observer survives side-by-side.** The `recomputeUmbrellas` call at `service.go:242` stays. Its purpose shifts from "discovery" (Phase 1) to "regression sentinel" (Phase 2) — predicted divergences should DECREASE as adjusters reroute (per shadow-analysis Cluster A1-A5 disposition), and the snapshot diff IS the success signal.

---

## 4. Per-Adjuster Migration Map

For each shadow-analysis cluster, the table maps cluster → adjuster(s) → expected role → predicted snapshot diff after Phase 2 dual-write lands. **In Phase 2 dual-write mode, the cluster's snapshot diff is observable but inert — the WARN still fires because the mutation still happens. Phase 3 is where the WARN actually goes away.** This is critical for REVIEWER: Phase 2's success signal is the LEDGER being populated correctly, NOT the WARN stream shrinking.

| Cluster # | Cluster name | Adjuster(s) touched | Today's behavior (file:lines) | Phase 2 role | Phase 2 snapshot diff prediction | Phase 3 snapshot diff prediction |
|---|---|---|---|---|---|---|
| 1 | **A1-A5 paired CA-down / TA-up** | A1 (goodwill exclusion) | `assets.go:38-109` — `data.Goodwill=0; data.TotalAssets -= originalGoodwill` | OverlayEmitter (also dual-write mutates TA — Phase 3 deletes the mutation) | UNCHANGED — TA divergence still fires because dual-write still mutates TA. New: AdjustmentLedger has 1 entry per fired A1, Overlays has 1 OverlaySpec{OverlayID:"A1_goodwill_exclusion", Field:"TotalAssets", Operation:"subtract", AmountSemantics:Incremental} | RESOLVED — Phase 3's Restated() recomputes TA as sum(components) + plug; A1's overlay applies only to InvestedCapital.TotalAssets. Per shadow-analysis Cluster A1-A5 disposition. |
| 1 | (same) | A2 (intangible writedown) | `assets.go:112-194` — `data.OtherIntangibles=retained; data.TotalAssets -= writedown` | Restater (component-only logical intent; dual-write also still mutates TA) | Same shape as A1: LedgerEntry{Component:"OtherIntangibles", DeltaAmount:-writedown, EquityOffset:-writedown}. TA divergence WARN still fires. | RESOLVED — recompute closes the asymmetry. |
| 1 | (same) | A4 (DTA valuation allowance) | `assets.go:271-349` — `data.DeferredTaxAssets=adjusted; data.TotalAssets -= allowance; data.ValuationAllowance += allowance` | Restater | LedgerEntry{Component:"DeferredTaxAssets", DeltaAmount:-allowance, EquityOffset:-allowance}. Note A4 ALSO mutates `data.ValuationAllowance` at `assets.go:309`; keep that mutation unchanged (it's not a component sum target). | Same. |
| 1 | (same) | A5 (inventory writedown) | `assets.go:196-269` — `data.Inventory -= writedown; data.TotalAssets -= writedown` | Restater + TaxShieldDTA | LedgerEntry{Component:"Inventory", DeltaAmount:-writedown, EquityOffset:-writedown, TaxShieldDTA: writedown * data.EffectiveTaxRate}. Computing the tax shield from `data.EffectiveTaxRate` (Phase 0 field) — if zero, omit (set to 0.0). Today's code doesn't compute a tax shield; this is new. | RESOLVED — CA divergence ALSO closes because Phase 3 recomputes CA = Cash + Inventory + OtherCurrentAssets from mutated Inventory. |
| 2 | **B1 lease-cap TL drift** | B1 (operating leases) | `liabilities.go:107-224` + orchestrator at `:87-88` adds Amount to `data.TotalDebt` AND `data.InterestBearingDebt` | OverlayEmitter | OverlaySpec{OverlayID:"B1_operating_lease_capitalization", Field:"TotalDebt", Operation:"add", AmountSemantics:Incremental, Amount:presentValue}. Phase 2 dual-write keeps `data.TotalDebt += Amount` at `liabilities.go:87` exactly as today. TL divergence WARN still fires. | RESOLVED — Phase 3 routes B1's value into InvestedCapital.CapitalStructureDebt; Restated.TotalDebt loses the lease portion; TL divergence disappears. Per shadow-analysis Cluster B1 disposition. |
| 2 | (same) | B2 (pension/OPEB) | `liabilities.go:287-359` + orchestrator at `:87-88` | OverlayEmitter | OverlaySpec{OverlayID:"B2_pension_underfunding", Field:"TotalDebt", Operation:"add", AmountSemantics:Incremental}. Dual-write unchanged. | Same as B1. |
| 2 | (same) | B3 (contingent liabilities) | `liabilities.go:362-456` + orchestrator at `:87-88` | OverlayEmitter (Field: `"DebtLikeClaims"` per spec §"B3 routing correction" lines 181-189) | OverlaySpec{OverlayID:"B3_contingent_liabilities", Field:"DebtLikeClaims", Operation:"add", AmountSemantics:Incremental, AIProvenance:non-nil when AI fired}. **Critical:** Phase 2 dual-write STILL mutates `data.TotalDebt += Amount` at `:87` for B3 (because Phase 2 changes zero downstream behavior). The OverlaySpec's `Field:"DebtLikeClaims"` is the FUTURE Phase 4 routing — recorded today, inert today. | Phase 3 RESOLVED for B1/B2; Phase 4 FLIPS B3 routing — WACC weights shift on filers with material B3. This is the spec's consumer-visible accuracy change, deliberately deferred. |
| 3 | **B1-PARSER-TL-ZERO (AMD/KO)** | (parser-side) | `internal/infra/gateways/sec/parser.go::findValue` for `Liabilities` tag | N/A (parser, not cleaner) | UNCHANGED per §2 Option B. AMD.json and KO.json shadow snapshots stay byte-for-byte identical (modulo determinism noise). | Phase 3's Restated reconstructs TL from sum(components) + plug, hiding the parser dropout from `Restated`. AsReported still surfaces zero (T2-BS-3 carve-out). |
| 4 | **A-FY-NULL** | A1/A2/A4/A5 enable predicates on FY periods | `service.go::checkRuleApplicability` at `:503` + rule thresholds | N/A (rule-engine input; not adjuster shape) | UNCHANGED. Phase 2's refactor does not touch rule-applicability logic. Cluster A-FY-NULL stays open. | NO change in Phase 3 either. Filed as a Phase 2 follow-up sub-task: "verify A1/A2/A4/A5 enable predicates on FY periods to confirm whether cleaner is no-op-ing or applying-without-emitting" per shadow-analysis disposition. Filed in §10 Open Questions Q3 below. |
| 5 | **CL-NULL** | (none — `CurrentLiabilities` has no adjuster) | N/A | N/A | UNCHANGED. CL still emits zero divergences. | Phase 3 resolves once parser learns lease-split decomposition (deferred). |
| 6 | **F-MSFT-LARGE-PLUG** | (diagnostic supplement of Cluster B1) | Same as Cluster B1 | Same as B1 | Same as B1 (LedgerEntry/OverlaySpec emitted; WARN still fires in Phase 2 dual-write). | Same as B1 — Phase 3 resolves WARN; large plug informs future parser-side typed-component expansion (not Phase 2 scope). |
| 7 | **ZERO-DIVERGENCE** | (none — sanity check) | N/A | N/A | No-op. | No-op. |

### Migration ordering (which adjuster gets refactored first, and why)

Sequencing forced by dependencies:

1. **Interface + ledger entity (PR-1).** Nothing else compiles without `Adjuster`, `LedgerEntry`, `OverlaySpec`, `AdjusterOutput`, `AmountSemantics`, `AIProvenance`, `AdjustmentLedger` types and the orchestrator scaffolding. This PR ships the types, the orchestrator's new dispatch loop (still calling the existing `ProcessGoodwillAdjustment`/etc. methods via thin adapter shims), and a property test pinning the equity-offset invariant for zero-adjuster case (trivial: empty ledger).
2. **Assets adjusters (PR-2).** A1, A2, A4, A5 in `assets.go`. Reason: smallest blast radius (no AI integration; no calculator service; no orchestrator-level `data.TotalDebt +=` side effect). Tests are mature (`assets_test.go` is well-populated). Cluster A1-A5 ledger entries become observable; predicted snapshot diff: AMD/F/KO/MXL `.json` files gain new `clamp_suspected` / `ledger_correlated` cross-reference fields (if the snapshot test is extended — see §5).
3. **Earnings adjusters (PR-3).** C1-C7 in `earnings.go`. Reason: orthogonal to balance-sheet — they mutate `NormalizedOperatingIncome` and `InterestExpense` (see grep at `earnings.go:112,154,214,313,316,360`), not the umbrellas the Phase 1 shim recomputes. So Cluster A1-A5 / B1 snapshot diffs are unaffected; only `data.AdjustmentLedger` length changes. Lower review risk; serves as a "ship the interface end-to-end against C-rules" rehearsal before the B-rule risk in PR-4.
4. **Liabilities adjusters (PR-4).** B1, B2, B3 in `liabilities.go`. Reason: highest risk — the orchestrator-level `data.TotalDebt += result.Amount` at `liabilities.go:87-88` is the load-bearing dual-write site, AND B3's AI path is the only adjuster with `AIProvenance` capture. Land last so PR-2/PR-3's stability gives confidence in the interface, and so any unforeseen interface revision happens before B-rule touch. T2-BS-3 carve-out documentation (§2 Option B) lands in this PR's CLAUDE.md gotcha update.

**Why earnings before liabilities (PR-3 before PR-4):** C-rule adjusters touch income-statement fields only (`NormalizedOperatingIncome`, `InterestExpense`) which the Phase 1 recompute shim does NOT observe. So PR-3 is a "ship the interface, exercise the orchestrator, validate ledger population" PR with zero predicted snapshot drift. PR-4's liabilities touch the high-risk surface; landing PR-3 first gives a known-good interface to refactor B-rules against.

---

## 5. Test Strategy

### 5.1 Bit-for-bit invariant

The dual-write design (§3.6 invariant 5) means Phase 2 ships ZERO downstream behavior change by construction. Two layered regression gates:

| Gate | Test | What it pins | Run frequency |
|---|---|---|---|
| **G1** | `TestDDM_LegacyPath_BitForBit` in `internal/services/valuation/models/ddm_bitforbit_test.go` | `math.Float64bits` equality on JPM/BAC/WFC DDM `IntrinsicValuePerShare`/`EquityValue`/`EnterpriseValue` against pre-Tier-2 goldens at `testdata/golden/{jpm,bac,wfc}_ddm_pre_tier2_{input,output}.json`. CLAUDE.md flags this as LOAD-BEARING. | After EVERY BACKEND task per §7, before VERIFIER hand-off. **MUST pass green.** Revert any commit that breaks it; never update goldens. |
| **G2** | Existing adjuster table-driven tests: `internal/services/datacleaner/adjustments/{assets_test.go,liabilities_test.go,earnings_test.go,integration_test.go,real_sec_data_test.go}` | The existing `*AdjustmentResult` shape (Amount, Applied, Adjustments, Flags, Reasoning fields). Dual-write means these tests stay GREEN unchanged. | Before each PR's VERIFIER stage. |
| **G3** | Replay validation: `go run ./cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/2026-05-19/` against AAPL, MSFT, JPM (Tier 2 DDM ticker) | The `17-response.json` `valuation_summary` block stays byte-for-byte identical modulo timestamps. | Before each PR's merge approval. |

### 5.2 New tests required (per spec §"Testing strategy" lines 451-503)

| Test | Location | What it pins | Phase |
|---|---|---|---|
| `TestAdjuster_Interface_Contract` | `internal/services/datacleaner/adjustments/adjuster_test.go` (NEW) | Every concrete adjuster implements the `Adjuster` interface. Trivial type-assertion table test. | PR-1 |
| `TestLedgerEntry_FiredFalse_NoMonetaryDeltas` | `internal/core/entities/adjustment_ledger_test.go` (NEW) | When `Fired=false`, `DeltaAmount==0 && EquityOffset==0 && TaxShieldDTA==0 && SkipReason != ""`. (Invariant §3.6.6.) | PR-1 |
| `TestLedgerEntry_FiredTrue_HasReasoning` | (same file) | When `Fired=true`, `Reasoning != ""` (mirrors today's adjuster contract — every fired adjustment has a reasoning string). | PR-1 |
| `TestOrchestrator_LedgerOrdering` | `internal/services/datacleaner/adjustments/ledger_invariants_test.go` (NEW) | After `applyActiveAdjustments(ctx, data, cleaningCtx)`, `data.AdjustmentLedger` entries appear in execution order: all asset-adjuster entries first, then liability-adjuster, then earnings-adjuster. Within each, rule-engine order. (Invariant §3.6.1.) | PR-1 + extended in PR-2/3/4 |
| `TestOrchestrator_NoLedgerReadsBeforeOrchestrator` | (same file) | Static `go vet`-style check or runtime guard: no adjuster reads `working.AdjustmentLedger` during `Apply`. Simplest implementation: a runtime check inside each adjuster that asserts `len(working.AdjustmentLedger) == 0` is impossible (because adjusters can run sequentially with growing ledger), so this check is via code review only — REVIEWER checklist item. **No automated test for this**; document the invariant in adjuster godoc and call it out in CODEOWNERS-style PR template. | PR-1 (documentation only) |
| `TestRestater_EquityOffsetZeroSum` (property test) | `internal/services/datacleaner/adjustments/ledger_invariants_test.go` | For any well-formed `FinancialData` generator (gopter), `sum(LedgerEntries where Fired=true) of EquityOffset == sum of corresponding Component.DeltaAmount`. (I.e., every Restater that mutates Inventory by -X declares EquityOffset of -X — flows through retained earnings.) | PR-2 (Restater adjusters) |
| `TestOverlay_DoesNotMutateUmbrellaInLedgerPath` | (same file) | For OverlayEmitter-role outputs (A1, B1, B2, B3), the LedgerEntries (if any) have `EquityOffset=0` AND the dual-write mutation is the ONLY mechanism by which umbrellas change. In Phase 2 dual-write mode this is documentary; in Phase 3 it becomes a hard invariant. | PR-4 |
| `TestLedger_BasketSnapshot_ClusterPrediction` | `internal/integration/datacleaner_ledger_basket_test.go` (NEW) | For each of the 10 basket tickers, after running `CleanFinancialData`, assert that `data.AdjustmentLedger` contains the expected `AdjusterID` entries per cluster: AAPL → B1/B2/B3 entries fired; AMD → A1/A2/A5 + B1 fired (subject to thresholds); etc. **Use existing `internal/integration/testdata/recompute-shadow/<TICKER>.json` as the source of truth for which clusters fire on which tickers**; the new test asserts the ledger contains corresponding AdjusterIDs. | PR-4 (after all adjusters land) |
| `TestRecompute_LedgerEnrichment` (optional, see §3.6.7) | `internal/services/datacleaner/recompute_test.go` (MODIFY) | If PR-1 ships the WARN log enrichment, this test asserts WARN includes `recent_adjusters` field listing recent ledger AdjusterIDs. **Optional — skip if PR-1 doesn't ship enrichment.** | PR-1 (optional) |

### 5.3 Replay validation per PR

Before each PR merges:

```bash
# AAPL — Cluster B1 + A1-A5 high-fidelity ticker
go run ./cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/2026-05-19/AAPL/req_<uuid>/

# MSFT — Clean industrial B1 signal
go run ./cmd/replay --diff-stages --from=parsed artifacts/tier2-baseline/2026-05-19/MSFT/req_<uuid>/

# JPM — Tier 2 DDM bit-for-bit ticker (requires --allow-schema-drift due to JPM bundle missing 10-clean-output.json per CLAUDE.md)
go run ./cmd/replay --diff-stages --allow-schema-drift --from=parsed artifacts/tier2-baseline/2026-05-19/JPM/req_<uuid>/
```

Expected drift per PR:

- **PR-1** (interface skeleton): ZERO drift. No adjuster behavior changed.
- **PR-2** (assets): ZERO numeric drift in `17-response.json` (dual-write preserves outputs). New: `10-clean-output.json` snapshot's `FinancialData` carries `adjustment_ledger` and `overlays` fields with A-rule entries. Diff `--diff-view=raw` shows these added; `--diff-view=valuation` shows zero numeric drift.
- **PR-3** (earnings): same shape as PR-2 but C-rule entries.
- **PR-4** (liabilities): same shape; B-rule entries. **High-risk PR — run all 3 replay tickers; reject merge if any numeric drift appears.**

### 5.4 Coverage target

CLAUDE.md sets ≥90% for critical finance modules. Adjuster files (`assets.go`, `liabilities.go`, `earnings.go`) are critical finance modules. Target after Phase 2:

- `assets.go`: existing coverage from `assets_test.go` is high; new tests in `ledger_invariants_test.go` add ledger-population coverage. Maintain ≥90%.
- `liabilities.go`: same — `liabilities_test.go` is well-populated; B1 has the AI path which already has separate tests. Maintain ≥90%.
- `earnings.go`: same — `earnings_test.go` covers all 7 C-rules. Maintain ≥90%.
- `adjuster.go` (NEW): the interface file is mostly types + godoc. Test coverage of the file itself is trivial; the contract is exercised by every adjuster's test. Target ≥80%.
- `adjustment_ledger.go` (NEW entity): simple struct + invariant tests. Target ≥90%.

Run after each PR:

```bash
go test -coverprofile=cover.out ./internal/services/datacleaner/adjustments/... ./internal/core/entities/...
go tool cover -func=cover.out | grep -E '(adjuster|adjustment_ledger|assets|liabilities|earnings)\.go'
```

---

## 6. Rollback / Kill-Switch Strategy

### Decision: NO config flag in Phase 2.

**Argument considered:** Phase 1 was config-flag-gated in spirit (the shadow shim emits WARN but never mutates; the flag-equivalent is "no flag because output is unobservable to consumers"). Symmetry would suggest Phase 2 ships behind a flag like `DATACLEANER_ENABLE_LEDGER`.

**Argument against (chosen):** Phase 2 is dual-write by design (§3.6 invariant 5) — the existing mutations stay, the new ledger appends. There IS no behavior to gate. A flag would have one of two effects:

1. **Flag=off skips the ledger append:** the only observable change is `data.AdjustmentLedger == nil`. No consumer reads the ledger in Phase 2 anyway. The flag would just be dead code that Phase 3 has to clean up before consuming the ledger.
2. **Flag=off skips the interface refactor entirely:** impossible — the interface IS the refactor. Half-refactored adjusters don't compile.

So a flag would be cosmetic at best, dead-code-debt at worst. KISS.

### Revert path if a bug ships

- **PR-1 bug (interface design):** single PR revert. The orchestrator dispatch is the only thing that changed; reverting restores the prior `assetAdjuster.ProcessAssetAdjustments(...)` call shape. Time-to-revert: ~30 min.
- **PR-2/PR-3/PR-4 bug (per-adjuster refactor):** revert the specific PR; the adjuster-refactor commits are mutually independent (each PR refactors a disjoint set of files). Time-to-revert: ~15 min per PR.
- **Compound bug spanning PRs (e.g., interface signature requires change):** revert PRs in LIFO order. Worst case: revert PR-4, PR-3, PR-2, PR-1 sequentially — ~2 hr total. The dual-write design means even after full revert, production behavior is identical to pre-Phase-2 master.
- **Tier 2 DDM bit-for-bit regression (G1):** highest-priority revert path. Immediate revert; do NOT attempt forward-fix. The bit-for-bit invariant is so load-bearing per CLAUDE.md that any minute spent forward-fixing is risk. After revert, root-cause separately, then re-attempt with the fix baked in.

---

## 7. Tasks by Agent (BACKEND breakdown)

BACKEND executes tasks top-to-bottom. Each task is 1-4 hrs of focused work with explicit acceptance signals. PR boundaries are noted; ship one PR per phase boundary.

### PR-1 — Interface + entity skeleton (~1 agent shift, ~7 commits)

#### Task 1.1 — Add `LedgerEntry`, `OverlaySpec`, `AdjustmentLedger`, `AmountSemantics`, `AIProvenance` entities
- **Files:**
  - Create: `internal/core/entities/adjustment_ledger.go`
  - Create: `internal/core/entities/adjustment_ledger_test.go`
- **Sub-steps:**
  - Write `LedgerEntry`, `OverlaySpec`, `AdjustmentLedger`, `AmountSemantics`, `AIProvenance` per §3.2-3.3 verbatim.
  - Write unit tests: `TestLedgerEntry_FiredFalse_NoMonetaryDeltas`, `TestLedgerEntry_FiredTrue_HasReasoning`, `TestOverlaySpec_AmountSemantics_Validate`, `TestAmountSemantics_StringConstants`.
- **Acceptance signal:** `go test ./internal/core/entities/... -run TestLedgerEntry -count=1` passes. `go vet ./internal/core/entities/...` clean.

#### Task 1.2 — Append `AdjustmentLedger` and `Overlays` fields to `entities.FinancialData`
- **Files:**
  - Modify: `internal/core/entities/financial_data.go` — insert at line ~138 (after existing Phase 0 plug fields, before `TotalLiabilities` at line 145)
- **Sub-steps:**
  - Add `AdjustmentLedger AdjustmentLedger` and `Overlays []OverlaySpec` fields per §3.4 verbatim.
  - Add godoc comments per §3.4 verbatim.
- **Acceptance signal:** `go build ./...` succeeds. Existing tests in `internal/core/entities/...` stay green (the new fields are zero-value-safe).

#### Task 1.3 — Define the `Adjuster` interface and `AdjusterOutput`
- **Files:**
  - Create: `internal/services/datacleaner/adjustments/adjuster.go`
  - Create: `internal/services/datacleaner/adjustments/adjuster_test.go`
- **Sub-steps:**
  - Write `Adjuster` interface and `AdjusterOutput` struct per §3.1 verbatim, including the full godoc.
  - Write `TestAdjuster_Interface_Contract` — currently asserts nothing concrete (no implementations exist yet); will be extended in PR-2/3/4. For PR-1, the test just type-asserts the interface compiles.
- **Acceptance signal:** `go build ./internal/services/datacleaner/adjustments/...` succeeds. `go test ./internal/services/datacleaner/adjustments/ -run TestAdjuster -count=1` passes (trivially).

#### Task 1.4 — Refactor orchestrator at `service.go::applyActiveAdjustments` to support the new interface (dual-path)
- **Files:**
  - Modify: `internal/services/datacleaner/service.go` lines 430-501
- **Sub-steps:**
  - Keep the existing three `ProcessXAdjustments` calls at lines 463, 473, 492 untouched.
  - Introduce ledger-collection scaffolding: after each `Process*` call, manually construct a `LedgerEntry` per applied adjustment in `result.Adjustments` and append to `data.AdjustmentLedger`. The construction is mechanical: map `entities.Adjustment` (existing shape at `assets.go:72-83`) → `entities.LedgerEntry` with `Fired:true, AdjusterID:adj.ID, RuleID:adj.RuleID, Reasoning:adj.Reasoning, Timestamp:adj.Timestamp, Component:adj.FromAccount, DeltaAmount:-adj.Amount` (sign convention: writedowns are negative deltas). For skipped adjusters (rules that returned `Applied:false`), construct `LedgerEntry{Fired:false, ...}` using the rule's reasoning string.
  - **This intermediate ledger-emission shim lives ONLY in PR-1.** PR-2/3/4 each migrate one adjuster file to emit `AdjusterOutput` directly; when the last migration lands in PR-4, the shim is deleted (or kept as a defensive fallback — implementer decides at PR-4 time).
- **Acceptance signal:** After running `CleanFinancialData` on any non-trivial FinancialData, `data.AdjustmentLedger` is non-nil and contains at least one entry per fired adjuster. `go test ./internal/services/datacleaner/... -count=1` stays green (existing tests don't read the ledger).

#### Task 1.5 — Property test: ledger ordering invariant
- **Files:**
  - Create: `internal/services/datacleaner/adjustments/ledger_invariants_test.go`
- **Sub-steps:**
  - Write `TestOrchestrator_LedgerOrdering` per §5.2. Generator: use existing test helpers in `assets_test.go` to build a `FinancialData` that triggers at least one fired adjuster per category. Assert: ledger entries are partitioned into 3 contiguous groups (asset adjusters first, liability second, earnings third). Within each group, AdjusterIDs match the rule-engine order from `s.rulesEngine.GetIndustryRules(cleaningCtx.IndustryCode)`.
- **Acceptance signal:** Test passes. Coverage report shows new entity and adjuster.go files at ≥80%.

#### Task 1.6 — `recomputeUmbrellas` WARN log enrichment with recent adjusters (REQUIRED per Q1 resolution 2026-05-21)
- **Files:**
  - Modify: `internal/services/datacleaner/recompute.go::emitIfDiverged` lines 116-149
  - Modify: `internal/services/datacleaner/recompute_test.go`
- **Sub-steps:**
  - Add a new param `recentAdjusters []string` to `emitIfDiverged` (call sites pass last 5 AdjusterIDs from `fd.AdjustmentLedger`).
  - Add `zap.Strings("recent_adjusters", recentAdjusters)` to the WARN line.
  - **Phase 1's `TestRecomputeUmbrellas_NoMutation` MUST stay green.**
- **Acceptance signal:** `go test ./internal/services/datacleaner/ -run TestRecomputeUmbrellas -count=1` passes. WARN line in production logs (when grepped via `rg '"phase":"DC-1-P1-shadow"'`) includes the new field. Promoted from OPTIONAL to required per Q1 resolution — must land in PR-1.

#### Task 1.7 — Documentation: CLAUDE.md DC-1 gotcha update + PR description boilerplate
- **Files:**
  - Modify: `CLAUDE.md` — extend the existing "DC-1 datacleaner refactor — Phase 1 SHIPPED" gotcha to add "Phase 2 IN-FLIGHT" sub-note pointing to this plan.
  - Modify: `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` change-log table — add `| 2026-05-XX | Phase 2 PR-1 SHIPPED: Adjuster interface + LedgerEntry/OverlaySpec entities + orchestrator scaffolding. Zero downstream behavior change (dual-write). Subsequent PRs migrate adjusters atop the interface. |`
  - Modify: `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` — Phase 2 PR-1 progress paragraph.
- **Acceptance signal:** Doc changes land in the same PR as the code so REVIEWER sees them together.

**PR-1 acceptance criteria (full):**
- All Task 1.1-1.5 acceptance signals green
- `TestDDM_LegacyPath_BitForBit` GREEN (G1)
- Full test suite green (`go test ./... -count=1`) modulo pre-existing SCHED-1 flake
- Coverage ≥90% for entities + adjuster.go ≥80%
- AAPL/MSFT/JPM replay shows zero numeric drift in `17-response.json`
- `internal/integration/testdata/recompute-shadow/<TICKER>.json` snapshots UNCHANGED (PR-1 ships zero adjuster behavior change)

### PR-2 — Asset adjusters migrated (~1 agent shift, ~7 commits)

#### Task 2.1 — Refactor A1 (goodwill exclusion) to implement `Adjuster`
- **Files:**
  - Modify: `internal/services/datacleaner/adjustments/assets.go` lines 38-109
  - Modify: `internal/services/datacleaner/adjustments/assets_test.go` — extend existing `TestProcessGoodwillAdjustment` cases to also assert `AdjusterOutput` shape
- **Sub-steps:**
  - Add a new method on `AssetAdjuster`: `func (aa *AssetAdjuster) ApplyA1Goodwill(ctx, working, rule, cleaningCtx) (AdjusterOutput, error)`. Wrap the existing `ProcessGoodwillAdjustment` body. Translate the existing `AdjustmentResult` to `AdjusterOutput`: when `Applied=true`, emit one `OverlaySpec{OverlayID:"A1_goodwill_exclusion", Field:"TotalAssets", Operation:"subtract", Amount:originalGoodwill, AmountSemantics:AmountIncremental, Reasoning:...}`. Phase 2 dual-write: keep `data.Goodwill = 0.0` and `data.TotalAssets -= originalGoodwill` mutations unchanged. When `Applied=false`, emit `LedgerEntry{Fired:false, AdjusterID:"A1_goodwill_exclusion", SkipReason:..., SkipMetrics:{"goodwill_ratio":goodwillRatio,"threshold":0.05}}`.
  - Implement `Name() string { return "A1_goodwill_exclusion" }`.
  - Wire into orchestrator: in `service.go::applyActiveAdjustments`, when the rule.ID is `"goodwill_exclusion"`, call `aa.ApplyA1Goodwill(ctx, data, rule, cleaningCtx)` instead of `aa.ProcessGoodwillAdjustment(data, rule)`. The shim from Task 1.4 still handles entries A2/A4/A5 in this PR (they migrate in 2.2-2.4).
- **Acceptance signal:** `TestProcessGoodwillAdjustment` cases all pass with extended ledger assertions. `TestDDM_LegacyPath_BitForBit` GREEN.

#### Task 2.2 — Refactor A2 (intangible writedown) to implement `Adjuster`
- **Files:** `internal/services/datacleaner/adjustments/assets.go` lines 112-194; `assets_test.go`
- **Sub-steps:** Same shape as 2.1 but the role is **Restater**. Emit `LedgerEntry{Fired:true, AdjusterID:"A2_intangible_writedown", Component:"OtherIntangibles", DeltaAmount:-writedownAmount, EquityOffset:-writedownAmount, Reasoning:...}`. No OverlaySpec. Dual-write: keep `data.OtherIntangibles = retainedAmount; data.TotalAssets -= writedownAmount` mutations.
- **Acceptance signal:** Same shape as 2.1.

#### Task 2.3 — Refactor A4 (DTA valuation allowance) to implement `Adjuster`
- **Files:** `internal/services/datacleaner/adjustments/assets.go` lines 271-349; `assets_test.go`
- **Sub-steps:** Restater. Emit `LedgerEntry{Fired:true, AdjusterID:"A4_dta_valuation_allowance", Component:"DeferredTaxAssets", DeltaAmount:-valuationAllowance, EquityOffset:-valuationAllowance}`. Dual-write: keep `data.DeferredTaxAssets = adjustedDTA; data.TotalAssets -= valuationAllowance; data.ValuationAllowance += valuationAllowance` mutations. Note: `data.ValuationAllowance += valuationAllowance` at line 309 is a separate field mutation (not a component-sum target); keep unchanged.
- **Acceptance signal:** Same shape.

#### Task 2.4 — Refactor A5 (inventory writedown) to implement `Adjuster` with TaxShieldDTA
- **Files:** `internal/services/datacleaner/adjustments/assets.go` lines 196-269; `assets_test.go`
- **Sub-steps:** Restater + TaxShieldDTA. Emit `LedgerEntry{Fired:true, AdjusterID:"A5_inventory_writedown", Component:"Inventory", DeltaAmount:-writedownAmount, EquityOffset:-writedownAmount, TaxShieldDTA: writedownAmount * data.EffectiveTaxRate}` (when `data.EffectiveTaxRate > 0`; else `TaxShieldDTA:0`). Dual-write: keep `data.Inventory -= writedownAmount; data.TotalAssets -= writedownAmount` mutations.
- **Acceptance signal:** Same shape, plus new test case `TestProcessInventoryAdjustment_TaxShieldDTA_PopulatedWhenEffectiveTaxRateNonZero`.

#### Task 2.5 — Refactor flag-only review functions (`ProcessRDCapitalizationReview`, `ProcessCapitalizedSoftwareReview`) to emit ledger entries
- **Files:** `internal/services/datacleaner/adjustments/assets.go` lines 412-509; `assets_test.go`
- **Sub-steps:** These functions return `Applied:false` always (flag-only). Wrap as `Adjuster` implementations that emit `LedgerEntry{Fired:false, AdjusterID:"A-rd_capitalization_review", SkipReason:"flag-only review; no balance-sheet adjustment", ...}` for observability. Even though they don't change numbers, recording them in the ledger answers "did the cleaner consider R&D capitalization for this ticker?" Spec §"Ledger entry" line 120 explicitly supports this pattern.
- **Acceptance signal:** Ledger entries for these reviews appear in `data.AdjustmentLedger` when the rule fires.

#### Task 2.7 — Cluster A-FY-NULL: verify A1/A2/A4/A5 enable predicates on FY periods (per Q3 resolution 2026-05-21)
- **Files:**
  - Create: `docs/reviewer/DC-1-FY-enable-predicate-investigation.md` (tracker; whether bug or by-design is the deliverable)
  - Read-only investigation: `internal/services/datacleaner/rules/` (rule-engine enable predicates), `internal/infra/gateways/sec/parser.go` (FY collapse logic), `internal/integration/testdata/recompute-shadow/{AMD,F,KO,MXL}.json` (the affected snapshots)
- **Sub-steps:**
  - For each of AMD / F / KO / MXL, identify why FY periods (2023FY, 2024FY, 2025FY) emit the Cluster B1 (or B1-PARSER-TL-ZERO) TL divergence but NOT the paired CA-down/TA-up A1-A5 pattern that quarterly periods emit.
  - Two hypotheses to confirm or refute: (a) rule-engine enable-predicate gates the A-rules on quarterly periods only; (b) SEC parser's FY collapse strips inputs the rules read (e.g., quarterly inventory writedown disclosures aggregate to annual differently).
  - Run `go test ./internal/services/datacleaner/ -run TestAdjusters -v -count=1` with a debug log added at each adjuster's enable check, capturing whether the rule was REACHED-and-SKIPPED vs NEVER-REACHED.
  - Document findings in the tracker. If hypothesis (a) is confirmed and the design intent is preserved, close the tracker. If hypothesis (b) is confirmed (parser bug), file as a Phase 4+ followup — DO NOT fix in Phase 2 (out of scope).
- **Acceptance signal:** Tracker file exists with a one-paragraph conclusion + cited evidence (debug log lines + which test produced them). No code changes in PR-2 result from this task — Phase 2 stays scope-tight.

#### Task 2.6 — Remove the PR-1 shim's asset-side fallback path
- **Files:** `internal/services/datacleaner/service.go::applyActiveAdjustments`
- **Sub-steps:** The Task 1.4 shim mapped `entities.Adjustment` → `entities.LedgerEntry` for all categories. After PR-2 lands, the asset path's shim entries are redundant (each A-rule emits its own `AdjusterOutput`). Delete the asset-side shim branch only; keep liability + earnings shim for PR-3/PR-4.
- **Acceptance signal:** Ledger entries from A-rules are sourced from `AdjusterOutput`, not the shim. Same count, same content.

**PR-2 acceptance criteria:**
- All Task 2.1-2.7 acceptance signals green (2.7 added per Q3 resolution; produces tracker, not code)
- `TestDDM_LegacyPath_BitForBit` GREEN
- AAPL/MSFT replay shows zero numeric drift; `10-clean-output.json` carries A-rule entries in `adjustment_ledger`
- `internal/integration/testdata/recompute-shadow/<TICKER>.json` snapshots UNCHANGED (dual-write preserves divergence pattern). REVIEWER confirms snapshot diffs are EMPTY in this PR.
- **SchemaVersion bump (per QA risk-surface 2026-05-22):** `CurrentSchemaVersions["FinancialData"]` MUST be incremented from 7 to 8 in the same PR that flips dual-write ON for any adjuster category. Atomic with the first populating commit. Artifact baselines at `artifacts/tier2-baseline/*/` MUST be refreshed in the same PR so that replay-tool output cleanly separates "structural schema drift" from "valuation math regression" — otherwise the replay diff becomes useless as a regression detector. The bump fires ONCE total (whichever of PR-2/3/4 lands first); PR-3 and PR-4 inherit `SchemaVersion=8`.

### PR-3 — Earnings adjusters migrated (~1 agent shift, ~9 commits — one per C-rule)

#### Task 3.1-3.7 — Refactor C1-C7 to implement `Adjuster`
- **Files:** `internal/services/datacleaner/adjustments/earnings.go`; `earnings_test.go`
- **Sub-steps:** One commit per C-rule. Each becomes a Restater emitting `LedgerEntry{Component:"NormalizedOperatingIncome" | "InterestExpense", DeltaAmount:..., EquityOffset:DeltaAmount}`. Dual-write: keep existing income-statement mutations at `earnings.go:112,154,214,313,316,360`.
  - C1 (restructuring charges): `Component:"NormalizedOperatingIncome", DeltaAmount: +restructuringAmount, EquityOffset: +restructuringAmount` (add-back to normalized OI, increases retained earnings).
  - C2 (asset sale gains): `Component:"NormalizedOperatingIncome", DeltaAmount: -data.AssetSaleGains, EquityOffset: -data.AssetSaleGains`.
  - C3 (litigation settlements): same as C1 with `+data.LitigationSettlements`.
  - C4 (stock-based comp): same pattern.
  - C5 (derivative gains/losses): mutates twice at `:313,316`; emit one LedgerEntry with net `DeltaAmount`.
  - C6 (capitalized interest): `Component:"InterestExpense", DeltaAmount: +data.CapitalizedInterest, EquityOffset: 0` (interest expense reclassification doesn't flow to retained earnings — it shifts between line items).
  - C7 (working capital): no balance-sheet mutation today (check existing code at `earnings.go:390+`); emit `Fired:false` if no mutation.

#### Task 3.8 — Remove the PR-1 shim's earnings-side fallback path
- **Files:** `service.go::applyActiveAdjustments`
- **Sub-steps:** Same as Task 2.6 but for earnings.
- **Acceptance signal:** C-rule ledger entries sourced from `AdjusterOutput`.

**PR-3 acceptance criteria:**
- All Task 3.1-3.8 signals green
- `TestDDM_LegacyPath_BitForBit` GREEN
- Replay shows zero numeric drift; new C-rule entries in `adjustment_ledger`
- Snapshot UNCHANGED

### PR-4 — Liability adjusters migrated + B3 OverlaySpec routing + T2-BS-3 docs (~1 agent shift, ~8 commits — highest-risk PR, allocate extra verification budget)

#### Task 4.1 — Refactor B1 (operating leases) to implement `Adjuster`
- **Files:** `internal/services/datacleaner/adjustments/liabilities.go` lines 107-284 (including `fallbackToSimpleCapitalization`); `liabilities_test.go`
- **Sub-steps:** OverlayEmitter. Emit `OverlaySpec{OverlayID:"B1_operating_lease_capitalization", Field:"TotalDebt", Operation:"add", Amount:presentValue, AmountSemantics:AmountIncremental, Reasoning:...}`. **Critically: Phase 2 dual-write keeps the `data.TotalDebt += result.Amount` and `data.InterestBearingDebt += result.Amount` mutations at the orchestrator level (`liabilities.go:87-88`) EXACTLY UNCHANGED.** The OverlaySpec is recorded but inert.
- **Acceptance signal:** OverlaySpec appears in `data.Overlays` for any ticker where B1 fires (AAPL, MSFT, MXL, etc.). `TestDDM_LegacyPath_BitForBit` GREEN.

#### Task 4.2 — Refactor B2 (pension/OPEB) to implement `Adjuster`
- **Files:** `liabilities.go` lines 287-359; `liabilities_test.go`
- **Sub-steps:** OverlayEmitter. Emit `OverlaySpec{OverlayID:"B2_pension_underfunding", Field:"TotalDebt", Operation:"add", AmountSemantics:AmountIncremental}`. Dual-write unchanged.
- **Acceptance signal:** Same shape as 4.1.

#### Task 4.3 — Refactor B3 (contingent liabilities) to implement `Adjuster` — including AI provenance capture
- **Files:** `liabilities.go` lines 362-456 + the AI helper at 622-701; `liabilities_test.go`
- **Sub-steps:** OverlayEmitter with `Field:"DebtLikeClaims"` (NOT `"TotalDebt"`). **Critical:** the OverlaySpec's `Field` records the Phase 4 routing intent; the dual-write mutation STILL points at `TotalDebt` via the orchestrator at `:87-88`. So `data.TotalDebt` is mutated AND `OverlaySpec{Field:"DebtLikeClaims"}` is recorded. The mismatch is intentional — Phase 4 flips the consumer to read `Overlays[Field:"DebtLikeClaims"]` and the dual-write mutation gets deleted.
  - When AI fires (`la.aiEnabled && la.aiService != nil`): populate `OverlaySpec.AIProvenance` with `{ModelName: <from ai service config — check existing code at liabilities.go:687 metadata which today uses "footnote_analysis" as a stub; emit best-effort>, PromptHash:<sha256 of prompt — current code doesn't compute; punt to a placeholder that PR-4 documents as "Phase 3 work item: actual hashing">, ...}`. **This is a known limitation: today's AI helper does not return prompt/source hashes.** The AIProvenance struct is populated as best-effort with available fields (Confidence, Probability from existing `ai.AnalyzeFootnote` response); hash fields are zero-string in Phase 2 with a TODO to implement in Phase 3.
- **Acceptance signal:** OverlaySpec emitted with `Field:"DebtLikeClaims"`. AIProvenance non-nil when AI fires; zero hash fields acceptable in Phase 2 (documented TODO).

#### Task 4.4 — Refactor orchestrator-level `data.TotalDebt += result.Amount` at `liabilities.go:87-88`
- **Files:** `internal/services/datacleaner/adjustments/liabilities.go` lines 56-104 (`ProcessLiabilityAdjustments`)
- **Sub-steps:** This orchestrator is being absorbed into the new `Adjuster` dispatch. Two options:
  - **Option α (chosen):** Keep `ProcessLiabilityAdjustments` as a thin wrapper that for each rule calls the new `Apply*` method and then performs the dual-write mutation `data.TotalDebt += output.Overlays[0].Amount` (when output is OverlayEmitter-shaped and Field is `"TotalDebt"` or `"DebtLikeClaims"`). The mutation logic moves into the wrapper but the bytewise behavior is preserved.
  - **Option β (rejected):** Have each B-rule adjuster mutate `data.TotalDebt` directly inside its `Apply` method. Rejected because it spreads the dual-write site across three files instead of one, making Phase 3's deletion harder.
- **Acceptance signal:** `TestProcessLiabilityAdjustments` (existing) stays green. `data.TotalDebt` mutation order across B1→B2→B3 is bit-identical to today.

#### Task 4.5 — Remove the PR-1 shim's liability-side fallback path
- **Files:** `service.go::applyActiveAdjustments`
- **Sub-steps:** Same as 2.6 / 3.8. After 4.5, the PR-1 shim is fully deleted.

#### Task 4.6 — Add `TestLedger_BasketSnapshot_ClusterPrediction` integration test
- **Files:** Create `internal/integration/datacleaner_ledger_basket_test.go`
- **Sub-steps:** Per §5.2. Use the existing 10 `recompute-shadow/<TICKER>.json` snapshots as truth source for which AdjusterIDs should appear in each ticker's ledger.
- **Acceptance signal:** Test passes against the 10 basket tickers.

#### Task 4.7 — T2-BS-3 documentation + CLAUDE.md gotcha
- **Files:**
  - Modify: `CLAUDE.md` — add new gotcha bullet titled "DC-1 Phase 2 SHIPPED 2026-05-XX, merge `<hash>`" describing the Adjuster interface, T2-BS-3 Option B carve-out, and the `LedgerEntry.SourceReliability` field. Reference this plan.
  - Modify: `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md` — Status update: "Disposition: Option B chosen by Phase 2 ARCH 2026-05-19. Parser fix deferred."
  - Modify: `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` — Phase 2 SHIPPED progress paragraph.
  - Modify: `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` change-log — "| 2026-05-XX | Phase 2 SHIPPED ... |".
  - Modify: `docs/THESIS.md` DC-1 row status.
  - Create: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md` (template from Phase 1 closeout).
  - Optionally create: `TESTING.md` if it exists; otherwise document the equity-offset invariant in the spec changelog.
- **Acceptance signal:** All doc changes land in PR-4.

**PR-4 acceptance criteria:**
- All Task 4.1-4.7 signals green
- `TestDDM_LegacyPath_BitForBit` GREEN
- Full test suite green
- All 3 replay tickers (AAPL/MSFT/JPM) zero numeric drift in `17-response.json`
- Basket snapshot integration test passes
- `internal/integration/testdata/recompute-shadow/<TICKER>.json` snapshots UNCHANGED (CRITICAL — dual-write preserves the divergence pattern; only Phase 3 closes the divergences). REVIEWER explicitly confirms.

---

## 8. PR Strategy Recommendation

### Recommendation: **Split into 4 PRs (PR-1 → PR-2 → PR-3 → PR-4).**

### Justification

**Argument for one PR (rejected):**
- Atomic deployment; either Phase 2 is in or out.
- Smaller chance of master-drift causing in-flight conflicts.
- Spec §"Phasing & implementation sequence" describes Phase 2 as one row.

**Argument for splitting (chosen):**
1. **Reviewer cognitive load.** 8 adjusters × 3 files × a new interface + entities + orchestrator + tests = ~3000 LOC diff in one PR. Reviewer can't hold that in head. Split-by-PR keeps each diff at ~700-1000 LOC, individually reviewable in 1-2 hr.
2. **Risk staging.** PR-1 (skeleton) is lowest risk and lands first; gives a stable foundation. PR-2 (assets) is medium risk and exercises the interface against the cluster with the most test coverage (`assets_test.go` is the most populated). PR-3 (earnings) is lowest behavioral risk (income-statement only; doesn't perturb the Phase 1 recompute observer). PR-4 (liabilities) is HIGHEST risk — DDM bit-for-bit invariant, B3 AI path, orchestrator-level `data.TotalDebt +=` site — and lands LAST when the interface is battle-tested.
3. **Partial-merge shippability.** Each intermediate PR's master state is shippable:
   - **After PR-1:** ledger + interface exist; no adjuster uses them yet (the PR-1 shim at Task 1.4 produces legacy-shaped ledger entries). `data.AdjustmentLedger` is populated but inert. Behavior bit-for-bit identical.
   - **After PR-2:** A-rules use the new interface; B-rules and C-rules still use the legacy shim. Mixed state but no behavior change.
   - **After PR-3:** A and C rules native; B rules legacy. Same.
   - **After PR-4:** all native; shim deleted. Phase 2 closed.
   - At any cut between PRs, master is shippable to production. The dual-write design guarantees this.
4. **Tier 2 master-drift mitigation.** Tier 2 worktrees `tier2-p1..tier2-p4` are pending rebase per CLAUDE.md. A monolithic PR landing in a single agent shift still wouldn't ship 4 separately-verifiable diffs; the split structure remains valuable for blast-radius reasons even at agent pace.

### PR boundaries summary

| PR | Title | Estimated effort | Scope | Critical files touched |
|---|---|---|---|---|
| **PR-1** | DC-1 Phase 2 PR-1: Adjuster interface + LedgerEntry/OverlaySpec entities + orchestrator scaffolding + recompute WARN enrichment | ~1 agent shift | Types + scaffolding shim; zero adjuster migration; **Task 1.6 SHIPPED per Q1 resolution** | `entities/adjustment_ledger.go` (NEW), `entities/financial_data.go`, `adjustments/adjuster.go` (NEW), `service.go::applyActiveAdjustments`, `recompute.go::emitIfDiverged` |
| **PR-2** | DC-1 Phase 2 PR-2: Asset adjusters migrated to Adjuster interface + FY-NULL investigation | ~1 agent shift | A1, A2, A4, A5 + flag-only reviews; **Task 2.7 FY-enable-predicate investigation per Q3 resolution** | `adjustments/assets.go`, `adjustments/assets_test.go`, `service.go` (shim removal asset-side), `docs/reviewer/DC-1-FY-enable-predicate-investigation.md` (NEW) |
| **PR-3** | DC-1 Phase 2 PR-3: Earnings adjusters migrated to Adjuster interface | ~1 agent shift | C1-C7 | `adjustments/earnings.go`, `adjustments/earnings_test.go`, `service.go` (shim removal earnings-side) |
| **PR-4** | DC-1 Phase 2 PR-4: Liability adjusters migrated + B3 OverlaySpec(DebtLikeClaims) + T2-BS-3 docs | ~1 agent shift (highest-risk) | B1, B2, B3 (with AIProvenance — empty hashes per Q4), orchestrator absorption, integration test, all docs | `adjustments/liabilities.go`, `adjustments/liabilities_test.go`, `service.go` (final shim removal), `integration/datacleaner_ledger_basket_test.go` (NEW), CLAUDE.md, multiple docs |

### Cadence

- BACKEND runs `git fetch && git merge origin/master` at the START of each PR (not weekly within a PR; weekly creates churn). After PR-1 merges, PR-2's worktree rebases to PR-1's master tip; same chain through PR-4.
- Each PR completes full V-R-Q (VERIFIER → REVIEWER → QA → HUMAN merge approval) before next PR opens. Parallel PR work risks cross-coupling on `service.go::applyActiveAdjustments` which all four PRs touch.

---

## 9. Spec / Doc Updates Required

### CLAUDE.md — new gotcha bullet (landed in PR-4)

Format mirrors the existing "DC-1 Phase 1 SHIPPED" gotcha at CLAUDE.md (the entry ending with "...the 7-cluster Phase 2 punch list"):

> **DC-1 datacleaner refactor — Phase 2 SHIPPED 2026-05-XX, merge `<hash>`.** Adjuster interface lands at `internal/services/datacleaner/adjustments/adjuster.go` — every existing adjuster (A1-A5, B1-B3, C1-C7) implements `Adjuster.Apply(ctx, working, rule, cleaningCtx) (AdjusterOutput, error)` and emits `LedgerEntry` and/or `OverlaySpec` records into `data.AdjustmentLedger` and `data.Overlays` fields on `FinancialData`. Phase 2 is **dual-write**: the existing in-place mutations (`data.TotalAssets -= X`, `data.TotalDebt += Y`) remain alongside the new ledger/overlay emission. No downstream consumer reads the ledger or overlays yet — Phase 3 will introduce `CleanedFinancialData.Restated()` and `.InvestedCapital()` accessors that consume them. **B3 contingent-liability OverlaySpec uses `Field:"DebtLikeClaims"`** (the future Phase 4 routing), but the Phase 2 dual-write mutation still points at `data.TotalDebt`; the routing flip is deferred to Phase 4 (the spec's substantive accuracy change). **T2-BS-3 disposition: Option B (carve-out, no parser fix).** The AMD/KO `TotalLiabilities == 0` parser dropout stays in `AsReported` until Phase 3 reconstructs `Restated.TotalLiabilities` from sum(components) + plug, or a separate parser-side initiative addresses T2-BS-3 directly. `LedgerEntry.SourceReliability` field is the hook ("high" | "medium" | "parser_known_dropout"). Phase 1 `recomputeUmbrellas` shadow shim remains in place unmodified; its WARN stream is the Phase 3 regression sentinel. Spec: `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md`. Plan: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-implementation-plan.md`.

### Spec changelog updates (one per PR, landed in each PR)

Add to `docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md` change-log table:

| Date | Change |
|---|---|
| 2026-05-XX (PR-1) | Phase 2 PR-1 SHIPPED: Adjuster interface at `internal/services/datacleaner/adjustments/adjuster.go`; LedgerEntry/OverlaySpec entities at `internal/core/entities/adjustment_ledger.go`; AdjustmentLedger and Overlays fields on FinancialData; orchestrator scaffolding emits ledger entries via legacy-Adjustment shim. Zero adjuster migration in PR-1. Zero downstream behavior change (dual-write). |
| 2026-05-XX (PR-2) | Phase 2 PR-2 SHIPPED: A1 (OverlayEmitter), A2/A4/A5 (Restaters), flag-only reviews migrated to Adjuster interface. Asset-side legacy shim deleted. |
| 2026-05-XX (PR-3) | Phase 2 PR-3 SHIPPED: C1-C7 (Restaters) migrated. Earnings-side legacy shim deleted. |
| 2026-05-XX (PR-4) | Phase 2 PR-4 SHIPPED: B1/B2 (OverlayEmitters, Field=TotalDebt), B3 (OverlayEmitter, Field=DebtLikeClaims with AIProvenance). Liability shim deleted; T2-BS-3 Option B carve-out documented. Basket snapshot integration test landed. Phase 2 closed. Phase 3 (view reconstruction) is the next gate. |

### Tracker updates

- `docs/reviewer/DC-1-datacleaner-component-primitive-and-parallel-views.md` — Phase 2 progress paragraph(s), one per PR.
- `docs/reviewer/T2-BS-3-parser-totalliabilities-zero-amd-ko.md` — Status: "Disposition: Option B (carve-out). Phase 2 ARCH 2026-05-19 chose to defer parser-side fix. Tracker stays OPEN. Parser fix would be re-considered if a future phase needs `AsReported.TotalLiabilities` to be truthful for AMD/KO, or if a separate parser-side initiative requests it."
- `docs/THESIS.md` — DC-1 phase row bumped from "Phase 1 SHIPPED" to "Phase 2 SHIPPED" (after PR-4 merges).

### Closeout report (landed in PR-4)

Create: `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-closeout.md` using the Phase 1 closeout as the template. Mandatory sections:
- What landed (per PR)
- Acceptance criteria results
- V-R-Q outcomes per PR
- Lessons learned
- Phase 2 → Phase 3 gate input (what Phase 3 ARCH needs to know)
- Shadow-analysis-v2 placeholder: a follow-up section to be filled in post-Phase-3 enumerating residual divergences. **NOT a Phase 2 deliverable.** Phase 2 closeout just notes "shadow analysis remains the Phase 1 cluster set; Phase 3 will produce shadow-analysis-v2 enumerating residuals after view reconstruction lands."

### TESTING.md (only if it exists)

Per the Phase 2 handoff doc acceptance criteria, "TESTING.md extended with Adjuster contract + equity-offset invariant subsection." Check if the file exists at the repo root. If yes, append a subsection citing the equity-offset invariant test. If no, document the invariant in the spec changelog row instead — TESTING.md creation is not in Phase 2 scope.

---

## 10. Open Questions / Decisions Deferred to Human

### Q1 — `recomputeUmbrellas` WARN log enrichment (PR-1 Task 1.6) — **RESOLVED 2026-05-21: SHIP**
User directed PR-1 to ship the WARN log enrichment. Rationale accepted: additive field on a log line is not a semantics modification; Phase 3 debugging benefits from immediate access to recent-adjuster correlation. Task 1.6 promoted from OPTIONAL to REQUIRED.

### Q2 — TaxShieldDTA on A2 (intangible writedown) — populated or zero?
Spec §"Adjuster reclassification" line 174 mentions TaxShieldDTA on A5 inventory writedown. A2 intangible writedowns also generate a tax shield (impairment is deductible). Today's A2 code at `assets.go:111-194` does not compute or apply a tax shield. Phase 2 default: emit `TaxShieldDTA: 0` for A2 (faithful to today's behavior, dual-write preserves bit-for-bit). Alternative: compute `TaxShieldDTA = writedownAmount * data.EffectiveTaxRate` like A5. Recommendation: **stick with zero for A2 in Phase 2** to preserve dual-write invariant; revisit in Phase 3 when consumers actually read TaxShieldDTA. **Decision needed during PR-2.**

### Q3 — Cluster A-FY-NULL follow-up sub-task — **RESOLVED 2026-05-21: INCLUDE as PR-2 Task 2.7**
User directed Phase 2 to include the FY-enable-predicate verification as a PR-2 sub-task. Rationale accepted: context is hot (same agent that just refactored A1-A5 is best positioned to read enable predicates); tracker shipping in PR-2 means Phase 3 inherits a closed knowledge gap, not an open one. Task 2.7 inserted in §7. Investigation produces a tracker (`docs/reviewer/DC-1-FY-enable-predicate-investigation.md`), NOT code changes — Phase 2 scope stays tight.

### Q4 — `AIProvenance` hash fields in Phase 2 (PR-4 Task 4.3) — **RESOLVED 2026-05-21: ACCEPT empty hashes + TODO**
User accepted ARCH's recommendation. PR-4 ships `AIProvenance{PromptHash:"", SourceDocHash:"", ...}` with a documented `// TODO: Phase 3` marker. Hashing implementation defers to Phase 3 alongside view-reconstruction work where the hashes are actually consumed for replay determinism.

### Q5 — Should the `Adjuster` interface accept a value receiver or pointer receiver?
Existing methods like `(aa *AssetAdjuster) ProcessGoodwillAdjustment` use pointer receivers. The new `Adjuster` interface as proposed in §3.1 is implementable by either. Recommendation: **pointer receivers on the concrete types** (`(aa *AssetAdjuster) Apply` etc.) matching today's convention. Interface itself doesn't dictate. **Decision needed during PR-1.**

### Q6 — `entities.LedgerEntry` vs `adjustments.LedgerEntry` namespace
The plan places `LedgerEntry`, `OverlaySpec`, `AdjustmentLedger`, etc. in `internal/core/entities/` to mirror the Phase 0 entity placement (`FinancialData` in `entities`). Alternative: place them in `internal/services/datacleaner/adjustments/` (closer to their producers). Recommendation: **`entities/`** per spec §"Adjuster output" which positions them as core domain types (consumed by future view reconstruction in Phase 3 from outside the `adjustments/` package). Choosing `adjustments/` would force a Phase 3 move. **Decision settled in §3 via the spec anchor; calling out only because it's a common reviewer ask.**

---

## 11. Phase 2 → Phase 3 Gate

What Phase 3 ARCH needs from Phase 2's output:

1. **Stable `LedgerEntry` and `OverlaySpec` field shapes.** Phase 3 builds `Restated()` and `InvestedCapital()` accessors that consume these structs by field name. The field set in §3.2-3.3 must not change after Phase 2 closes; additive changes are OK in Phase 3.
2. **Populated ledgers across the basket.** Phase 3's bit-for-bit gate ("`cleaned.Restated()` produces bit-for-bit identical results to today's single-view cleaner output across basket" — spec §"Phasing & implementation sequence" line 448) requires Phase 2's ledger + dual-write outputs to match. The Task 4.6 basket integration test pins this — Phase 3 inherits and extends it.
3. **No production consumer reads the ledger.** Phase 3 introduces the first consumers (via `CleanedFinancialData.Restated()`). If Phase 2 accidentally added a production read, Phase 3's design assumes a clean greenfield. Audit at PR-4 merge: `grep -r "AdjustmentLedger" internal/` outside of `datacleaner/` and entity files.
4. **The `recomputeUmbrellas` shim and shadow snapshots remain in place.** Phase 3 uses the SAME WARN/snapshot machinery as the regression sentinel for view reconstruction (cluster A1-A5 / B1 divergences SHOULD shrink after Phase 3's recompute lands; PR-4's snapshot UNCHANGED claim is the cross-check that this hasn't shrunk prematurely).

---

## 12. Change Log

| Date | Change |
|---|---|
| 2026-05-19 | Initial Phase 2 implementation plan filed by ARCH (`/plan-and-create`). T2-BS-3 disposition: Option B (carve-out). PR strategy: 4-PR split. Anchored at master HEAD `987ec31`. |
| 2026-05-21 | Human review complete: Q1 SHIP (Task 1.6 promoted from OPTIONAL to REQUIRED), Q3 INCLUDE (Task 2.7 inserted in PR-2), Q4 ACCEPT (empty hashes + Phase 3 TODO). Time estimates revised from human-engineer days (~2.5 weeks) to AI-agent shifts (~4 shifts across 4 PRs). BACKEND dispatched on PR-1. |
| 2026-05-22 | PR-2 SHIPPED on branch `dc1-phase-2-pr-2` (stacked on PR-1 tip `dc1-phase-2-pr-1-clean`). 7 commits: Task 2.1 (A1 OverlayEmitter + SchemaVersion 7→8) `4ca4b3c`; Task 2.2 (A2 Restater) `15a5798`; Task 2.3 (A4 Restater) `79d3015`; Task 2.4 (A5 Restater + TaxShieldDTA) `631bf72`; Task 2.5 (RDCapitalization + CapitalizedSoftware flag-only reviews — FlagEmitter convention) `039b680`; Task 2.6 (delete asset-side shim branch) `2c132aa`; Task 2.7 (A-FY-NULL read-only investigation tracker) `df25866`. All load-bearing invariants GREEN throughout: `TestDDM_LegacyPath_BitForBit` (jpm/bac/wfc), `TestRecomputeUmbrellas_NoMutation`, `TestOrchestrator_LedgerOrdering`, `TestDataCleanerRecompute_ShadowMode_TickerBasket` + shadow-snapshot byte-identity. Canonical pattern across all 6 A-rule migrations: mutation-FREE `Apply*` on `AssetAdjuster`; dispatcher `ProcessAssetAdjustments` owns the capture → Apply → translate → mutate → drain-natives sequence. PR-3 (earnings adjusters C1-C7 + Task 3.8 earnings-side shim deletion) is the next handoff; handoff doc at `docs/refactoring/implementations/datacleaner-component-primitive-and-parallel-views-phase-2-pr-3-handoff.md`. |
| 2026-05-22 | PR-3 SHIPPED on branch dc1-phase-2-pr-3. 8 commits: 3.1 C1 b1af6b1; 3.2 C2 e621320; 3.3 C3 988a371; 3.5 C5 5654464; 3.6 C6 5610d51; 3.4 C4 79b78bd (FlagEmitter, plan-vs-code disagreement documented); 3.7 C7 75afa8b; 3.8 earnings-shim deletion 4af3c33. All load-bearing invariants GREEN throughout. PR-4 ready. |
