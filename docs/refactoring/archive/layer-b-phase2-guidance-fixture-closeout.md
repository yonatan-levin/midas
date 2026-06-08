# Closeout — Layer B Phase 2: Guidance-Artifact Fixture Consumption + Assumption-Authority Hierarchy

**Date:** 2026-06-08
**Status:** SHIPPED on branch `feat/layer-b-phase2-guidance-fixture` (off the Layer A tip). FF-able onto Layer A; user completes the guarded fast-forward.
**Design of record:** `docs/refactoring/spec/layer-b-phase2-guidance-fixture-spec.md`
**Parent spec:** `docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md` — Phase 2 (§8.2/§8.3/§9/§12.4/§12.5).
**Predecessor:** Layer A (DCF reinvestment / operating-leverage) at `CalculationVersion 4.7` — `docs/refactoring/archive/dcf-reinvestment-layer-a-closeout.md`.
**Engine version:** UNCHANGED at `4.7` (Decision 7 — zero math change on the default path).

---

## 1. What shipped

midas now consumes a **hand-authored, immutable, content-addressed guidance fixture deterministically** (no LLM) and anchors **near-term (year 1–2) only** DCF inputs through the assumption-authority hierarchy (§9). The common case — no fixture (empty `GuidanceRoot`, the production default) — is **byte-identical to the 4.7 engine** (NF1), which is what keeps DDM/FFO/revenue_multiple and every un-fixtured ticker bit-for-bit.

### New code
| Component | Location |
|---|---|
| Guidance artifact types + canonical hashing + structural validation + accession-keyed immutable loader | `internal/services/valuation/guidance/{artifact,validate,loader,dates,bundle_stage}.go` |
| §9 assumption-authority resolver (precedence + §9.3 guardrails + midpoint anchor) | `internal/services/valuation/authority/resolver.go` |
| Service consumption seam (loader → resolver → bundle capture → anchors) | `internal/services/valuation/guidance.go::resolveGuidance` + `stampAssumptionSources` |
| Near-term anchor application (DCF path only) | `internal/services/valuation/reinvestment.go::applyNearTermAnchors` |
| Additive engine override seams | `pkg/finance/dcf/dcf.go` — `Inputs.NearTermReinvestmentOverride` + `NearTermMarginOverride` (per-year maps; nil/empty ⇒ byte-identical) |
| Response provenance | `entities.AssumptionSources` + `handlers.FairValueResponse.AssumptionSources` (omitempty) |
| Replay capture | `09-guidance.json` stage via `artifact.SetGuidanceResolution` + `replay.BundleGuidanceGateway` |
| Config | `config.Valuation.GuidanceRoot` (default `""` = disabled) |
| Fixtures | `internal/services/valuation/guidance/testdata/guidance/0000002488/*.json` |

### Decisions resolved
- **§12.4 — anchor mechanic:** midpoint-replace, near-term-only. A `validated`, high-confidence, non-stale envelope sets year-1 (and optionally year-2) to `m=(value_low+value_high)/2`; years ≥ 3 + terminal untouched (the structural §9.3 dominance guardrail).
- **§12.5 — version:** NO bump; stays `4.7` (the default path has zero math change, so a bump would needlessly invalidate caches). Phase 3 (a real extraction tool changing a production value) gets the bump.

---

## 2. Validation arc — FOUR rounds

1. **`/plan-and-create` B-V-R-Q (role subagents):** ARCH (design) → BACKEND (B1–B7) → VERIFIER **VERIFIED** → REVIEWER **APPROVE_WITH_NITS** (2 Medium + 4 Low, all addressed) → QA **PASS**.
2. **Live API testing** — caught **2 integration bugs the all-green unit suite missed** (the unit tests used the padded CIK + tested the entity/seam, never the full HTTP path):
   - The loader rejected AMD's **un-padded SEC CIK `2488`** vs the fixture dir `0000002488` ⇒ fixture never consumed. Fixed via `normalizeCIK` (zero-pad to 10; still rejects non-numeric/traversal). Pinned by `TestLoader_UnpaddedCIK_NormalizesAndResolves`.
   - `FairValueResponse` never exposed `assumption_sources` despite the entity carrying it ⇒ structured provenance never reached the response. Fixed by adding the omitempty field + `buildFairValueResponse` copy + `replay/diff.go` field-count-drift-guard registration (`goFieldToJSON` + `countFairValueFields` 44→45).
