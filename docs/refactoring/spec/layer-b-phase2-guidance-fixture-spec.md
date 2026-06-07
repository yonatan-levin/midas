# Layer B Phase 2 — Guidance-Artifact Contract + Assumption-Authority Hierarchy + Fixture Consumption

**MODE:** PLAN_AND_CREATE
**ROLE:** ARCH
**Version:** 1.0 (design / implementation-ready)
**Date:** 2026-06-07
**Branch:** `feat/layer-b-phase2-guidance-fixture` (off the Layer A tip)
**Parent spec:** `docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md` — Phase 2 = §8.2/§8.3/§8.4 (Layer B contract + determinism boundary + consumption), §9 (assumption-authority hierarchy), §10 "Phase 2", §11.3/§11.4 (tests), §12.4/§12.5 (open questions).
**Predecessor:** Layer A (reinvestment / operating-leverage DCF) SHIPPED at `CalculationVersion 4.7` — `docs/refactoring/archive/dcf-reinvestment-layer-a-closeout.md`.
**Status:** DESIGN COMPLETE — ready for BACKEND. All open design decisions resolved autonomously below (each with rationale).

> **Worktree note:** this branch is a git worktree NOT in `go.work`; gopls/IDE diagnostics show false "undefined" errors. Trust CLI `go build`/`go test`, not IDE diagnostics.

---

## Summary

Phase 2 makes midas **consume a hand-authored, immutable guidance-artifact fixture deterministically** — proving the full Layer B consumption path (the §8.2 artifact contract, the §9 assumption-authority precedence resolver, per-assumption source recording, and replay-bundle capture) against a known-good fixture **before any LLM extraction tool (Phase 3) exists**. No live LLM runs in the valuation hot path; midas reads only captured fixtures. **The common case (no guidance artifact) is byte-identical to today's valuation** — that absent-guidance equivalence is the mechanism that keeps DDM/FFO/revenue_multiple and every un-fixtured ticker bit-for-bit unchanged.

### Goals

1. Finalize the guidance-artifact JSON schema (extending §8.2): exact fields, required-vs-optional, the mandatory `"no_explicit_guidance_found"` status form (§8.3 item 3), and an `ai_provenance` block mirroring midas's existing `AIProvenance` pattern. Versioned via `schema_version`.
2. Specify accession-keyed identity, immutability, content-addressing, deterministic conflict resolution + staleness, and how the valuation receives an explicit as-of/filing-cutoff so replay pins the exact artifact (§8.3 items 1,2,4,5).
3. Implement the §9 assumption-authority hierarchy as a deterministic resolver: 5-level precedence, per-assumption source recording (a new diagnostic block + per-assumption source tags consistent with the RM-1 / VAL-1 conventions), and the §9.3 anti-"assumption-laundering" guardrails.
4. Implement a deterministic, accession-keyed, **immutable** loader that caches "no guidance found" as a first-class result.
5. Capture the consumed artifact (or the no-guidance record) as a new numbered stage file in the replay bundle so a guidance-consuming valuation replays bit-for-bit.
6. Decide §12.4 (the year-1 anchor mechanic) and §12.5 (whether Phase 2 warrants a `CalculationVersion` bump).

### Non-Goals

- **No LLM / extraction tool.** That is Phase 3. Phase 2 reads fixtures only.
- **No changes to Layer A math, the DCF projection, WACC, growth, or the cleaner.** Guidance is an *input selector* that sits between profile resolution and DCF-input construction; when absent it is a strict no-op.
- **No multi-knob coverage beyond the three guidance kinds named by the contract** (CapEx, margin, revenue). Phase 2 wires the *resolver + recorder + loader + capture*; the only assumption the fixture actually steers in this phase is **year-1 (and optionally year-2) of the DCF reinvestment/margin trajectory** — see §A (the anchor mechanic).
- **No DDM/FFO/revenue_multiple behavior change.** Guidance is DCF-path-aware (it anchors the reinvestment-model near-term inputs). Alt-model paths record `source=profile/historical` for their assumptions but their *values* are untouched.
- **No new live data dependency, no network, no DB table.** Fixtures live on disk under a versioned, content-addressed directory.

---

## Requirements

### Functional

- **F1.** A deterministic loader resolves, for a `(CIK, as-of)` valuation, at most one guidance artifact — keyed on `(CIK, accession)` — or a cached **absent** result. Same inputs ⇒ same artifact (or same absence) every run.
- **F2.** Artifacts are immutable and content-addressed: the on-disk `artifact_sha256` is recomputed on load and must match the embedded value; a mismatch is a hard load error (never silently consumed).
- **F3.** The assumption-authority resolver applies the §9 precedence and records, **per assumption**, which level supplied the final value, surfaced in the response/trace.
- **F4.** Anti-laundering guardrails (§9.3) are enforced: a numeric override requires `validation.status == "validated"`, `confidence ≥ threshold`, an explicit `value` + an accepted `evidence` quote; vague-prose-only artifacts contribute qualitative context, never a number; guidance anchors near-term (year 1–2) only and never dominates intrinsic value; low-confidence or absent ⇒ fall through to Layer A.
- **F5.** The selected artifact (or the absent record) is captured into the replay bundle as a numbered stage file, and a guidance-consuming valuation replays bit-for-bit through `cmd/replay`.
- **F6.** The contract round-trips: a fixture (including `"no_explicit_guidance_found"`) marshals/unmarshals losslessly and validates against the schema.

### Non-Functional

- **NF1 — Absent-guidance byte-identity (load-bearing).** With no fixture (the production default), every valuation is byte-identical to the Layer A 4.7 engine: DDM `TestDDM_LegacyPath_BitForBit`, FFO/revenue_multiple primaries, recompute-shadow snapshots, and full `go test ./...` all green.
- **NF2 — Determinism.** No wall-clock, no map-iteration-order, no filesystem-listing-order dependence in any value that reaches the result, the trace, or the captured bundle. (The loader's directory scan sorts; hashes exclude wall-clock — same discipline as `hash.go::sha256HexPromptCanonical`.)
- **NF3 — Hermetic replay.** The loader, when driven from the replay bundle, reads the *captured* artifact from the bundle, not the live fixture directory (preserves replay's hermeticity contract: no live filesystem outside the bundle).
- **NF4 — Failure isolation.** A malformed/absent fixture degrades to the absent path (fall through to Layer A) with a `Warnings` entry — it never aborts a valuation. (A *content-hash mismatch* on a present artifact is the one hard error, because silently consuming a tampered artifact violates immutability.)
- **NF5 — Coverage.** ≥90% on new finance-adjacent code per the CLAUDE.md standard; table-driven tests.

---

## Architecture

### Data-flow placement

The guidance step sits **between AssumptionProfile resolution and DCF-input construction**, exactly where the prompt specifies and where the data flow already produces the profile but has not yet built the model input:

```
performValuation (internal/services/valuation/service.go)
  ├─ resolve AssumptionProfile  ............  service.go ~875-933  → bundle 08-assumption-profile.json
  ├─ [NEW] resolve guidance + authority  ...  service.go (new call) → bundle 09-guidance.json   ◄── Phase 2
  ├─ growth estimate / WACC / model select .  (unchanged)            → 12/13/14
  ├─ build dcf.Inputs                        (unchanged shape)       → applyReinvestmentModel reads anchored near-term inputs
  └─ DCF / alt-model                         (unchanged)             → 15/16/17
```

The resolver produces a **`GuidanceResolution`** value (the selected artifact or the absent record + the per-assumption source map). It is threaded into `applyReinvestmentModel` (the single Layer-A wiring seam) which already owns the near-term reinvestment/margin inputs — so the anchor is applied at exactly one place, on the DCF path only.

```
                        ┌──────────────────────────────┐
   (CIK, as-of) ───────►│  guidance.Loader (immutable, │
                        │  accession-keyed, caches      │──► *guidance.Artifact  OR  AbsentRecord
                        │  absent; content-addressed)   │
                        └──────────────┬───────────────┘
                                       │
   ResolvedProfile (Layer A) ─────────►│  authority.Resolver (§9 precedence + §9.3 guardrails)
   ValuationOptions (user override) ──►│
                                       ▼
                        ┌──────────────────────────────┐
                        │  GuidanceResolution           │
                        │   • Near-term anchors (yr 1-2) │──► applyReinvestmentModel (DCF only)
                        │   • per-assumption SourceMap   │──► result.AssumptionSources (diagnostic)
                        │   • Warnings (source tags)     │──► result.Warnings
                        │   • CapturedArtifact / Absent  │──► bundle 09-guidance.json
                        └──────────────────────────────┘
```

### Decision 1 — Finalized guidance-artifact JSON schema

A new package **`internal/services/valuation/guidance`** owns the Go types and (un)marshalling. The schema extends §8.2 with explicit required/optional and the mandatory absent form. `schema_version` is `"1.0.0"`.

**Top-level (`guidance.Artifact`)**

| Field | Type | Req? | Notes |
|---|---|---|---|
| `schema_version` | string (semver) | **required** | `"1.0.0"`. Loader rejects an unknown major version (forward-compat gate). |
| `status` | enum string | **required** | `"validated"` \| `"needs_review"` \| `"rejected"` \| `"no_explicit_guidance_found"`. Only `"validated"` is eligible to supply a numeric override (§9.3). |
| `issuer` | object | **required** | `{ ticker: string, cik: string }`. `cik` is the zero-padded 10-digit form (matches SEC/`ports.FlexibleCIK`). |
| `filing` | object | **required** | identity block — see below. **Carries `accession`** (the immutable key). |
| `source_selection` | object | optional | `{ sections: []string, selected_text_sha256: string }`. Provenance of the extracted region; absent on `no_explicit_guidance_found`. |
| `extraction` | object | optional | the three guidance envelopes — see below. **Absent/empty when `status == "no_explicit_guidance_found"`.** |
| `ai_provenance` | object | optional | mirrors midas `AIProvenance` — see below. **For a hand-authored Phase-2 fixture, `provider="hand_authored"`** and the hashes are computed over the fixture inputs. |
| `validation` | object | **required** | `{ status: string, confidence: float64, warnings: []string, normalization_rules_version: string, validator_version: string }`. `confidence` here is the **artifact-level** validator confidence; per-envelope confidence lives inside each envelope. |
| `artifact_sha256` | string (hex) | **required** | SHA-256 over the canonical serialization of every field EXCEPT `artifact_sha256` itself (content-address; see Decision 2). |

**`filing` block (identity — §8.3 item 1)**

| Field | Type | Req? | Notes |
|---|---|---|---|
| `accession` | string | **required** | e.g. `"0000002488-26-000012"`. The immutable identity together with `cik`. |
| `form_type` | string | **required** | `"10-K"` \| `"10-Q"` \| `"10-K/A"` … drives conflict tie-break (form specificity). |
| `filing_date` | date (`YYYY-MM-DD`) | **required** | drives conflict resolution (newest wins) + as-of cutoff eligibility. |
| `period_end` | date (`YYYY-MM-DD`) | **required** | the fiscal period the filing reports. |
| `sec_url` | string | optional | human reference. |
| `source_doc_sha256` | string (hex) | optional | hash of the raw filing text (Phase 3 fills it; fixture may set a synthetic value). |

**`extraction` envelopes** — `capex_guidance` (object), `margin_guidance` (array), `revenue_guidance` (array). Every envelope shares one shape (`guidance.Envelope`):

| Field | Type | Req? | Notes |
|---|---|---|---|
| `value_low` | float64 | **required** | low end. For a point estimate, `value_low == value_high`. |
| `value_high` | float64 | **required** | high end. Invariant `value_low ≤ value_high`. |
| `unit` | enum string | **required** | `"absolute_usd"` \| `"pct"` (margin as a fraction in [0,1]). Defends against scale errors (§8.6). |
| `period` | string | **required** | explicit, e.g. `"FY2026"`. Ambiguous/empty ⇒ envelope is invalid ⇒ not consumed (§8.6 period-ambiguity rule). |
| `basis` | object | optional | `{ gross_or_net, cash_or_accrual, gaap_or_non_gaap, consolidated_or_segment }` — recorded; `gaap_or_non_gaap` MUST be present for margin (never silently mix, §8.6). |
| `confidence` | float64 [0,1] | **required** | the **validator-computed** per-envelope confidence (§8.3 item 6). |
| `evidence` | array | **required-if-numeric** | `[{ quote: string, location: string }]`. A numeric override requires ≥1 forward-looking evidence quote (§9.3). |

**`ai_provenance` block** (mirrors `internal/core/entities/adjustment_ledger.go::AIProvenance` + the hashing discipline in `internal/services/datacleaner/adjustments/hash.go`):

```jsonc
"ai_provenance": {
  "provider": "hand_authored",          // Phase 2 fixtures; Phase 3 → "anthropic" etc.
  "model_name": "fixture",              // mirrors AIProvenance.ModelName
  "model_version": "n/a",
  "temperature": 0.0,
  "prompt_sha256": "…",                 // canonical-request fingerprint (timestamp-free, sorted keys) — same rule as sha256HexPromptCanonical
  "schema_sha256": "…",                 // hash of the extraction schema version
  "raw_response_sha256": "…",           // hash of the raw extracted text
  "extraction_code_git_sha": "…"        // empty for hand-authored fixtures
}
```

> **Rationale.** Reusing midas's existing `AIProvenance` shape + the `hash.go` canonical-hashing rule (exclude wall-clock, sort map keys, type-tag unsupported values) means the Phase-3 extraction tool's provenance maps 1:1 onto what Phase 2 already consumes — zero contract churn at the Phase-2→3 boundary. The guidance `AIProvenance` is recorded for audit/replay; in Phase 2 it never *drives* a value (the value comes from `extraction`), it just travels with the captured artifact.

**The mandatory `no_explicit_guidance_found` form** (§8.3 item 3) is a complete, valid artifact with `status: "no_explicit_guidance_found"`, populated `issuer` + `filing` (so the absence is *attributed to a specific filing*, making it cacheable and replay-pinnable), `extraction` absent/empty, and `validation.status` mirroring. This is what the loader caches and what the bundle captures when a filing was searched but carried no guidance.

### Decision 2 — Accession-keyed identity, immutability, content-addressing, conflict, staleness (§8.3)

- **Identity (item 1).** The artifact key is `(cik, accession)`, NEVER ticker/date. `accession` is recorded inside `filing`. On disk the file is named by accession to make the key self-evident (below).
- **As-of / filing-cutoff (item 1, replay pinning).** The valuation's as-of is **`s.clock.Now()`** (the existing Clock seam — production = wall-clock; replay binds it to `manifest.started_at`). The loader uses as-of as the **eligibility cutoff**: only artifacts whose `filing.filing_date ≤ as-of` are candidates. Because replay binds the clock to the captured `started_at`, the candidate set is identical on replay. **In replay, the loader does not scan the live directory at all — it reads the captured `09-guidance.json` from the bundle (NF3).** That is what pins "the exact artifact" bit-for-bit.
- **Immutability + content-addressing (items 2, 4-as-hash).** On load, the loader recomputes `artifact_sha256` over the canonical serialization (all fields except `artifact_sha256`, keys sorted, no wall-clock) and **hard-errors on mismatch**. A new prompt/model in Phase 3 produces a *new* file (new accession-scoped variant or a superseding accession) — never an in-place overwrite. The loader treats the directory as append-only.
- **Conflict resolution (item 4 — deterministic).** When ≥2 eligible artifacts reference the **same `period_end`** for the same CIK (e.g. a 10-K and a later 10-Q both speaking to FY2026): pick by, in order, (a) **newest `filing_date`**, (b) tie → **most specific `form_type`** via a fixed rank (`10-K/A` > `10-Q/A` > `10-K` > `10-Q`), (c) tie → **lexicographically-largest `accession`** (total order, fully deterministic). The chosen artifact and the rejected candidates' accessions are recorded in the trace.
- **Staleness (item 5).** An artifact whose newest guidance `period` has already lapsed relative to as-of (i.e. the period it references ends before as-of AND actuals would exist) is **stale ⇒ not consumed for a numeric override** (it is still captured into the bundle with a `stale` flag and a `Warnings` tag, so the absence-of-anchor is auditable). Concretely: a `period` parsed to a fiscal-year-end `≤ as-of` is stale. This keeps a FY2026 capex anchor from steering a valuation run after FY2026 closed.

**On-disk layout (fixtures, Phase 2):**

```
testdata/guidance/                         # fixtures live under testdata for Phase 2 (no prod dir yet)
  <CIK_zeropadded>/                         # e.g. 0000002488/
    <accession>.json                        # e.g. 0000002488-26-000012.json  (immutable, content-addressed)
    <accession>.json                        # a second filing for the same CIK (conflict-resolution fixtures)
```

> **Rationale (testdata, not a prod config dir).** Phase 2 has no extraction tool and no real artifacts; standing up a production `config/guidance/` directory now would ship an empty, load-bearing-by-accident path. Fixtures under `testdata/guidance/` keep the loader's directory root **injectable** (a `GuidanceRoot` config field defaulting to empty = "no guidance, ever"). Production stays on the empty root ⇒ absent path ⇒ byte-identical (NF1). Phase 3 flips the default root to the real directory in one line + a config bump.

### Decision 3 — The assumption-authority resolver (§9)

New package **`internal/services/valuation/authority`** (leaf-ish; imports `guidance` + `profile`, imported by `valuation`). It is the single place precedence is decided, mirroring the `params` package's "single authoritative knob-resolution path" pattern.

**Precedence (highest first), per assumption:**

| Level | Source tag | Supplies |
|---|---|---|
| 1 | `user_override` | `ValuationOptions`/`params.Overrides` explicit per-request value (already resolved by `params`; authority records the tag, does not re-resolve). |
| 2 | `guidance` | a `validated`, high-confidence, non-stale envelope with explicit value + accepted evidence — **near-term (year 1–2) only**. |
| 3 | `profile` | the Layer-A `AssumptionProfile` reinvestment/margin trajectory (the default for the vast majority). |
| 4 | `historical` | TTM / normalized fallback when profile params are absent. |
| 5 | `default` | conservative constants. |

**Resolver output (`authority.Resolution`):**

```go
type Resolution struct {
    // Per-assumption final source + value provenance (for the diagnostic block).
    Sources map[string]AssumptionSource   // key e.g. "capex_year1", "operating_margin_year1"
    // Near-term anchors the DCF reinvestment model consumes (year 1, optionally year 2).
    Anchors NearTermAnchors
    // Source tags + guardrail messages for result.Warnings (RM-1 / VAL-1 convention).
    Warnings []string
    // What gets captured into the bundle (the selected artifact or the absent record).
    Captured *guidance.Artifact
    GuidanceStatus string                  // "validated" | "no_explicit_guidance_found" | "stale" | "absent" | "low_confidence"
}

type AssumptionSource struct {
    Level   string  // "user_override" | "guidance" | "profile" | "historical" | "default"
    Value   float64
    Detail  string  // e.g. "accession=0000002488-26-000012 period=FY2026 conf=0.82"
}
```

**`NearTermAnchors`** carries only what Phase 2 actually steers: `CapExYear1 *float64`, `OperatingMarginYear1 *float64`, `RevenueGrowthYear1 *float64` (and optional `*Year2`). Nil ⇒ no anchor ⇒ Layer A runs unchanged for that knob.

### Decision 4 — Per-assumption source recording shape

Add **`AssumptionSources map[string]entities.AssumptionSourceValue`** to `entities.ValuationResult` (additive, `omitempty` ⇒ default-path responses stay byte-identical). `AssumptionSourceValue` lives in `entities` to keep the import boundary clean (entities must not import `authority`), mirroring how `AppliedOverrideValue` already lives there for the `params` feature:

```go
type AssumptionSourceValue struct {
    Source string  `json:"source"`           // "user_override"|"guidance"|"profile"|"historical"|"default"
    Detail string  `json:"detail,omitempty"` // e.g. "accession=… period=FY2026 conf=0.82"
}
```

**Wire shape (only when guidance OR a non-default source actually fired):**
```jsonc
"assumption_sources": {
  "capex_year1":            { "source": "guidance", "detail": "accession=0000002488-26-000012 period=FY2026 conf=0.82" },
  "operating_margin_year1": { "source": "profile",  "detail": "hypergrowth_profitable:high_growth" },
  "reinvestment_trajectory":{ "source": "profile",  "detail": "sales_to_capital fade=5y" }
}
```

Additionally, a human-readable **source tag** is appended to `result.Warnings`, exactly like the RM-1 `revenue_base: …` and the Layer-A `reinvestment_model: …` lines, e.g.:
`guidance: capex_year1 anchored from accession 0000002488-26-000012 (FY2026, conf=0.82, midpoint $1.50B)`.

> **Rationale.** Two surfaces, both already conventional in this codebase: a structured `assumption_sources` map (mirrors `applied_overrides`) for machine/dashboard consumption + replay diffing, and a `Warnings` source tag (mirrors RM-1/BUG-015/Layer-A) for human log auditing. No new surface invented.

### Decision 5 — The loader

New **`guidance.Loader`** in `internal/services/valuation/guidance`:

- Constructed with a `Root string` (the directory; empty = disabled). Production wires empty ⇒ disabled ⇒ absent path everywhere ⇒ NF1.
- `Load(cik string, asOf time.Time) (Resolution, error)` where `Resolution` is `{Artifact *Artifact; Absent bool; Trace LoadTrace}`:
  1. If `Root == ""` ⇒ return `{Absent: true}` immediately (the production no-op).
  2. List `Root/<cik>/` in **sorted** order (NF2). No subdir ⇒ `{Absent: true}` (cached as such — absence is a first-class, cacheable result, §8.3 item 3).
  3. Parse each file; for each, recompute and verify `artifact_sha256` (hard error on mismatch — F2).
  4. Filter to eligible candidates: `filing.filing_date ≤ asOf`.
  5. Apply conflict resolution (Decision 2) per `period_end`, then select the single artifact whose guidance is newest + non-stale relative to `asOf`. If the winner has `status == "no_explicit_guidance_found"` ⇒ return it (a positive "we looked, found nothing" record). If no eligible candidate ⇒ `{Absent: true}`.
- **Caching of absence:** the loader memoizes per `(cik, asOf-as-eligibility-bucket)` so a re-resolve in the same request is free; absence is cached identically to a hit. (In-process, request-scoped; no cross-request global state — keeps replay hermetic.)
- **Replay seam:** the loader exposes `LoadFromBundle(raw []byte) (Resolution, error)` used by the replay path so it consumes the captured `09-guidance.json` rather than scanning the live directory (NF3). The replay `BundleGuidanceGateway` returns `ErrBundleMissingPayload`-style absence (no panic) when the bundle predates guidance capture — matching the existing replay gateway discipline so an old bundle simply replays on the absent path.

### Decision 6 (§12.4) — Year-1 anchor mechanic: **MIDPOINT-as-clamp on the modeled near-term value, range → diagnostic**

**Chosen:** a high-confidence guidance envelope sets the **midpoint** `m = (value_low + value_high) / 2` and **clamps the Layer-A modeled year-1 (and optionally year-2) value toward that midpoint** — specifically the anchored value *replaces* the modeled near-term value when guidance is `validated` + high-confidence + non-stale, while the modeled trajectory carries every later year unchanged. The full `(value_low, value_high)` range is recorded in the trace + `assumption_sources` detail for a future sensitivity pass (not wired into intrinsic value in Phase 2).

**Why midpoint-replace over the two alternatives:**

| Option | Verdict | Reason |
|---|---|---|
| Feed range into sensitivity only | Rejected for Phase 2 | midas has no Monte-Carlo/sensitivity-into-intrinsic-value machinery (parent §3 non-goal). It would record guidance but never let it *change* a value — Phase 2 could not prove the consumption path actually moves a number. |
| Hard clamp `[low, high]` on the modeled value | Rejected as primary | When the modeled near-term value already lies inside `[low, high]`, a clamp is a no-op — so a deliberately-anchored fixture might not change the output, making the "high-confidence anchors year-1" test non-deterministic w.r.t. the modeled baseline. |
| **Midpoint-replace (chosen)** | **Selected** | Deterministic and *observable*: a high-confidence fixture always moves year-1 to `m`, so the §11.3 "fixture anchors year-1" test asserts a concrete value. It satisfies §9.3 ("anchors near-term") exactly — only year 1–2, never the terminal, never later years, so guidance **cannot dominate intrinsic value** (the bulk of EV for a reinvestment-heavy firm is years 3-N + terminal, all from Layer A). |

**§9.3 dominance guardrail (enforced, not just asserted):** the anchor applies to **at most year 1 and year 2**; the resolver hard-refuses to write any anchor index ≥ 3. A post-anchor assertion in `applyReinvestmentModel` confirms the anchored years are a strict prefix of the horizon. This is the structural guarantee that "AI guidance does not dominate intrinsic value."

**Mechanic at the seam:** `applyReinvestmentModel` already builds `in.RevenueGrowthRates`, `in.BaseOperatingMargin`/`TargetOperatingMargin`, and the sales-to-capital curve. The anchor injects the near-term values:
- `RevenueGrowthYear1` ⇒ overrides `in.RevenueGrowthRates[0]` (and `[1]` if Year2 present).
- `OperatingMarginYear1` ⇒ seeds the year-1 point on the margin-convergence path (the convergence schedule then proceeds from the anchored start; later years still converge toward `TargetOperatingMargin`).
- `CapExYear1` (absolute USD) ⇒ converted to a year-1 sales-to-capital / reinvestment override for the first projection year only.
Each anchored knob writes its `AssumptionSources["…_year1"] = {Source:"guidance", …}` entry. When no anchor is present, **every one of these is skipped and `applyReinvestmentModel` is byte-identical to Layer A** (NF1).

### Decision 7 (§12.5) — CalculationVersion: **NO bump in Phase 2; stays `4.7`**

**Decision:** Phase 2 does **not** bump `CalculationVersion`. It stays at `4.7`.

**Rationale.** `CalculationVersion` exists to invalidate caches and flag a *math change to the default valuation path*. Phase 2 ships **zero math change on the default (absent-guidance) path** — that is the explicit NF1 invariant and the entire point of the phase. A value only changes when a fixture is present, which never happens in production in Phase 2 (empty `GuidanceRoot`). Bumping the version would needlessly invalidate every cached 4.7 result and would falsely signal a default-path change. Replay determinism is instead pinned by the **captured `09-guidance.json` bundle stage** (a present-or-absent record travels with each bundle), not by the version string. The bump belongs to **Phase 3**, when a real extraction tool first changes a *production* value — at which point CalcVersion → 4.8 (or as sequenced) is the correct cache-invalidation signal.

> Guard: the existing `service_test.go` `CalculationVersion == "4.7"` pins stay green unchanged — that is itself a Phase-2 regression assertion (no accidental default-path drift).

### Decision 8 — Replay-bundle capture (§8.4)

Capture the resolution as **`09-guidance.json`** (the open slot between `08-assumption-profile.json` and `10-clean-*.json`, which is exactly where guidance resolves in the flow):

- On a hit: capture the **selected `guidance.Artifact`** verbatim (the immutable object, including its `artifact_sha256`) plus a small `resolution` envelope (`{ status, selected_accession, rejected_accessions, anchors_applied, stale }`).
- On absent / no-guidance: capture `{ status: "absent" | "no_explicit_guidance_found", … }` so the bundle records *that guidance was considered and none applied*. This makes "no guidance" a captured fact, so a replay of an absent-path valuation re-derives absence identically.
- Wiring: a `b.Snapshot(ctx, "guidance.resolved", "09-guidance.json", resolution)` + `b.AddSchemaVersion("GuidanceResolution", 1)` via a small `SetGuidanceResolution` helper on `*artifact.Bundle` (mirrors `SetAssumptionProfileManifest`). Nil-safe receiver ⇒ test/no-bundle paths are unaffected.
- Replay consumes it via the loader's `LoadFromBundle` seam (Decision 5 / NF3). Old bundles (no `09-`) replay on the absent path — bit-for-bit with their original (which also had no guidance), preserving every existing baseline in `artifacts/tier2-baseline/`.

### Files touched (create / modify)

**Create**
- `internal/services/valuation/guidance/artifact.go` — the `Artifact`/`Envelope`/`Filing`/`AIProvenance`(guidance flavor) types, `schema_version` const, enum constants, canonical-serialization + `ComputeArtifactSHA256`.
- `internal/services/valuation/guidance/loader.go` — `Loader`, `Load`, `LoadFromBundle`, conflict resolution, staleness, absence caching.
- `internal/services/valuation/guidance/validate.go` — structural validation (value_low ≤ value_high, period non-empty, margin basis present, evidence-required-for-numeric).
- `internal/services/valuation/authority/resolver.go` — `Resolver`, `Resolution`, `NearTermAnchors`, the §9 precedence + §9.3 guardrails.
- `internal/services/valuation/guidance.go` (in the `valuation` package) — `resolveGuidance(ctx, cik, asOf, profile, opts) authority.Resolution`, the thin service-level orchestrator that calls loader+resolver, writes `assumption_sources`, appends warnings, and captures the bundle stage.
- `testdata/guidance/0000002488/…json` — AMD fixtures: one high-confidence FY-capex artifact, one low-confidence artifact, one `no_explicit_guidance_found` artifact, plus a conflict pair (10-K + later 10-Q on the same period).
- Test files alongside each (`*_test.go`).

**Modify**
- `internal/services/valuation/service.go` — call `resolveGuidance` after profile resolution (~after L933); thread `authority.Resolution` into `applyReinvestmentModel`; append guidance warnings + stamp `result.AssumptionSources`.
- `internal/services/valuation/reinvestment.go::applyReinvestmentModel` — accept `anchors authority.NearTermAnchors`; apply the midpoint anchor to year 1–2 of `RevenueGrowthRates`/margin/capex; assert anchors are a strict near-term prefix; **byte-identical when anchors are nil**.
- `internal/core/entities/valuation.go` — add `AssumptionSources map[string]AssumptionSourceValue` + the `AssumptionSourceValue` type (additive, omitempty).
- `internal/observability/artifact/bundle.go` — add `SetGuidanceResolution` helper (mirrors `SetAssumptionProfileManifest`).
- `internal/observability/replay/` — add `BundleGuidanceGateway` reading `09-guidance.json` (absent-not-panic), wire it through `replay.Module` and the loader's `LoadFromBundle` seam.
- `internal/config/config.go` — add `Valuation.GuidanceRoot string` (default `""` = disabled).

---

## API / Contract shapes

### Guidance artifact (fixture) — high-confidence hit
```jsonc
{
  "schema_version": "1.0.0",
  "status": "validated",
  "issuer": { "ticker": "AMD", "cik": "0000002488" },
  "filing": {
    "accession": "0000002488-26-000012", "form_type": "10-K",
    "filing_date": "2026-02-04", "period_end": "2025-12-28",
    "sec_url": "https://www.sec.gov/...", "source_doc_sha256": "…"
  },
  "source_selection": { "sections": ["Item 7 MD&A", "Liquidity and Capital Resources"], "selected_text_sha256": "…" },
  "extraction": {
    "capex_guidance": {
      "value_low": 1.4e9, "value_high": 1.6e9, "unit": "absolute_usd", "period": "FY2026",
      "basis": { "gross_or_net": "gross", "cash_or_accrual": "cash", "gaap_or_non_gaap": "gaap", "consolidated_or_segment": "consolidated" },
      "confidence": 0.82,
      "evidence": [ { "quote": "we expect capital expenditures of approximately $1.5 billion in fiscal 2026", "location": "Item 7, ¶ Liquidity" } ]
    },
    "margin_guidance": [], "revenue_guidance": []
  },
  "ai_provenance": { "provider": "hand_authored", "model_name": "fixture", "model_version": "n/a", "temperature": 0.0,
                     "prompt_sha256": "…", "schema_sha256": "…", "raw_response_sha256": "…", "extraction_code_git_sha": "" },
  "validation": { "status": "validated", "confidence": 0.82, "warnings": [], "normalization_rules_version": "1.0.0", "validator_version": "fixture-1.0.0" },
  "artifact_sha256": "…"
}
```

### Guidance artifact — mandatory absent form
```jsonc
{
  "schema_version": "1.0.0",
  "status": "no_explicit_guidance_found",
  "issuer": { "ticker": "AMD", "cik": "0000002488" },
  "filing": { "accession": "0000002488-26-000099", "form_type": "10-Q", "filing_date": "2026-05-01", "period_end": "2026-03-29" },
  "validation": { "status": "no_explicit_guidance_found", "confidence": 0.0, "warnings": [], "normalization_rules_version": "1.0.0", "validator_version": "fixture-1.0.0" },
  "artifact_sha256": "…"
}
```

### `09-guidance.json` bundle stage (captured)
```jsonc
{ "resolution": { "status": "validated", "selected_accession": "0000002488-26-000012",
                  "rejected_accessions": [], "anchors_applied": ["capex_year1"], "stale": false },
  "artifact": { /* the selected Artifact verbatim, incl artifact_sha256 */ } }
```

### `assumption_sources` on `FairValueResponse` — only when non-default sources fired
```jsonc
"assumption_sources": {
  "capex_year1": { "source": "guidance", "detail": "accession=0000002488-26-000012 period=FY2026 conf=0.82 midpoint=$1.50B" }
}
```

### Critical abstractions (module boundaries)
- `guidance.Loader` — the ONLY code that touches the fixture filesystem / bundle bytes. Owns immutability + content-addressing + conflict + staleness. No valuation logic.
- `authority.Resolver` — the ONLY code that decides §9 precedence + §9.3 guardrails. No filesystem, no DCF math.
- `valuation.resolveGuidance` — the thin service seam: loader → resolver → (warnings, sources, anchors, bundle capture). Imports both; imported by nothing else.
- `applyReinvestmentModel` — the ONLY place the anchor touches the DCF inputs (DCF path only).

---

## Module Descriptions

- **`internal/services/valuation/guidance`** — pure artifact domain: types, (un)marshal, canonical hashing (reusing the `hash.go` discipline), structural validation, and the deterministic loader (accession-keyed, immutable, content-addressed, conflict/staleness rules, absence caching, bundle seam). No imports of `models`/`entities` beyond what's needed; no DCF math. Leaf-style, like `profile`.
- **`internal/services/valuation/authority`** — the §9 precedence engine + §9.3 guardrails. Input: `*guidance.Artifact` (or absent), `*profile.ResolvedProfile`, the user-override set; output: `authority.Resolution` (per-assumption sources, near-term anchors, warnings, captured artifact, status). The "single authoritative source-decision path", analogous to `params`.
- **`valuation.resolveGuidance` + `applyReinvestmentModel` changes** — the consumption seam. `resolveGuidance` runs once per valuation between profile resolution and DCF-input build; `applyReinvestmentModel` applies the near-term anchor on the DCF path only, byte-identical when no anchor exists.
- **`artifact.SetGuidanceResolution` + replay `BundleGuidanceGateway`** — capture + hermetic replay of the resolution.

---

## Tasks by Agent

### BACKEND (small, reviewable chunks — sequence as listed)

- **B1 — guidance types + hashing + validation.** Create `guidance/artifact.go` + `guidance/validate.go`: `Artifact`/`Envelope`/`Filing`/`AIProvenance` types, enums, `schema_version="1.0.0"`, `ComputeArtifactSHA256` (canonical, wall-clock-free, sorted-key — reuse the `hash.go` rule), structural validators. Tests: round-trip (incl. `no_explicit_guidance_found`), `value_low ≤ value_high`, period-required, margin-basis-required, evidence-required-for-numeric, content-hash determinism.
- **B2 — loader.** Create `guidance/loader.go`: `Loader{Root}`, `Load(cik, asOf)`, sorted scan, content-hash verify (hard error), filing-date eligibility, conflict resolution (newest → form-rank → accession-lex), staleness, absence-as-first-class + caching, `LoadFromBundle`. Tests: hit, absent (no dir / empty dir), conflict pair resolves deterministically, stale not consumed, hash-mismatch hard-errors, `Root==""` ⇒ always absent.
- **B3 — authority resolver.** Create `authority/resolver.go`: §9 precedence, §9.3 guardrails (validated+confidence+evidence required for numeric; vague-prose ⇒ context-only; near-term-only enforcement; low-conf/absent ⇒ fall through), midpoint anchor (Decision 6), per-assumption `Sources` + warnings. Tests: each precedence level wins in turn; guardrails reject laundering; anchor index ≥ 3 refused; midpoint computed correctly.
- **B4 — entities + bundle helper + config.** Add `AssumptionSources`/`AssumptionSourceValue` to `entities/valuation.go` (omitempty); `SetGuidanceResolution` to `artifact/bundle.go`; `Valuation.GuidanceRoot` to `config.go` (default `""`). Tests: default-path JSON omits `assumption_sources` (byte-identity guard); nil-bundle `SetGuidanceResolution` no-ops.
- **B5 — service wiring.** Add `valuation/guidance.go::resolveGuidance`; call it in `performValuation` after profile resolution; thread anchors into `applyReinvestmentModel`; append warnings; stamp `result.AssumptionSources`; capture `09-guidance.json`. **Critical:** the empty-root path must be byte-identical — add `TestPerformValuation_NoGuidance_ByteIdentical` against a 4.7 golden. Also wire the alt-model path to stamp `assumption_sources` with `source=profile/historical` (no value change).
- **B6 — replay capture seam.** Add `BundleGuidanceGateway` (absent-not-panic) reading `09-guidance.json`; wire through `replay.Module` + `LoadFromBundle`. Tests: a guidance-consuming bundle replays bit-for-bit; an old bundle without `09-` replays on the absent path.
- **B7 — fixtures.** Author `testdata/guidance/0000002488/*.json`: high-confidence capex hit, low-confidence, `no_explicit_guidance_found`, conflict pair (10-K + later 10-Q same period), one stale artifact. Compute correct `artifact_sha256` for each (a tiny `go test -run TestComputeFixtureHashes -update`-style helper or a one-off generator under the test package — NOT a prod tool).

### QA

- **Q1 — absent-guidance byte-identity (NF1, load-bearing).** Full `go test ./... -count=1` green; `TestDDM_LegacyPath_BitForBit`; FFO (PLD/EQIX) + revenue_multiple (MXL) primaries unchanged; recompute-shadow snapshots byte-identical; `service_test.go` `CalculationVersion=="4.7"` pins unchanged. Confirm a production-config (empty `GuidanceRoot`) valuation is byte-identical to the 4.7 engine.
- **Q2 — fixture high-confidence anchors year-1.** With the AMD high-confidence fixture + as-of after the filing, assert year-1 capex/margin/growth anchored to the midpoint, `assumption_sources["capex_year1"].source=="guidance"`, the `guidance: …` warning tag present, and the anchor confined to years 1–2 (year ≥ 3 untouched).
- **Q3 — low-confidence / absent ⇒ Layer A fallback.** Low-confidence fixture and `no_explicit_guidance_found` fixture each produce a result byte-identical to the no-fixture Layer-A run (same DCF value), with the resolution captured and a fall-through warning recorded.
- **Q4 — replay bit-for-bit with fixture captured.** Capture a bundle from a guidance-consuming valuation; `cmd/replay --from=parsed --diff-stages` matches `17-response.json`; the `09-guidance.json` stage round-trips; an old baseline bundle still replays.
- **Q5 — contract round-trips.** Every fixture (incl. `no_explicit_guidance_found`) marshals/unmarshals losslessly and re-validates; content-hash mismatch hard-errors; conflict pair resolves to the documented winner.
- **Q6 — determinism / guardrails.** No wall-clock/map-order leakage into result/trace/bundle; a "vague bullish prose, no value" fixture contributes context only (no numeric anchor); a fixture trying to anchor year 3 is refused.

### REVIEWER (focus)

- The **NF1 byte-identity** proof: confirm every guidance path is gated so the empty-root / nil-anchor case is provably a no-op (read `applyReinvestmentModel` and `resolveGuidance` for any unconditional mutation).
- **§9.3 dominance guardrail** is structural (near-term-prefix-only), not just asserted — confirm the year ≥ 3 refusal and the post-anchor prefix assertion.
- **Determinism** of the loader (sorted scan, hash excludes wall-clock, conflict total-order) and the **replay hermeticity** seam (bundle bytes, not live FS).
- The §12.5 **no-bump** decision: confirm no production value can change in Phase 2 (so not bumping is correct) and the CalcVersion pins are intentionally left green.

---

## Load-bearing invariants + Test plan

### Load-bearing invariants (must stay GREEN at every commit)
- **Absent-guidance = byte-identical to Layer A 4.7** for every un-fixtured ticker and every alt-model (DDM/FFO/revenue_multiple). This is the master invariant — it is why the common case is unaffected.
- `TestDDM_LegacyPath_BitForBit` (JPM/BAC/WFC) — DDM is dividend-derived; guidance is DCF-path-only and never anchors on the absent path.
- FFO (PLD/EQIX) + revenue_multiple (MXL) primary values bit-for-bit.
- `TestCalculateDCF_LegacyProportional_BitForBit` and the Layer-A `applyReinvestmentModel` no-anchor identity.
- recompute-shadow snapshots byte-identical (`git diff --quiet internal/integration/testdata/recompute-shadow/` exits 0).
- `service_test.go` `CalculationVersion == "4.7"` pins unchanged (Phase 2 does not bump — Decision 7).
- Existing `artifacts/tier2-baseline/` bundles replay unchanged (no `09-` ⇒ absent path).
- Full `go test ./... -count=1` EXIT=0.

### §11.3 test plan (Phase 2 scope; section-selection N/A — no extraction tool)
| Area | Test | Asserts |
|---|---|---|
| Fixture high-confidence anchors yr-1 | Q2 | midpoint anchor applied to yr 1–2; `assumption_sources`=guidance; warning tag; yr ≥ 3 untouched |
| Low-confidence / absent ⇒ Layer A fallback | Q3 | DCF value byte-identical to no-fixture run; fall-through warning; resolution captured |
| Replay bit-for-bit with fixture captured | Q4 | `--from=parsed --diff-stages` matches; `09-guidance.json` round-trips; old bundles replay |
| Contract round-trips (incl. absent) | Q5 | lossless marshal/unmarshal + re-validate; content-hash mismatch hard-errors; conflict resolves to documented winner |
| Prompt/content-hash stability (AIProvenance-style) | B1 | `artifact_sha256` + `prompt_sha256` deterministic, wall-clock-free, sorted-key |
| Validator (structural) | B1/B3 | value_low ≤ value_high; period required; margin basis required; evidence required for numeric; vague-prose ⇒ context-only |
| Loader determinism | B2 | sorted scan; staleness; conflict total-order; absence cached |
| Byte-identity (NF1) | Q1 | full suite + DDM/FFO/RM + shadow + version pins all green; empty-root production-config identical to 4.7 |

> **Model-quality eval (§11.3 last row)** is explicitly **out of scope for Phase 2** — there is no model. It belongs to Phase 3's quarantined eval set.

---

## Spec / doc updates (small, concrete)

- **`docs/refactoring/spec/dcf-reinvestment-and-filing-intelligence-spec.md`** — update the Implementation-Status table Phase-2 row from "PLANNING — NOT STARTED" to "DESIGN COMPLETE (this spec)"; resolve §12.4 (midpoint-replace, near-term-only) and §12.5 (no CalcVersion bump in Phase 2 — stays 4.7; bump deferred to Phase 3) by linking here.
- **This file** is the Phase-2 design of record; a Phase-2 closeout doc (`docs/refactoring/archive/layer-b-phase2-guidance-fixture-closeout.md`) is authored at ship time.
- **CLAUDE.md gotcha bullet (at ship):** "Layer B Phase 2 — guidance fixtures are consumed via `internal/services/valuation/guidance` + `authority`; production runs with empty `GuidanceRoot` ⇒ absent path ⇒ byte-identical to 4.7. The anchor is near-term-only (yr 1–2) and DCF-path-only; CalcVersion stays 4.7 (no production value changes in Phase 2)."
- **No `docs/openapi.yaml` change required** for the absent path; when documenting `assumption_sources`, add it as an `omitempty` object on `FairValueResponse` (additive, optional) at ship time.

---

## Acceptance Criteria

1. With empty `GuidanceRoot` (production default), every valuation is byte-identical to the Layer-A 4.7 engine — DDM/FFO/RM primaries, recompute-shadow, version pins, full suite all green (NF1).
2. A high-confidence AMD fixture + as-of after its filing anchors year-1 (and optionally year-2) capex/margin/growth to the envelope midpoint; `assumption_sources["…_year1"].source == "guidance"`; a `guidance: …` source tag appears in `Warnings`; years ≥ 3 are untouched (§9.3 dominance guardrail holds structurally).
3. A low-confidence fixture and a `no_explicit_guidance_found` fixture each yield a DCF value byte-identical to the no-fixture Layer-A run, with the resolution captured and a fall-through warning recorded.
4. The loader is deterministic: sorted scan, content-hash verified (mismatch hard-errors), filing-date eligibility against as-of, conflict resolved by newest → form-rank → accession-lex, staleness excluded from numeric override, absence cached as a first-class result.
5. A guidance-consuming valuation captures `09-guidance.json` and replays bit-for-bit via `cmd/replay --from=parsed --diff-stages`; bundles predating guidance capture replay on the absent path unchanged.
6. Every fixture (incl. the absent form) round-trips and re-validates; the `ai_provenance` block mirrors midas's `AIProvenance` shape and hashing discipline.
7. `CalculationVersion` is unchanged at `4.7`; no production value changes in Phase 2.

---

## Assumptions and Open Questions

**Assumptions (used to finalize this design):**
- The valuation's as-of is the existing `s.clock.Now()` Clock seam (production wall-clock; replay → `manifest.started_at`). No new as-of parameter is threaded through the public `CalculateValuation` signature in Phase 2.
- `FinancialData` carries `CIK` + per-period `FilingDate` but NOT `accession`. Phase 2 keys the loader by `(CIK, as-of)` and treats `accession` as the artifact's recorded immutable identity; plumbing a live accession from the SEC gateway to the valuation is a Phase-3 concern (the extraction tool already works in accession space).
- Fixtures live under `testdata/guidance/` with an injectable `GuidanceRoot` defaulting to empty; no production `config/guidance/` directory ships in Phase 2.
- The only value Phase 2 actually steers is the DCF reinvestment-model near-term (year 1–2) inputs via `applyReinvestmentModel`; alt-models record sources but do not change values.

**Non-blocking questions (deferred to Phase 3, do not block BACKEND):**
- The exact `confidence ≥ threshold` cutoff value (Phase 2 uses a single config constant, default `0.70`; calibration is Phase 3).
- Whether `revenue_guidance` should anchor a year-1 *revenue level* vs the year-1 *growth rate* (Phase 2 anchors growth-rate[0] to stay inside the existing `RevenueGrowthRates` seam; revenue-level anchoring is a Phase-3 refinement).
- Sensitivity-range consumption of `(value_low, value_high)` into intrinsic value (recorded now; wired never in Phase 2 — parent §3 non-goal).

**Decisions already made (no input needed):** §12.4 (midpoint-replace, near-term-only) and §12.5 (no bump; stays 4.7) — see Decisions 6 and 7.

---

## Next Steps

1. **BACKEND** executes B1 → B7 in order (each independently testable; B1–B3 are leaf packages with no service dependency and can land first).
2. **QA** runs Q1–Q6 after B5/B6; Q1 (byte-identity) gates every commit.
3. **REVIEWER** focuses on the NF1 no-op proof, the §9.3 structural guardrail, loader determinism, and replay hermeticity.
4. Author the Phase-2 closeout + the CLAUDE.md gotcha bullet at ship; then hand off to **Phase 3** (the extraction tool) whose output validates against this contract.

**HANDOFF_TO:** BACKEND