3. **`zen-mcp` gpt-5.5 CROSS-MODEL review** (the project's signature gate) — caught **11 more real issues the same-model gates rationalized past**:
   - **HIGH-1 (load-bearing):** the year-1 margin anchor mutated `BaseOperatingMargin` (shifts the whole convergence curve) **and** raised `TargetOperatingMargin` (alters terminal NOPAT) ⇒ guidance **leaked into terminal value**, violating the §9.3 "near-term only, never dominates intrinsic value" invariant. Fixed by routing margin year-1 through `NearTermMarginOverride[1]` (no Base/Target mutation). Pinned by a year-1-only-vs-terminal regression.
   - **HIGH-2:** `ValueLow/ValueHigh float64` ⇒ omitted JSON anchors a silent `0` (defeats "explicit value required"). Fixed → `*float64` presence + confidence ∈ [0,1].
   - **HIGH-3:** unit not enforced per kind (capex-as-pct / margin-as-USD passed). Fixed → kind-specific unit checks (§8.6 scale defense).
   - **HIGH-4:** hash verified *after* structural validation ⇒ tampered+invalid silently skipped. Fixed → hash-before-structural.
   - **MEDIUM-1..7:** newest-period staleness; loader issuer-CIK-matches-dir; first-*eligible* (not [0]) margin/revenue envelope; revenue anchor extends a short slice (no silent no-op); engine `validateInputs` rejects override year ∉ [1,2] (defense beyond the service seam); gross-as-net CapEx approximation disclosed in provenance + warning; replay returns non-missing read errors (only missing ⇒ absent).
4. **Live re-confirmation** post cross-model fixes: empty-root AMD `112.5696` (byte-identical to 4.7); fixture-root AMD `115.1051` with `assumption_sources.capex_year1 source=guidance` + `gross_capex_as_net_reinvestment_approx=true` disclosure.

---

## 3. Load-bearing invariants (GREEN at every commit)
- **NF1 absent-guidance byte-identity** — empty `GuidanceRoot` ⇒ byte-identical to 4.7 (gated at engine / seam / service / response levels; pinned by `TestResolveGuidance_EmptyRoot_NoOp`, `TestApplyReinvestmentModel_NilAnchors_ByteIdentical`, `TestCalculateDCF_NearTerm*Override_NilIsByteIdentical`).
- `TestDDM_LegacyPath_BitForBit`, `TestCalculateDCF_LegacyProportional_BitForBit`.
- `recompute-shadow` snapshots byte-identical (`git diff --quiet …/recompute-shadow/` exit 0).
- `service_test.go` `CalculationVersion == "4.7"` pins unchanged (no Phase-2 bump).
- Full `go test ./... -count=1` EXIT 0 (50 packages).

---

## 4. Deferred to Phase 3 (the offline extraction tool)
- **Gross→net CapEx conversion** (`net = grossCapEx − D&A + ΔWC`). Phase 2 treats guided gross capex AS the net reinvestment, disclosed via `gross_capex_as_net_reinvestment_approx=true` in provenance + warning; fixtured-path-only, never production (empty root).
- The actual LLM extraction tool (accession-keyed, validator-computed confidence, human-in-the-loop) producing artifacts that validate against this Phase-2 contract.
- Flipping the default `GuidanceRoot` to a production directory (one line + a config bump) + the CalculationVersion bump that first production-value change warrants.
- Revenue-level (vs growth-rate) anchoring; sensitivity-range consumption of `(value_low, value_high)`.

---

## 5. Commit ladder
B1–B7 + spec (`0a859af..0709c61`) → review fixes (`a4a2938`) → live-run fixes (`7e0db54`) → gpt-5.5 cross-model fixes (`9f19eb9`) → docs (this closeout).
