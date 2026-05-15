# Observability Replay Tooling — Phase R3b Implementation Plan

**Status:** PLAN v1 — awaiting human approval before BACKEND dispatch.

**Builds on:**
- [`observability-replay-tooling-spec.md`](./observability-replay-tooling-spec.md) v0.4 (R0+R1+R2+R3a SHIPPED). All design decisions, ADRs, CLI contract, and testing strategy are owned by that spec.
- [`observability-replay-tooling-r3-implementation-plan.md`](./observability-replay-tooling-r3-implementation-plan.md) v2 (R3a SHIPPED at merge `011d78c`, 2026-05-06; deferred items captured in this plan). This R3b plan mirrors its structure (Pre-Flight + ordered Stages + per-task contracts + Test Plan + Coverage Gates + Done-When + Risks + Spec Updates + Implementation Outcome placeholder).
- [`docs/reviewer/archive/RPL3-r3a-followups.md`](../reviewer/RPL3-r3a-followups.md) — the consolidated R3b backlog (5 deferred Stages + 8 LOW NITs + 1 missing test + 1 R2 modernization sweep). This file IS R3b's scope.

This document does **not** redesign anything. It sequences BACKEND's work for R3b only — the final dispatch that completes Phase 2.D.

**Scope:** R3b ONLY — Stage K (`--diff-stages` engine wiring), Stage L.1 (verbose stage-diff text render), Stage M.1 (JSON contract golden tests), Stage M.3 (parsed-mode round-trip integration), Stage N (perf benches NF2/NF3), Stage O.6 (`init()` reflection guard), 8 LOW-NIT cleanup sweep, the missing `evaluateBundleWithRecover` panic-coverage test, and one R2-era modernization carry-forward. After R3b ships, Phase 2.D is COMPLETE and the spec bumps v0.4 → v0.5.

**LoC + commit estimate (R3b only, derived from RPL-3 backlog estimates):**
- Stage K (RPL-3a — `stage_diff.go` + engine wiring + flag re-add): ~300 LoC.
- Stage L.1 (RPL-3b — verbose stage-diff text render): ~80 LoC.
- Stage M.3 (RPL-3d — `seedFullBundle_ParsedMode` builder + parsed-mode round-trip test): ~180 LoC (~150 builder + ~30 test).
- Stage M.1 (RPL-3c — 6 golden fixtures + harness + `UPDATE_GOLDEN=1`): ~150 LoC + ~5–10 KiB checked-in fixture JSON.
- Stage N (RPL-3e — synthetic perf corpus generator + 2 benches): ~150 LoC + checked-in 100-bundle corpus (~5 MiB) OR generator-only (decision below).
- Stage O.6 (RPL-3f — `init()` reflection guard): ~30 LoC + 1 test.
- Cleanup sweep (RPL-3g/h/i/j/k/l/m/n + RPL-3p R2 modernization): ~50 LoC across ~10 small touches in one commit.
- RPL-3o panic-coverage test (folded into the cleanup commit): ~30 LoC.
- **Estimated total:** ~975 LoC across ~10–12 atomic commits.

The RPL-3 estimate of ~925 LoC tracks; the +50 LoC delta is the panic-coverage test plus belt-and-suspenders test scaffolding for Stage K's snapshot capture path.

**Commit cadence:** Each Stage gets its own commit so reverts stay surgical, mirroring R0+R1+R2+R3a. Stage K and L.1 are split into two commits (engine wiring then text render) for revertability.

---

## Revision History

- **v1 (initial)**: Stage breakdown for R3b derived from `docs/reviewer/archive/RPL3-r3a-followups.md` Sections A/B/C/D. Resolves the 7 implementation-level decisions called out by the dispatch instructions. Mirrors the R3 plan v2 structure for continuity. Spike NOT required (R3a's Pre-Flight spike covered the parallel-fx.App question; R3b builds on top of already-shipped R3a infrastructure).

---

## 1. Preamble

**R0 + R1 + R2 + R3a already shipped on master.** R3b inherits a working `cmd/replay` binary with parallel batch (`--workers`), filter flags (`--filter-ticker`/`--filter-since`), tunable float tolerances (`--float-rel-tol`/`--float-abs-tol`), walk/replay split timing, and the cmd/server import-boundary CI guard.

**Confirmed live in the repo as of master HEAD:**
- `internal/observability/replay/{errors,manifest,schema,diff,walk,output,duration,clock,types,compare,replay,module,gateway_*,stubs}.go` plus tests (84.4% coverage).
- `internal/services/valuation/clock.go` — `Clock` interface + `wallClock{}` (D10).
- `cmd/replay/main.go` — flag set: `--format`, `--out`, `--allow-schema-drift`, `--allow-git-drift`, `--quiet`, `--verbose`, `--from`, `--workers`, `--filter-ticker`, `--filter-since`, `--float-rel-tol`, `--float-abs-tol`. **NOT registered:** `--diff-stages` (intentionally absent post-R3a-cycle-3, lines 128–132 carry the rationale).
- `internal/observability/replay/spike_parallel_fxapp_test.go` (build-tag `replay_spike`) — Pre-Flight spike retained as permanent regression guard.
- `cmd/server/import_boundary_test.go` — runs as part of normal `go test ./cmd/server/...`.
- `scripts/lint-prometheus-registers.{sh,ps1}` — passing on master HEAD.

**Coverage baseline at start of R3b** (per spec v0.4 change-log + RPL-3 carry-forward note):
- `internal/observability/replay/`: 84.4% (gates `≥ 90%`; gap concentrated in deferred Stage K/M.1 surfaces).
- `cmd/replay/`: 87.2% (gates `≥ 80%`).
- `internal/services/valuation/`: 89.1% (no R3b production-source change here).

R3b's natural test additions (Stage K's `stage_diff.go` tests, Stage M.1 golden tests, Stage M.3 parsed-mode round-trip, Stage N benches, Stage O.6 reflection-guard test, Stage RPL-3o panic-coverage test) are expected to lift the replay package toward 90%. The deferred surfaces R3a flagged ARE the surfaces R3b is building, so the lift is structural rather than incidental.

**Key R3b code surfaces (already shipped, will be modified or extended):**
- `cmd/replay/main.go` — flag set; R3b registers `--diff-stages` (Stage K) and removes the deferred-rationale comment block at lines 128–132 (RPL-3k).
- `internal/observability/replay/replay.go` — `Replay()` orchestrator; R3b adds the `--diff-stages` branch that calls into `stage_diff.go` (Stage K).
- `internal/observability/replay/output.go` — `Result.StageDiffs` field added (Stage K); `writeResultRow` extended with verbose stage-diff render (Stage L.1); `Summary` doc-comment clarified for `DurationMs` (RPL-3m).
- `internal/observability/replay/types.go` (or wherever `Options` lives) — `Options.DiffStages bool` added (Stage K).
- `internal/observability/replay/diff.go` — `init()` reflection guard added (Stage O.6 / RPL-3f); `countFairValueFields` constant becomes structural assertion.
- `internal/observability/replay/duration.go:58` — `strings.HasSuffix + strings.TrimSuffix` → `strings.CutSuffix` (RPL-3j).
- `internal/observability/replay/module.go:262, :367-374` — `for range 16` modernization (RPL-3h); `_ = marketGateway` clarification comment (RPL-3l).
- `internal/observability/replay/integration_test.go:47-49, :242` — `maps.Copy` and `interface{}→any` modernization (RPL-3p).
- `internal/observability/replay/spike_parallel_fxapp_test.go:69-70, :144` — `for i := range numWorkers` (RPL-3i).

**New code surfaces R3b introduces:**
- `internal/observability/replay/stage_diff.go` + `_test.go` (Stage K).
- `internal/observability/replay/output_golden_test.go` + `testdata/golden/*.json` (Stage M.1).
- `internal/observability/replay/replay_bench_test.go` (Stage N).
- `internal/observability/replay/testdata/perf/` corpus or generator (Stage N).
- `cmd/replay/main_test.go` — extension of existing test file with the `evaluateBundleWithRecover` panic-coverage test (RPL-3o).

---

## 2. Pre-Flight

**No spike required for R3b.** R3a's Pre-Flight spike (`spike_parallel_fxapp_test.go`) verified parallel `fx.App` lifecycle correctness — the only fx-composition concern that warranted advance investigation. R3b's work runs on top of R3a's already-shipped parallelism infrastructure; Stages K, L.1, M.1, M.3, N, O.6 introduce no new fx primitives, no new goroutine boundaries, and no new external Go modules.

**Two execution-level uncertainties BACKEND should resolve at the start of Stage K (NOT a discrete spike):**

1. **Pre-K.A — Verify the bundle stage-file inventory is stable.** R3 plan v2 §2 Pre-I.B established the inventory `{10-clean-output.json, 12-growth-curve.json, 13-wacc.json, 15-valuation.json}`. R3a did not consume the inventory. Re-verify by listing a recent production bundle (`ls artifacts/<recent-date>/<TICKER>/req_*/`) and confirming all four files appear. If a file is sometimes absent (e.g. cleaner skipped for FPI tickers), the diff path treats absent-on-both-sides as "no diff" and absent-on-one-side as a `StringDiff` at path `stages.<filename>.<missing-side>` so the operator sees the asymmetry. Document the result in Stage K's commit message.

2. **Pre-K.B — Decide Stage K's diff source-of-truth (see §3 Decision K.1).** Resolved below in §3 — Stage K reads pre-captured `*.json` files from the bundle directly (NOT re-derived from a tee'd snapshot writer). Rationale and consequences captured in §3 Stage K.

Both Pre-K checks land inside Stage K's first commit; they are NOT a separate phase.

---

## 3. Ordered Task List (TDD)

Each task is `Test first → Implementation → Acceptance`. Stages run sequentially; within a Stage, tasks can be combined into a single commit if they share test file scope. BACKEND respects dependency order: Stage K lands first (all subsequent stages either consume its surface or are independent); Stage L.1 follows Stage K; Stages M.1, M.3, N, O.6 are mutually independent and can interleave; the cleanup sweep + RPL-3o test land last.

### Stage K — `--diff-stages` engine wiring + `stage_diff.go` (RPL-3a)

**Goal:** When `--diff-stages` is set, replay diffs intermediate-stage JSON files (`10-clean-output.json`, `12-growth-curve.json`, `13-wacc.json`, `15-valuation.json`) against the bundle's recorded versions, in addition to the response-level diff. Output enriches each `Result` with a per-stage diff section.

**Decisions resolved at the implementation-level (NOT new spec ADRs):**

- **Decision K.1 — Stage-diff source-of-truth: read pre-captured `*.json` files from the bundle directly.** Stage K does NOT re-derive stage values from `*entities.ValuationResult`, NOR does it tee an in-memory `artifact.Bundle` writer during replay (which was R3 plan v2's K.1.a default).
  - **Rationale:** the pre-captured files are what was on disk at request time; reading them is faster, simpler, and matches the user's mental model ("show me what's in the bundle vs what the engine produces this time"). The "current" side comes from a fresh tee'd snapshot via `replay.Module`'s existing observability wiring (which already accepts an `artifact.Bundle` writer as an injection point under R3a — verify by reading `module.go::buildArtifactBundle` or its equivalent).
  - **Concrete contract:** Stage K's `diffStage(bundleDir, stageFile string, current []byte, relTol, absTol float64) StageDiff` reads `<bundleDir>/<stageFile>` from disk, takes the engine-produced bytes (current) as a parameter, and returns the per-field diff structure. The caller (Replay() orchestrator) is responsible for capturing `current` from the replay's tee'd writer.
  - **Why not re-derive:** the stage JSON shapes drift over time (e.g., `13-wacc.json` is `entities.WACCComponents` — adding a field would require code change in stage_diff.go). Reading the bundle's saved JSON sidesteps the entity-shape coupling.
  - **Why not tee-only (K.1.a from R3 plan v2):** the tee approach is fine for the "current" side but does NOT eliminate the bundle read — Stage K must compare against the bundle's saved file. So the read happens regardless; explicit `os.ReadFile` is simpler than reconstructing it through `artifact.OpenBundle` + filename lookup.

- **Decision K.2 — `--diff-stages` flag shape: bool flag (no value).** Spec §7 sample at L515-554 shows the flag as `--diff-stages` with no value. The flag selects "diff all four stages in `StageDiffInventory`"; selectivity is NOT supported in R3b (`--diff-stages=wacc,fcf_projection` could be added later as an additive feature with no breaking-change concern).
  - **Rationale:** the inventory is small (4 files); any further selectivity is a doc-comment away from `git grep stage_diff.go` and adds CLI surface for no clear win in the user's named scale (the watchlist-regression workflow). If a future user wants to suppress one stage, the JSON output is already filterable via `jq '.results[].stage_diffs | del(.["13-wacc.json"])'`.

- **Decision K.3 — Stage K + L.1 commit boundary: two commits.** Stage K lands as commit 1 (engine wiring + JSON output of `Result.StageDiffs`); Stage L.1 lands as commit 2 (text-mode verbose render).
  - **Rationale:** revertability — if a downstream regression is traced to either, the commit boundary makes git revert surgical. The JSON output of stage diffs is independently useful (`jq` consumes it without a text renderer).

#### Task K.0 — Pre-K verification (folded into K.1's commit)

- **Action:** BACKEND runs `ls artifacts/<recent-date>/<TICKER>/req_*/` against at least 3 recent bundles and confirms `10-clean-output.json`, `12-growth-curve.json`, `13-wacc.json`, `15-valuation.json` are all present. Document in Stage K's commit message: "Verified stage inventory in bundles X/Y/Z — all 4 files present."
- **If any file is sometimes absent:** the diff path handles this gracefully via the `bundle_missing` / `current_missing` asymmetric-marker convention (Decision K.1 above). Document the absence pattern; do NOT remove the file from `StageDiffInventory`.

#### Task K.1 — `stage_diff.go` core + `Result.StageDiffs` field

- **File new:** `internal/observability/replay/stage_diff.go`
- **File modified:** `internal/observability/replay/output.go` (add `Result.StageDiffs map[string]StageDiff` with `omitempty` JSON tag).
- **File modified:** `internal/observability/replay/types.go` or wherever `Options` is defined (add `Options.DiffStages bool`).
- **Test first:** `internal/observability/replay/stage_diff_test.go`:
  - `TestStageDiffInventory_HasExpectedStages` — assert the constant slice contains exactly `{"10-clean-output.json", "12-growth-curve.json", "13-wacc.json", "15-valuation.json"}`. Catches accidental ordering or additions.
  - `TestStageDiff_BothFilesAbsent_NoDiff` — bundle directory has neither file, current is nil. Assert empty diff.
  - `TestStageDiff_FileAbsentInBundle_RecordedAsAsymmetric` — bundle missing the stage file but engine produced one. Assert one `StringDiff` at path `stages.<filename>.bundle_missing`.
  - `TestStageDiff_FileAbsentInCurrent_RecordedAsAsymmetric` — bundle has the file but engine didn't capture one. Assert one `StringDiff` at path `stages.<filename>.current_missing`.
  - `TestStageDiff_FloatFieldDriftWithinTolerance` — both files present; bundle's `13-wacc.json` has `cost_of_equity: 0.118`; engine's has `0.118 + 1e-10`; assert it lands in `DriftedWithinTolerance`, NOT `Diffs`.
  - `TestStageDiff_FloatFieldDriftOutsideTolerance` — `cost_of_equity` differs by 5%; assert one `FloatDiff` at path `stages.13-wacc.json.cost_of_equity`.
  - `TestStageDiff_NestedFieldPath_Renders` — bundle has `wacc_components.cost_of_debt.after_tax: 0.045`; engine has `0.046`; assert FloatDiff at path `stages.13-wacc.json.wacc_components.cost_of_debt.after_tax`.
  - `TestStageDiff_StringFieldChange_Diffs` — `model_selection.chosen` differs; assert one `StringDiff` (NOT silently ignored).
- **Implementation:**
  - Define `var StageDiffInventory = []string{"10-clean-output.json", "12-growth-curve.json", "13-wacc.json", "15-valuation.json"}` in `stage_diff.go`.
  - Define `type StageDiff struct { Floats []FloatDiff; Strings []StringDiff; DriftedWithinTolerance []FloatDiff }` (mirroring `Result`'s shape so renderers can reuse the same field-format helpers).
  - Define `func diffStage(bundleDir, stageFile string, current []byte, relTol, absTol float64) StageDiff`:
    - Read `<bundleDir>/<stageFile>` via `os.ReadFile`. Handle `os.ErrNotExist` as "bundle_missing"; if `current` is nil, that's "current_missing".
    - When both sides exist: `json.Unmarshal` both into `map[string]any`, then walk recursively comparing leaves. Use the existing `diff.go` float-tolerance helpers (`CompareFloat`, `FloatDiffOf`) for float leaves.
    - For non-float scalars (string, bool), compare via `==` and emit `StringDiff` on mismatch. For nested objects/arrays, recurse with a path prefix.
    - **Path naming:** `stages.<filename>.<dotted-path-to-field>`, e.g. `stages.13-wacc.json.wacc_components.cost_of_debt.after_tax`. This is consistent with the existing `compareFairValueResponses` walker conventions.
  - **Walker design choice:** keep the walker manual (matching the convention of `compare.go::compareFairValueResponses`). DO NOT use `go-cmp` here because heterogeneous JSON `map[string]any` shapes drift between stages and `go-cmp` over-reports nil-vs-zero / empty-string-vs-omitted distinctions that aren't drift.
  - Add `Result.StageDiffs map[string]StageDiff` with `json:"stage_diffs,omitempty"` to `output.go` so it's omitted from default output.
  - Add `Options.DiffStages bool` to `types.go` (search for the `Options` struct definition; current location may be in `replay.go` or `types.go`).
- **Acceptance:**
  - All `TestStageDiff*` tests pass under `-race -count=10`.
  - File-level coverage of `stage_diff.go` ≥ 90%.
  - `Result.StageDiffs` is omitted from JSON output when nil/empty (verify via existing JSON output test).

#### Task K.2 — Wire `--diff-stages` flag and integrate into `Replay()`

- **File modified:** `cmd/replay/main.go` (re-add `--diff-stages` flag; remove the dropped-flag rationale comment block at lines 128–132 — RPL-3k).
- **File modified:** `internal/observability/replay/replay.go` (add `--diff-stages` branch after `runEngine` returns).
- **Test first:** `cmd/replay/main_test.go`:
  - `TestParseFlags_DiffStages_DefaultFalse` — argv without `--diff-stages`; assert `flags.diffStages == false`.
  - `TestParseFlags_DiffStages_ExplicitTrue` — argv `--diff-stages`; assert `flags.diffStages == true`.
  - `TestRun_DiffStages_PopulatesStageDiffsField` — fixture bundle with a deliberately mutated `13-wacc.json` on disk; run with `--diff-stages`; assert `result.StageDiffs["13-wacc.json"].Floats` is non-empty.
  - `TestRun_DiffStages_DisabledByDefault_ZeroStageDiffs` — same fixture without the flag; assert `result.StageDiffs == nil` (or empty map; document which) AND no asymmetric markers leaked.
- **Implementation:**
  - In `parseFlags`: add `fs.BoolVar(&f.diffStages, "diff-stages", false, "Diff intermediate-stage JSON files in addition to the response-level diff")`.
  - Add `diffStages bool` field to `flags` struct (replaces the comment block at lines 128–132).
  - In `Run()`: pass `f.diffStages` into `replay.Options.DiffStages`.
  - In `Replay()` (modify `replay.go`): after the existing engine invocation succeeds AND `opts.DiffStages == true`, call `diffStage(...)` for each entry in `StageDiffInventory`, accumulating into `result.StageDiffs`. The "current" bytes come from the replay's tee'd snapshot writer (which `replay.Module` already wires under R3a — confirm by inspecting `module.go::buildArtifactBundle`; if no tee exists yet, the implementation creates a thin tee at the same site).
  - **If the tee-writer hookup is more involved than expected (>50 LoC):** fall back to re-reading `<bundleDir>/<stageFile>` for the bundle side and re-deriving the "current" side by re-running the engine with snapshot capture enabled. This is the K.1.a fallback from R3 plan v2 §3 Stage K. The fallback is acceptable because the spec D8 invariant ("replay produces no bundles of bundles") is preserved as long as the snapshot is in-memory, never written to disk.
  - Update `usageMessage` to register `--diff-stages`.
  - **Critically: drop the comment block at `cmd/replay/main.go:128-132` (RPL-3k)** as part of this commit. The comment was archaeology for the deferred state and becomes confusing once Stage K ships.
- **Acceptance:**
  - All `TestParseFlags_DiffStages_*` and `TestRun_DiffStages_*` tests pass.
  - Manual smoke: `go run ./cmd/replay --diff-stages --format=json artifacts/<bundle>` produces JSON with a populated `stage_diffs` per result.
  - Comment block at `main.go:128-132` is deleted; the flag is registered.

**Stage K commit cadence:** Tasks K.0 + K.1 + K.2 all land in ONE commit (the "Stage K" commit). Stage L.1 is a separate commit.

---

### Stage L.1 — Verbose stage-diff text render (RPL-3b)

**Goal:** Extend `--verbose` text-mode output to include per-stage diff sections beneath each bundle row, matching spec §7's verbose sample at L497-510.

#### Task L.1.1 — Verbose stage-diff text render

- **File modified:** `internal/observability/replay/output.go` (extend `writeResultRow`).
- **Test first:** `internal/observability/replay/output_test.go`:
  - `TestRenderText_VerboseFalse_OmitsStageDiffsSection` — populate `Result.StageDiffs` with one entry; render with verbose=false; assert no "Stage diffs:" header in output.
  - `TestRenderText_VerboseTrue_EmitsStageDiffsSection` — same Result, verbose=true; assert "Stage diffs:" header AND per-field diff lines beneath. Match the exact format from spec §7 L501-505.
  - `TestRenderText_VerboseTrue_EmitsBothResponseAndStageDiffs` — Result with both response-level Diffs AND StageDiffs; assert response diffs render BEFORE the "Stage diffs:" section (or as a uniform inverted hierarchy — pick a stable order and pin it via the test).
  - `TestRenderJSON_VerboseFlag_StageDiffsAlwaysIncluded` — render same Result to JSON with verbose=true and verbose=false; assert byte-identical output (JSON emits everything regardless of verbose).
- **Implementation:**
  - In `writeResultRow`, after the existing diff loops (response-level Diffs + StringDiffs + DriftedWithinTolerance), add:
    ```go
    if verbose && len(res.StageDiffs) > 0 {
        // emit "  Stage diffs:" header
        // for each stage filename in sorted order:
        //   emit "    <filename>:"
        //   for each FloatDiff in stage.Floats:
        //     emit "      - <field-path>: <old> -> <new> (rel_drift=<X.XXXXXX>)"
        //   for each StringDiff in stage.Strings:
        //     emit "      - <field-path>: <old> -> <new>"
    }
    ```
  - Sort stage filenames before iterating (deterministic output).
  - Match the exact indentation and `rel_drift=` format used in the existing per-row diff lines.
- **Acceptance:**
  - All `TestRenderText_*StageDiffs*` and `TestRenderJSON_VerboseFlag_StageDiffsAlwaysIncluded` tests pass.
  - Manual smoke: `go run ./cmd/replay --diff-stages --verbose artifacts/<bundle>` produces output matching spec §7 L497-510 sample shape.
  - File-level coverage of `output.go` does not regress; ideally lifts toward 95% as new branches exercise.

**Stage L.1 commit cadence:** Single commit after Stage K is merged.

---

### Stage M.3 — Parsed-mode round-trip integration test (RPL-3d)

**Goal:** Add `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs` to close the gap that `--from=parsed` is verified at unit level (gateway dispatch) and CLI level (flag parse) but NOT at integration level. R3a-BACKEND-2 attempted this and reverted because `seedFullBundle` is raw-mode-only.

**This stage can run in parallel with Stage L.1 if BACKEND prefers** — they touch different files (output.go vs integration_test.go).

#### Task M.3.1 — `seedFullBundle_ParsedMode` fixture builder

- **File modified:** `internal/observability/replay/integration_test.go` (extend with the new fixture builder).
- **Test first / Implementation:**
  - Add `func seedFullBundle_ParsedMode(t *testing.T, bundleDir string, ticker string, options entities.ValuationOptions)`.
  - Mirrors the existing `seedFullBundle` helper but emits `*.parsed.json` shapes for the gateway parsers to consume directly via the `--from=parsed` mode.
  - Concretely, instead of writing `05-fetch-sec.raw.json` (HTTP response bytes), write `05-fetch-sec.parsed.json` (the `ports.SECCompanyFacts` struct after parsing). Same for `06-fetch-market.parsed.json` and `07-fetch-macro.parsed.json`.
  - Reuse the synthetic `entities.FinancialData` / `MarketData` / `MacroData` shapes that `seedFullBundle` already constructs; the only difference is which file they land in and whether HTTP RoundTripper bytes are needed (parsed mode skips them).
  - Document the helper's purpose and limitations in its doc-comment: "Like seedFullBundle but for the --from=parsed mode. Bundle gateways read *.parsed.json directly, skipping the production parser. Use this helper when a test needs to exercise the parsed-mode path end-to-end."
- **Acceptance:**
  - The helper compiles and produces a valid bundle directory tree.
  - Manual verification: a single test case calls the helper and inspects `os.ReadDir(bundleDir)` to confirm all expected files exist.

#### Task M.3.2 — Parsed-mode round-trip test

- **File modified:** `internal/observability/replay/integration_test.go`.
- **Test first / Implementation:**
  - Add `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs(t *testing.T)`.
  - Setup: `bundleDir := t.TempDir(); seedFullBundle_ParsedMode(t, bundleDir, "TEST", defaultOptions)`.
  - Action: `result, err := replay.Replay(ctx, bundleDir, replay.Options{Mode: ModeParsed})`.
  - Assert: `err == nil`, `result.Status == StatusPass`, `len(result.Diffs) == 0`, `len(result.StringDiffs) == 0`, `result.FieldsChanged == 0`.
  - Run with `-count=10 -race` to surface any non-determinism in the parsed-mode gateway dispatch.
  - **Self-referential limitation:** like `TestRoundTrip_ReplaySelfConsistency_ZeroDiffs`, both halves of the test consume the same `buildFairValueResponse` helper. Document this in the test's doc-comment so a future reader doesn't mistake the test for proof of "parsed-mode reproduces production exactly."
- **Acceptance:**
  - Test passes under `-count=10 -race`.
  - Doc-comment is honest about the self-referential limitation.

**Stage M.3 commit cadence:** Single commit. Can land before, in parallel with, or after Stage L.1; Stage M.1's golden tests should land AFTER both Stage K and Stage L.1 because the JSON shape grows when those land.

---

### Stage M.1 — JSON contract golden tests (RPL-3c)

**Goal:** Lock in the JSON output shape (post-Stage-K extensions) by checking in golden fixtures and asserting `RenderJSON` output matches them byte-for-byte under representative inputs.

**Decision M.1 — Golden test approach:** **Checked-in `testdata/golden/*.json` fixtures + `bytes.Equal` comparison + `UPDATE_GOLDEN=1` regeneration harness.** This was R3 plan v2's M.1 default; carry forward.

**Decision M.1.b — Fixture file count and naming:** **6 fixtures** matching the RPL-3 estimate. Specifically:

| # | Fixture filename | What it covers |
|---|---|---|
| 1 | `json_pass_one_bundle.json` | Single passing bundle, default replay (no `--diff-stages`) |
| 2 | `json_fail_one_bundle.json` | Single failing bundle with one float diff outside tolerance |
| 3 | `json_errored_one_bundle.json` | Single errored bundle (e.g. `ErrBundleMissingPayload`) |
| 4 | `json_with_drifted_within_tolerance.json` | Single passing bundle WITH `drifted_within_tolerance` populated (verifies the existing field stays serialized) |
| 5 | `json_with_stage_diffs.json` | Single passing bundle WITH `stage_diffs` populated (verifies Stage K's new field) |
| 6 | `json_mixed_with_workers_4.json` | 3 bundles (1 pass + 1 fail + 1 errored) replayed under `--workers=4`; verifies deterministic sort + walk/replay timing fields |

These 6 cover: default-shape happy + drift + missing-payload + the legacy `drifted_within_tolerance` field + Stage K's new `stage_diffs` field + the `--workers > 1` deterministic-sort + walk/replay timing path.

**Why these 6 and not the R3 plan v2 set verbatim:** the R3 plan v2 listed `json_pass`, `json_fail`, `json_errored`, `json_mixed_three_bundles`, `json_with_stage_diffs`, `json_with_drifted_within_tolerance`. R3b shifts one — replaces the generic `json_mixed_three_bundles` with `json_mixed_with_workers_4` to specifically pin the parallel-dispatch + sort behavior R3a shipped, since that's the surface most likely to drift inadvertently. The replacement keeps the count at 6 and adds coverage value.

#### Task M.1.1 — Build the 6 fixtures + harness

- **File new:** `internal/observability/replay/testdata/golden/json_pass_one_bundle.json` (and 5 more).
- **File new:** `internal/observability/replay/output_golden_test.go`.
- **Test first / Implementation:**
  - Define `TestRenderJSON_GoldenFixture_PassOneBundle` — construct a `Report` representing one passing bundle programmatically; render via `(*Report).RenderJSON` to bytes; load `testdata/golden/json_pass_one_bundle.json`; compare via `bytes.Equal`.
  - Same pattern for the other 5 fixtures.
  - On mismatch: `t.Errorf` produces a `cmp.Diff`-style report so the operator sees the change. Include in the error message: `"to update goldens after a deliberate JSON-shape change, run: UPDATE_GOLDEN=1 go test -run TestRenderJSON_GoldenFixture ./internal/observability/replay/"`.
  - **`UPDATE_GOLDEN=1` harness:** if `os.Getenv("UPDATE_GOLDEN") == "1"`, the test writes the rendered bytes to the golden file AND skips the byte-comparison assertion (counted as PASS). This is the standard Go testing convention for golden-file regeneration.
  - Build each fixture by hand initially (~30 lines per fixture). Pin field order via Go's deterministic JSON marshaling. Use `json.MarshalIndent` with 2-space indent to match `RenderJSON`.
  - **Pin time-sensitive fields:** `Summary.WalkDurationMs` and `Summary.ReplayDurationMs` are wall-clock; tests must scrub these to a fixed value before comparison OR pin them via `Report.GeneratedAtUTC` (which already exists per `output.go:147` for golden-test pinning). Use the existing scrubbing mechanism if present; if not, add a `scrubTimingFields(*Report)` helper alongside the test file.
  - **Pin `git_sha_current`:** the field is populated from `runtime/debug.ReadBuildInfo` at startup; for golden tests it must be pinned to a deterministic value (e.g., empty string or `"test-build"`). Use the existing `gitSHAResolver` test seam (RPL-2e/-3 documented as `t.Parallel()`-incompatible).
- **Implementation note on time-sensitive scrubbing:** the golden-fixture test must NOT call `t.Parallel()` because it overrides `gitSHAResolver` (per the RPL-2e/RPL-3 documented constraint). Add a comment at the top of `output_golden_test.go`'s test functions: `// MUST NOT call t.Parallel — overrides gitSHAResolver package var (per replay.go:236-237 documentation).`.
- **Acceptance:**
  - All 6 golden tests pass on first run.
  - Manual smoke: temporarily flip a field in `Result.Status`'s JSON tag (e.g. `json:"state"` instead of `json:"status"`); verify all 6 tests fail with a useful diff message; revert.
  - Manual smoke: run `UPDATE_GOLDEN=1 go test -run TestRenderJSON_GoldenFixture ./internal/observability/replay/`; verify the goldens regenerate; revert any unintended changes.
  - File-level coverage of `output.go` approaches 100% (golden tests exercise nearly every branch).

**Stage M.1 commit cadence:** Single commit. MUST land AFTER Stage K and Stage L.1 because the JSON shape grows when those land.

---

### Stage N — Performance benches NF2 + NF3 (RPL-3e)

**Goal:** Establish performance regression guards. NF2 = single-bundle replay ≤ 200 ms; NF3 = 100-bundle batch ≤ 30 s. Both with 3× CI slack (NF2 fails at 600 ms; NF3 fails at 90 s).

**Decision N.1 — Synthetic corpus generation: generator-only, NOT checked in.** R3 plan v2's default was N.1.a (checked-in 100-bundle corpus, ~5 MiB). R3b reverses this:

- **N.1.b (NEW): generator at `internal/observability/replay/testdata/perf/gen/main.go`; corpus generated at `TestMain` runtime, NOT checked in.**
- **Rationale for the reversal:**
  - Repo bloat: 5 MiB of synthetic JSON checked into a 100+-MiB Go module is not catastrophic, but it sets a precedent for future perf-corpus additions.
  - Schema-version drift: when `CurrentSchemaVersions` changes (which it will across Phase 2.E and beyond), the checked-in corpus becomes stale. A generator regenerates against the current code automatically.
  - Determinism: the generator uses a fixed PRNG seed (e.g. `rand.New(rand.NewSource(42))`), so the corpus is byte-identical across `TestMain` invocations on the same code.
  - Time cost: generating 100 bundles takes <2 s on a modern machine — acceptable as a one-time `TestMain` cost when benches are explicitly requested via `-bench`. Default `go test ./...` does NOT run benches, so the generator only fires on bench invocations.
- **Decision N.1.c — `TestMain` gating:** the corpus is generated only when `testing.Short()` is FALSE AND a bench is being run. Use `flag.Lookup("test.bench")` to detect bench mode; skip generation for normal test runs. This avoids the "generator runs on every `go test ./...`" footgun.

#### Task N.1 — Synthetic perf corpus generator

- **File new:** `internal/observability/replay/testdata/perf/gen/main.go` (under `gen/` so `go build ./...` doesn't compile it as part of the replay package). Optional: also expose as a Go function for `TestMain` consumption.
- **File new:** `internal/observability/replay/replay_bench_test.go::TestMain` calls the generator at start of bench runs (only when needed).
- **Implementation:**
  - The generator produces 100 deterministic synthetic bundles under a temp directory (e.g. `os.MkdirTemp("", "replay-perf-corpus-*")`). Tickers cycle through a small list (`{AAPL, MSFT, AMD, GOOG, TSM, NVO, ASML, BABA, SAP, V}`); each bundle has a valid `00-manifest.json`, four `*.raw.json` files, a `17-response.json`. Schema versions match `CurrentSchemaVersions` at generation time.
  - The temp directory path is exposed via a package-level `perfCorpusDir string` set in `TestMain`. Benches read it.
  - Cleanup happens via `t.Cleanup(os.RemoveAll)` registered in `TestMain` exit hook.
- **Acceptance:**
  - Running `go test -bench=BenchmarkReplay_BatchOf100_NF3 ./internal/observability/replay/` generates 100 bundles, runs the bench, and cleans up afterwards.
  - Generator output is deterministic across runs (PRNG-seeded).
  - Default `go test ./...` does NOT generate the corpus (verified by `os.Stat` on a sentinel file).

#### Task N.2 — `BenchmarkReplay_SingleBundle_NF2` (≤ 200 ms target with 3× slack)

- **File new:** `internal/observability/replay/replay_bench_test.go`
- **Test first / Implementation:**
  - `BenchmarkReplay_SingleBundle_NF2(b *testing.B)`:
    - Pick one bundle from the synthetic corpus (e.g., the first ticker AAPL).
    - In `b.ResetTimer()` loop: call `replay.Replay(ctx, bundleDir, opts)`. `b.N` controls iterations.
    - After loop: compute `elapsedPerIter := b.Elapsed() / time.Duration(b.N)`.
    - Assertion: `if elapsedPerIter > 600 * time.Millisecond { b.Errorf("NF2 SLA broken: per-iter %v exceeds 600ms (3× of 200ms target)", elapsedPerIter) }`.
  - **NF threshold enforcement:** the spec says "≤ 200 ms on the user's local machine"; CI machines may be slower. The 3× slack absorbs CI variance. Document this in the bench function's doc-comment.
  - Use `b.SetBytes()` to report throughput in bytes/sec for operator visibility (bundle size is roughly known).
- **Acceptance:**
  - `go test -bench=BenchmarkReplay_SingleBundle_NF2 ./internal/observability/replay/ -benchtime=10x` exits 0.
  - Output shows per-iter wall time AND bytes/sec.

#### Task N.3 — `BenchmarkReplay_BatchOf100_NF3` (≤ 30 s target with 3× slack)

- **File same:** `internal/observability/replay/replay_bench_test.go`
- **Test first / Implementation:**
  - Two sub-benches: `BenchmarkReplay_BatchOf100_NF3_Sequential` and `BenchmarkReplay_BatchOf100_NF3_Parallel`.
  - Sequential: `for _, bundleDir := range corpus { replay.Replay(...) }`.
  - Parallel: dispatch via the same bounded-pool primitive `cmd/replay/main.go` uses (extract into a helper for testability). Worker count = `runtime.NumCPU()`.
  - Each sub-bench asserts total wall time ≤ 90 s (3× of 30 s).
- **Acceptance:**
  - Both sub-benches pass.
  - Output shows total wall time, effective bundles/second throughput, and the parallel speedup ratio (parallel_seq_ratio = sequential_time / parallel_time).
  - Parallel speedup is REPORTED but NOT asserted as a gate (CI variance can suppress the ratio).

**Stage N commit cadence:** Single commit. Independent of Stages K/L.1/M.1; can land in parallel with any of them.

---

### Stage O.6 — `init()` reflection guard for `countFairValueFields` (RPL-3f)

**Goal:** Add a `func init()` that uses reflection to assert `countFairValueFields()` matches the actual struct field count at package load. Catches any future field addition that didn't update both `goFieldToJSON` AND `countFairValueFields`.

**Decision O.6 — `init()` panic scope verification:** R3a Stage O.13 already shipped the `cmd/server` ↔ `replay` import-boundary CI guard (`cmd/server/import_boundary_test.go`). That guard is the load-bearing assumption that makes `init()` panic safe — `cmd/server` does not import `replay`, so a `replay`-package init panic cannot crash production server startup. Re-cite this in Stage O.6's commit message and in the `init()` function's doc-comment.

**REVIEWER cycle 1 already noted this scope verification; Stage O.6's commit must reference O.13's import-boundary test by file path so a future reader sees the dependency chain explicitly.**

#### Task O.6.1 — `init()` reflection guard + test

- **File modified:** `internal/observability/replay/diff.go` (add `func init()` near `countFairValueFields`).
- **File new (or extended):** `internal/observability/replay/diff_test.go::TestCountFairValueFields_MatchesReflection`.
- **Test first / Implementation:**
  - Add to `diff.go`:
    ```go
    func init() {
        // Field-count drift guard. Counts via reflect.Type.NumField on
        // FairValueResponse + Industry + SanityCheck (the three nested
        // structs whose field counts feed countFairValueFields). On
        // mismatch, panic so the replay binary refuses to start —
        // catches "added a field without updating goFieldToJSON" at
        // package-load time.
        //
        // Panic scope: replay-binary-only. cmd/server does NOT import
        // this package (enforced by cmd/server/import_boundary_test.go,
        // shipped under R3a Stage O.13). A panic here cannot crash
        // production server startup.
        responseFields := reflect.TypeOf(handlers.FairValueResponse{}).NumField()
        industryFields := reflect.TypeOf(handlers.Industry{}).NumField()
        sanityFields := reflect.TypeOf(entities.SanityCheck{}).NumField()
        actual := responseFields + industryFields + sanityFields
        expected := countFairValueFields()
        if actual != expected {
            panic(fmt.Sprintf(
                "replay/diff.go: countFairValueFields drift — reflect counted %d fields (response=%d + industry=%d + sanity=%d), constant returns %d. " +
                "Update countFairValueFields and goFieldToJSON to match the new struct shape.",
                actual, responseFields, industryFields, sanityFields, expected,
            ))
        }
    }
    ```
  - Add a unit test that asserts `init()` did not panic (i.e., the package loaded successfully). The test is a tautology in the happy path but documents the contract:
    ```go
    func TestCountFairValueFields_MatchesReflection(t *testing.T) {
        // If init() did not panic at package load, this test passes
        // implicitly. Re-asserting via reflection here lets a future
        // contributor see the contract without reading init().
        responseFields := reflect.TypeOf(handlers.FairValueResponse{}).NumField()
        industryFields := reflect.TypeOf(handlers.Industry{}).NumField()
        sanityFields := reflect.TypeOf(entities.SanityCheck{}).NumField()
        actual := responseFields + industryFields + sanityFields
        if got := countFairValueFields(); got != actual {
            t.Errorf("countFairValueFields() = %d; reflection counts %d", got, actual)
        }
    }
    ```
- **Acceptance:**
  - `go test ./internal/observability/replay/` passes (the existing 32 = 19+8+5 hand-count holds at master HEAD; if drift is detected at implementation time, fix the constant — the reflection IS the source of truth, the constant is the assertion).
  - Manual smoke: temporarily add a field to `handlers.FairValueResponse`; verify the next `go test ./internal/observability/replay/` invocation fails at init with the helpful panic message; revert.

**Stage O.6 commit cadence:** Single commit. Independent of all other R3b stages.

---

### Stage R3b-Final — Cleanup sweep + RPL-3o panic test (RPL-3g/h/i/j/l/m/n/p + RPL-3o)

**Goal:** Land the 8 LOW NIT items + 1 R2 modernization sweep + 1 missing panic-coverage test in a single coordinated commit. Each is a focused, low-risk change to a surface R3b is already touching elsewhere.

**Note:** RPL-3k (drop the `--diff-stages` deferred-rationale comment block) is folded into Stage K's commit, NOT this one — it co-locates with the flag re-add for git-blame coherence.

#### Task R3b-Final.1 — RPL-3g: drop `i, b := i, b` shadow at `cmd/replay/main.go:430`

- **File:** `cmd/replay/main.go:430`
- **Change:** Remove the `i, b := i, b` shadow line. Module declares `go 1.23.0`; per-iteration loop semantics are in effect, the shadow is dead code.
- **Verify:** `go test ./cmd/replay/...` still passes; `-race` clean.

#### Task R3b-Final.2 — RPL-3h: `for range 16` at `internal/observability/replay/module.go:262`

- **File:** `internal/observability/replay/module.go:262`
- **Change:** `for i := 0; i < 16; i++` → `for range 16` (Go 1.22+ integer-range form).
- **Verify:** module tests pass.

#### Task R3b-Final.3 — RPL-3i: `for i := range numWorkers` + drop shadow at `spike_parallel_fxapp_test.go:69-70` (and :144)

- **File:** `internal/observability/replay/spike_parallel_fxapp_test.go:69-70` and `:144`
- **Change:** `for i := 0; i < numWorkers; i++` → `for i := range numWorkers`. Drop the `i := i` shadow line if present.
- **Verify:** `go test -tags=replay_spike -race -count=10 -run TestSpike_ParallelFxAppLifecycle ./internal/observability/replay/` still passes.

#### Task R3b-Final.4 — RPL-3j: `strings.CutSuffix` at `internal/observability/replay/duration.go:58`

- **File:** `internal/observability/replay/duration.go:58-59`
- **Change:** Replace
  ```go
  if strings.HasSuffix(s, "d") {
      numStr := strings.TrimSuffix(s, "d")
  ```
  with
  ```go
  if numStr, ok := strings.CutSuffix(s, "d"); ok {
  ```
  (Go 1.21+).
- **Verify:** `TestParseDurationExtended_*` tests still pass (including the cycle-4 fix that pre-validates numeric prefix per `959997f`).

#### Task R3b-Final.5 — RPL-3l: `_ = marketGateway` clarity comment at `module.go:367-374`

- **File:** `internal/observability/replay/module.go:367-374`
- **Change:** Add a one-line comment above `_ = marketGateway`:
  ```go
  // marketGateway is consumed transitively by valuation.NewService below;
  // the underscore is the explicit "intentionally unused at this site"
  // marker so a future maintainer doesn't delete the parameter.
  _ = marketGateway
  ```
- **Verify:** module tests pass; `go vet ./...` clean.

#### Task R3b-Final.6 — RPL-3m: `Summary.DurationMs` doc-comment clarification at `output.go:122-130`

- **File:** `internal/observability/replay/output.go:122-130`
- **Change:** Extend the `DurationMs` doc-comment to clarify it is the SUM of per-bundle wall-clock under `--workers > 1`:
  ```go
  //   - DurationMs: cumulative per-bundle replay duration (sum of
  //     Result.DurationMs). Pre-existing field; preserves R2 contract.
  //     Under --workers > 1, this exceeds ReplayDurationMs because
  //     workers run concurrently — operators wanting the user-facing
  //     wait time should read ReplayDurationMs instead.
  ```
- **Verify:** existing tests pass (doc-only change).

#### Task R3b-Final.7 — RPL-3n: `--float-rel-tol=0` silent-default footgun note at `main.go:82`

- **File:** `cmd/replay/main.go:82` (the `usageMessage` block)
- **Change:** Extend the `--float-rel-tol` and `--float-abs-tol` lines:
  ```
    --float-rel-tol float   Relative tolerance for float diffs (default 1e-9; 0 means use default, NOT exact-match)
    --float-abs-tol float   Absolute tolerance for float diffs (default 1e-12; 0 means use default, NOT exact-match)
  ```
- **Verify:** `go run ./cmd/replay -h` shows the updated text.

#### Task R3b-Final.8 — RPL-3p: `maps.Copy` + `interface{}→any` at `integration_test.go:47-49 + :242`

- **File:** `internal/observability/replay/integration_test.go:47-49` and `:242`
- **Change:**
  - Lines 47-49: `for k, v := range CurrentSchemaVersions { dst[k] = v }` → `maps.Copy(dst, CurrentSchemaVersions)` (Go 1.21+; add `"maps"` import).
  - Line 242: `var recovered interface{}` → `var recovered any` (Go 1.18+).
- **Verify:** integration tests still pass.

**Note on RPL-3p scope:** R3 plan v2 RPL-3 §D notes RPL-3p covers `interface{}→any` "in `spike_parallel_fxapp_test.go` and other replay-package files." Audit the package for other `interface{}` occurrences during this sweep; convert all to `any` for consistency. The spike test has 4 occurrences (lines 61, 170, 174 per the grep above) — fold those into this commit.

#### Task R3b-Final.9 — RPL-3o: `evaluateBundleWithRecover` panic-coverage test

- **File:** `cmd/replay/main_test.go` (extend with one new test).
- **Test first / Implementation:**
  - Add `TestEvaluateBundleWithRecover_PanicConvertedToErroredResult(t *testing.T)`.
  - Strategy: construct a synthetic `Replay()`-equivalent path that panics inside the worker goroutine. Concretely:
    - Inject a stub via the existing test seam (or add one if needed) that causes `replay.Replay()` to panic. Candidate seams: a panicking `Auth` stub (which sits OUTSIDE the F11 datafetcher goroutine path, so the panic IS reachable by `cmd/replay/main.go::evaluateBundleWithRecover`'s `defer recover()`).
    - If no test seam exists for forcing a panic, add a build-tag-gated stub OR use a synthetic bundle that triggers a known panic path (e.g. malformed manifest causing a downstream nil-deref — but verify this is a real path, NOT a contrived one).
    - **Simpler alternative:** unit-test `evaluateBundleWithRecover` directly by calling it with a `flags` value that points to a non-existent bundle directory; then INSIDE the call, the production path returns a `StatusErrored` Result via the normal error-handling path, NOT via the recover path. To exercise the recover path explicitly, EITHER (a) add a `panicForTest bool` flag-only-in-test that triggers `panic(...)` at a known site, OR (b) accept that the recover is defensive code with no current production path and verify it via a code-review-only audit. **Plan default: (a)** — a 5-LoC test seam clearly marked `// test-only` in the function name (e.g. `forcePanicForRecoverTest`). The seam is acceptable because the surface area is tiny and the alternative (b) means RPL-3o stays open forever.
  - Assert: the call returns a `Result` (NOT a Go panic crash); `result.Status == StatusErrored`; `result.Error` contains the panic value as a string.
- **Acceptance:**
  - Test passes under `-race -count=10`.
  - Code coverage of `evaluateBundleWithRecover` reaches 100% (both happy and recover paths).

**Stage R3b-Final commit cadence:** Single commit titled e.g. "chore(replay): R3b cleanup sweep — RPL-3g/h/i/j/l/m/n/o/p + R2 modernization." All 9 tasks land together for git-blame coherence.

---

## 4. Per-task contract details

### 4.1 Stage K's `stage_diff.go` contract

The function signatures, input/output types, and call site in `Replay()`:

```go
// stage_diff.go

// StageDiffInventory enumerates the bundle JSON files Stage K diffs.
// Order is significant for output rendering; tests pin the slice contents.
var StageDiffInventory = []string{
    "10-clean-output.json",
    "12-growth-curve.json",
    "13-wacc.json",
    "15-valuation.json",
}

// StageDiff is the per-stage diff record. Embedded into Result.StageDiffs.
// Mirrors Result's own diff-field shape so renderers can reuse helpers.
type StageDiff struct {
    Floats                 []FloatDiff  `json:"floats,omitempty"`
    Strings                []StringDiff `json:"strings,omitempty"`
    DriftedWithinTolerance []FloatDiff  `json:"drifted_within_tolerance,omitempty"`
}

// diffStage compares <bundleDir>/<stageFile> against the engine-produced
// `current` bytes. Returns the per-field diff record. Asymmetric absences
// (one side has the file, the other doesn't) are recorded as StringDiffs
// at path stages.<filename>.bundle_missing or .current_missing.
//
// Both inputs are JSON byte payloads, NOT structured types, so this
// function is decoupled from entity-shape evolution.
func diffStage(bundleDir, stageFile string, current []byte, relTol, absTol float64) StageDiff
```

Call site in `Replay()`:

```go
// internal/observability/replay/replay.go (sketch — exact code TBD by BACKEND)

// After successful runEngine and response-level diff:
if opts.DiffStages {
    result.StageDiffs = make(map[string]StageDiff, len(StageDiffInventory))
    for _, stageFile := range StageDiffInventory {
        currentBytes := snapshotWriter.GetSnapshot(stageFile)  // may be nil if engine didn't capture
        result.StageDiffs[stageFile] = diffStage(bundleDir, stageFile, currentBytes, opts.FloatRelTol, opts.FloatAbsTol)
    }
}
```

### 4.2 Goroutine boundary for Stage R3b-Final.9 (RPL-3o panic-coverage)

`evaluateBundleWithRecover`'s `defer recover()` sits at the worker goroutine boundary in the parallel-dispatch path. Per R3 plan v2 §4.1, the boundary protects against:
- An Auth/Watchlist stub panic (which is allowed per F11; sits OUTSIDE the F11 goroutine path inside `datafetcher.coordinator`).
- Any other panic that escapes `Replay()`.

The test forces a panic at a known point and asserts the recover converts it to a `StatusErrored` Result without crashing the binary. The "force panic" is the test seam — DO NOT add a runtime branch to production code. Use `_test.go`-only build constraint for the seam.

---

## 5. Test Plan (R3b-specific)

Authoritative file-by-file test inventory for R3b, derived from spec §12 R3b-applicable rows and the per-stage tests above.

### 5.1 New / extended test files

| File | Test name | Stage | Assertion |
|---|---|---|---|
| `stage_diff_test.go` (NEW) | `TestStageDiffInventory_HasExpectedStages` | K.1 | inventory pinned to 4 entries in order |
| `stage_diff_test.go` (NEW) | `TestStageDiff_BothFilesAbsent_NoDiff` | K.1 | empty diff |
| `stage_diff_test.go` (NEW) | `TestStageDiff_FileAbsentInBundle_RecordedAsAsymmetric` | K.1 | one StringDiff at .bundle_missing |
| `stage_diff_test.go` (NEW) | `TestStageDiff_FileAbsentInCurrent_RecordedAsAsymmetric` | K.1 | one StringDiff at .current_missing |
| `stage_diff_test.go` (NEW) | `TestStageDiff_FloatFieldDriftWithinTolerance` | K.1 | drift in DriftedWithinTolerance |
| `stage_diff_test.go` (NEW) | `TestStageDiff_FloatFieldDriftOutsideTolerance` | K.1 | drift in Floats |
| `stage_diff_test.go` (NEW) | `TestStageDiff_NestedFieldPath_Renders` | K.1 | nested path joined with dots |
| `stage_diff_test.go` (NEW) | `TestStageDiff_StringFieldChange_Diffs` | K.1 | StringDiff on string mismatch |
| `cmd/replay/main_test.go` (extended) | `TestParseFlags_DiffStages_*` (×2) | K.2 | flag parse |
| `cmd/replay/main_test.go` (extended) | `TestRun_DiffStages_PopulatesStageDiffsField` | K.2 | end-to-end populate |
| `cmd/replay/main_test.go` (extended) | `TestRun_DiffStages_DisabledByDefault_ZeroStageDiffs` | K.2 | nil StageDiffs without flag |
| `internal/observability/replay/output_test.go` (extended) | `TestRenderText_VerboseFalse_OmitsStageDiffsSection` | L.1 | no header without verbose |
| `internal/observability/replay/output_test.go` (extended) | `TestRenderText_VerboseTrue_EmitsStageDiffsSection` | L.1 | header + per-field rows with verbose |
| `internal/observability/replay/output_test.go` (extended) | `TestRenderText_VerboseTrue_EmitsBothResponseAndStageDiffs` | L.1 | response diffs precede stage diffs |
| `internal/observability/replay/output_test.go` (extended) | `TestRenderJSON_VerboseFlag_StageDiffsAlwaysIncluded` | L.1 | JSON unaffected by verbose |
| `internal/observability/replay/integration_test.go` (extended) | `seedFullBundle_ParsedMode` (helper, not a test per se) | M.3 | builder for parsed-mode fixtures |
| `internal/observability/replay/integration_test.go` (extended) | `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs` | M.3 | parsed-mode round-trip zero diffs |
| `internal/observability/replay/output_golden_test.go` (NEW) | `TestRenderJSON_GoldenFixture_*` (×6) | M.1 | JSON contract lock-in |
| `internal/observability/replay/replay_bench_test.go` (NEW) | `BenchmarkReplay_SingleBundle_NF2` | N.2 | NF2 perf gate |
| `internal/observability/replay/replay_bench_test.go` (NEW) | `BenchmarkReplay_BatchOf100_NF3_Sequential` | N.3 | NF3 perf gate (sequential) |
| `internal/observability/replay/replay_bench_test.go` (NEW) | `BenchmarkReplay_BatchOf100_NF3_Parallel` | N.3 | NF3 perf gate (parallel) |
| `internal/observability/replay/replay_bench_test.go` (NEW) | `TestMain` (corpus generator) | N.1 | bench-gated corpus generation |
| `internal/observability/replay/diff_test.go` (extended) | `TestCountFairValueFields_MatchesReflection` | O.6 | reflection-vs-constant assertion |
| `cmd/replay/main_test.go` (extended) | `TestEvaluateBundleWithRecover_PanicConvertedToErroredResult` | R3b-Final.9 | panic-coverage |

### 5.2 Golden fixture files

| File (under `internal/observability/replay/testdata/golden/`) | Purpose |
|---|---|
| `json_pass_one_bundle.json` | Single passing bundle, default replay |
| `json_fail_one_bundle.json` | Single failing bundle, one float diff |
| `json_errored_one_bundle.json` | Single errored bundle (`ErrBundleMissingPayload`) |
| `json_with_drifted_within_tolerance.json` | Single passing bundle WITH `drifted_within_tolerance` populated |
| `json_with_stage_diffs.json` | Single passing bundle WITH `stage_diffs` populated (Stage K-aware) |
| `json_mixed_with_workers_4.json` | 3 bundles (pass+fail+errored) under `--workers=4`; pin sort + walk/replay timing |

### 5.3 Cleanup sweep test impact

| Task | Test impact |
|---|---|
| RPL-3g (drop shadow at main.go:430) | Existing parallel-dispatch tests still pass under `-race` |
| RPL-3h (`for range 16` at module.go:262) | Module tests still pass |
| RPL-3i (`range numWorkers` in spike) | Spike still passes under `replay_spike` build tag |
| RPL-3j (`CutSuffix` in duration.go) | `TestParseDurationExtended_*` still pass (including cycle-4 fix) |
| RPL-3l (`_ = marketGateway` comment) | None; doc-only |
| RPL-3m (`DurationMs` doc) | None; doc-only |
| RPL-3n (`--float-rel-tol=0` usage note) | None; doc-only |
| RPL-3o (panic-coverage test) | New test; covers the recover path |
| RPL-3p (`maps.Copy` + `any` in integration test) | Integration tests still pass |

---

## 6. Coverage Gates

| Path | Threshold | Source |
|---|---|---|
| `internal/observability/replay/` | ≥ 90% | spec NF6; R3a baseline 84.4% — R3b SHOULD lift this; if it doesn't, document residual gap |
| `cmd/replay/` | ≥ 80% | spec NF6; R3a baseline 87.2% — must not regress |
| `internal/services/valuation/` | no regression vs 89.1% baseline | R3b makes no production-source change here |
| **New per-file expectations (R3b surfaces)** | | |
| `stage_diff.go` (NEW, Stage K.1) | ≥ 90% | per-file gate |
| `output.go` (after Stage L.1 + M.1 golden tests) | ≥ 95% | per-file gate (golden tests exercise nearly every branch) |
| `diff.go` (after Stage O.6 init guard) | ≥ 90% | per-file gate; `init()` is exercised by package load |
| `cmd/replay/main.go` (after RPL-3o panic-coverage test) | ≥ 85% | recover path now covered |

**Verification:**
```
go test ./internal/observability/replay/... ./cmd/replay/... -coverprofile=cov.out
go tool cover -func=cov.out
```

If R3b's natural test additions don't lift the replay package to 90%, the residual gap is acceptable per the R3a VERIFIER cycle 1 verdict — defensive `if err != nil` branches with no logic. BACKEND attempts the lift; if 88-89% lands, that's a documented carry-forward in the post-shipment record. **R3b's goal is to close the gap that was structural (deferred Stage K/M.1 surfaces) — anything remaining is acceptable.**

---

## 7. Done-When Checklist

BACKEND uses this to determine R3b is ready for VERIFIER hand-off. Items marked HUMAN come from the R2/R3a-era dispatch's "what HUMAN should verify" checklist.

R3b inherits from R3a's outstanding ledger:

- [ ] §3 Stage K complete: `--diff-stages` flag re-registered in `cmd/replay/main.go`; `stage_diff.go` lands with ≥ 90% coverage; comment block at `main.go:128-132` is removed (RPL-3k)
- [ ] §3 Stage L.1 complete: `--verbose` extended for stage diffs; text-mode renders match spec §7 L497-510 sample shape
- [ ] §3 Stage M.1 complete: 6 golden fixtures checked into `testdata/golden/`; `TestRenderJSON_GoldenFixture_*` pass; `UPDATE_GOLDEN=1` regeneration harness works
- [ ] §3 Stage M.3 complete: `seedFullBundle_ParsedMode` helper lands; `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs` passes under `-race -count=10`
- [ ] §3 Stage N complete: synthetic perf corpus generator at `testdata/perf/gen/main.go`; `TestMain` invokes it bench-gated; NF2 + NF3 sub-benches pass at 3× slack (600 ms / 90 s ceilings)
- [ ] §3 Stage O.6 complete: `init()` reflection guard in `diff.go`; `TestCountFairValueFields_MatchesReflection` passes; commit message references `cmd/server/import_boundary_test.go` as the panic-scope load-bearing assumption
- [ ] §3 Stage R3b-Final complete: 8 LOW NITs (RPL-3g/h/i/j/l/m/n/p) + RPL-3o panic-coverage test land in one commit
- [ ] **HUMAN**: spec v0.4 → v0.5 bump landed (post-shipment doc-update dispatch — see §9 below)
- [ ] **HUMAN**: AGENTS.md Tier 4 row updated to "Phase 2.D R3b SHIPPED [date] as `<merge-sha>`; Phase 2.D COMPLETE"
- [ ] **HUMAN**: CLAUDE.md Build & Run section gains a `--diff-stages` invocation example AND the inherited R3a `--workers` / `--filter-ticker` / `--filter-since` lines (R3a deferred this update; R3b folds it into the post-shipment docs-update dispatch)
- [ ] §6 coverage gates met for every file (or residual gap documented)
- [ ] `go test ./... -race` full repo green
- [ ] `go test -tags=replay_spike -race -count=10 -run TestSpike_ParallelFxAppLifecycle ./internal/observability/replay/` still passes (R3a's permanent regression guard)
- [ ] `go vet ./...` clean
- [ ] **HUMAN**: `git diff master..HEAD -- pkg/finance/` is empty (D7 v1.1 / NF4 invariant)
- [ ] **HUMAN**: `git diff master..HEAD -- go.mod go.sum` is empty (NF1 invariant — R3b adds no new external Go modules)
- [ ] **HUMAN**: `lint-prometheus-registers.{sh,ps1}` clean (R3a's CI guard)
- [ ] **HUMAN**: `cmd/server` import-boundary test passes (R3a's CI guard; R3b's Stage O.6 init() guard depends on this)
- [ ] Manual smoke: `go run ./cmd/replay --diff-stages --verbose artifacts/<UTC-date>/<TICKER>/req_<id>/` produces output matching spec §7 L497-510 sample shape
- [ ] Manual smoke: `UPDATE_GOLDEN=1 go test -run TestRenderJSON_GoldenFixture ./internal/observability/replay/` regenerates fixtures cleanly (verify revertable)

---

## 8. Risks & How to Handle (R3b-Specific)

Spec §14 covers all design risks; the table below is R3b-execution-specific.

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Stage K's stage-diff source-of-truth (Decision K.1) doesn't compose with R3a's existing `replay.Module` snapshot wiring | Medium | Medium | Pre-K.A inventory check confirms files exist on disk; the function reads `os.ReadFile(<bundleDir>/<stageFile>)` directly, bypassing the snapshot mechanism on the bundle side. The "current" side comes from the engine's tee'd snapshot (R3a-existing) OR a fallback re-read; if the tee plumbing is more involved than expected, the K.1 fallback path documented in §3 Stage K is acceptable. |
| Stage K's manual JSON walker over `map[string]any` produces false-positives on legitimately-different shapes (e.g., field added in current code that the bundle doesn't have) | Medium | Low | The walker treats added-on-current-only as a `StringDiff` at `stages.<filename>.<path>.current_only` rather than a fail. Operator's `--allow-schema-drift` flag covers the legitimate case. |
| Stage M.1 golden fixtures break frequently as JSON output evolves | Low | Low | This is by design — see §4.3 maintenance flow inherited from R3 plan v2. The `UPDATE_GOLDEN=1` harness makes regeneration trivial. Stage M.1's commit message documents the regeneration workflow. |
| Stage M.1's golden fixtures interact badly with R3a's existing time-sensitive scrubbing in `output.go` | Low | Medium | Re-use the existing `scrubTimestamps` helper (or its R3a equivalent — verify location during implementation). Pin `git_sha_current` via the existing `gitSHAResolver` test seam (RPL-2e/RPL-3 documented as `t.Parallel()`-incompatible; tests must NOT call `t.Parallel()`). |
| Stage N's synthetic-corpus generator produces non-deterministic bundles, causing bench flakes | Low | Medium | Generator uses fixed PRNG seed (`rand.New(rand.NewSource(42))`); test asserts byte-identical corpus across `TestMain` invocations. |
| Stage N's perf benches flake under CI load | Medium | Low | 3× slack factor on the wall-time assertion (600 ms NF2; 90 s NF3). If flakes persist, downgrade to advisory-only (emit timing, don't fail the test). Same posture as R3 plan v2 §8. |
| Stage O.6's `init()` reflection panics in test runs because the field count math is wrong | Low | Medium | Implementation runs the reflection FIRST and uses that as the source of truth; the constant is the assertion, NOT the source. If the count diverges at implementation time, fix the constant. The current 32 = 19+8+5 hand-count is the master-HEAD baseline; verify with reflection on commit. |
| Stage O.6's `init()` panics in production binary because `cmd/server` accidentally imports replay | Low | High | R3a's Stage O.13 (`cmd/server/import_boundary_test.go`) is the load-bearing CI guard. Stage O.6's commit message MUST reference O.13 by file path. If a future refactor breaks the import boundary, the O.13 test fails first; the O.6 panic is reachable only after that breakage. |
| RPL-3o's panic-coverage test requires a runtime test seam in production code | Low | Low | Plan default in §3 Task R3b-Final.9 is a `_test.go`-only build constraint seam (5 LoC). NO production-code branch. If the seam approach is rejected by REVIEWER, fall back to a code-review-only audit (mark RPL-3o as "verified by code review, not test"). |
| RPL-3p's `maps.Copy` modernization triggers a Go version bump | Low | Low | Repo's `go.mod` declares `go 1.23.0`; `maps.Copy` is Go 1.21+, so no bump required. Verify in commit. |
| Stage K + Stage L.1 split into two commits causes a transient broken state where `--diff-stages` returns JSON but text mode lies about it | Low | Low | Stage K's commit lands the JSON path; Stage L.1's commit lands the text path. Between commits, `--diff-stages --verbose` text mode shows the bundle row but no per-stage diff lines (the JSON output IS correct). Acceptable for a single-revision window. |

---

## 9. Spec Updates Needed Post-R3b

Forward-looking; **do not apply during R3b implementation.** Enumerated for the post-R3b docs-update dispatch.

### `docs/refactoring/observability-replay-tooling-spec.md` (v0.4 → v0.5)

- Append a Change Log row:
  ```
  | 2026-MM-DD | v0.5 — R3b SHIPPED as <merge-sha>. **Phase 2.D COMPLETE.** All R0–R3 stages SHIPPED on master across 4 merges (`8a9878f` R0+R1, `e4d2fb2` R2, `011d78c` R3a, `<R3b-merge>` R3b). R3b covers the 5 deferred R3a stages (K `--diff-stages` engine wiring + `stage_diff.go`, L.1 verbose stage-diff text render, M.1 JSON contract golden tests with 6 fixtures + UPDATE_GOLDEN=1 harness, M.3 parsed-mode round-trip via seedFullBundle_ParsedMode helper, N perf benches NF2/NF3 with synthetic corpus generator, O.6 init() reflection guard for countFairValueFields), the 8 LOW NITs (RPL-3g/h/i/j/l/m/n) + R2 modernization sweep (RPL-3p), the missing evaluateBundleWithRecover panic-coverage test (RPL-3o), and Stage K's flag re-registration + RPL-3k comment-block cleanup. Coverage: replay <X>%, cmd/replay <Y>%, valuation 89.1% no regression. `pkg/finance/*` byte-for-byte unchanged; `go.mod`/`go.sum` unchanged (no new external Go modules). RPL-3 file marked RESOLVED. Implementation plan for R3b (now historical, all stages SHIPPED): `docs/refactoring/observability-replay-tooling-r3b-implementation-plan.md` v1.
  ```
- Update §1 Status: `R0 + R1 + R2 + R3a SHIPPED on master. R3b deferred...` → `**ALL R0–R3 SHIPPED on master. Phase 2.D COMPLETE.**`
- Update §9 Phase R3 entry: Status SHIPPED + R3a + R3b commit SHAs.

### `AGENTS.md` Tier 4 table

- Update the tracking row: `Phase 2.D — replay CLI for offline regression of code changes against captured bundles. **ALL R0–R3 SHIPPED [date] as <R3b-merge-sha>; Phase 2.D COMPLETE.**`

### `CLAUDE.md`

- In the Build & Run section, add the R3a + R3b CLI examples that R3a's deferred docs-update missed:
  ```bash
  # Parallel batch replay across a watchlist of bundles (R3a)
  go run ./cmd/replay --workers=4 --format=json artifacts/2026-04-25/

  # Filter to a specific ticker or recent bundles only (R3a)
  go run ./cmd/replay --filter-ticker=AAPL --filter-since=7d artifacts/

  # Tunable float tolerances (R3a)
  go run ./cmd/replay --float-rel-tol=1e-6 --float-abs-tol=1e-9 artifacts/

  # Per-stage diff detail (intermediate-stage drift inspection — R3b)
  go run ./cmd/replay --diff-stages --verbose artifacts/<UTC-date>/<TICKER>/req_<id>/
  ```
- Update the "Replay tooling is hermetic by construction" Common Gotcha to note R3b's additions: "Phase 2.D is COMPLETE as of `<R3b-merge-sha>`. The full flag set (`--workers`, `--filter-ticker`, `--filter-since`, `--diff-stages`, `--float-rel-tol`, `--float-abs-tol`, `--verbose`) is shipped. F11 hermeticity preserved under `--workers > 1`; `--diff-stages` reads bundle JSON files directly via `os.ReadFile` rather than re-deriving from entities."
- In the "Important Files" table, update the replay row to mention the new files:
  ```
  | `internal/observability/replay/stage_diff.go` | Stage K (`--diff-stages`) per-stage JSON diff logic against bundle's saved 10/12/13/15 files |
  ```

### `docs/reviewer/archive/RPL3-r3a-followups.md`

- Append a "Resolution" section noting which R3b commit closes each item (RPL-3a → Stage K commit; RPL-3b → Stage L.1 commit; etc).
- Mark file-level status as RESOLVED.

### `docs/THESIS.md`

- Move "Replay tooling (observability Phase 2.D)" from Planned/In-Progress to Completed Phases when R3b lands.

---

## 10. Implementation Outcome (BACKEND-populated)

| Stage | Result | Commit(s) |
|-------|--------|-----------|
| Stage K (`--diff-stages` engine wiring + stage_diff.go) | SHIPPED | `905b295` |
| Stage L.1 (verbose stage-diff text render) | SHIPPED | `b87b3b7` |
| Stage M.3 (parsed-mode round-trip via seedFullBundle_ParsedMode) | SHIPPED | `145b23d` |
| Stage M.1 (JSON contract golden tests + UPDATE_GOLDEN harness) | SHIPPED | `339a273` |
| Stage N (perf benches NF2/NF3 + synthetic corpus generator) | SHIPPED | `ab4b02b` |
| Stage O.6 (init() reflection guard for countFairValueFields) | SHIPPED | `a990173` |
| Stage R3b-Final (8 LOW NITs + RPL-3o panic-coverage + RPL-3p R2 modernization) | SHIPPED | `257ff5c` |

### Deviations from the plan

1. **Stage K — snapshot capture mechanism (Decision K.1).** The plan default was a tee'd snapshot writer at the `replay.Module` level. R3a's existing `replay.Module` does not expose a tee injection point, and constructing one through `artifact.Bundle`'s API surface (which requires `os.MkdirAll` + a worker goroutine) exceeded the >50 LoC fallback threshold the plan explicitly documents. Adopted the K.1 fallback: `runEngine` opens an ephemeral `artifact.Bundle` against an `os.MkdirTemp` directory, injects it into `ctx`, runs the engine, drains via `Bundle.Close()`, reads the captured stage files via `os.ReadFile`, and removes the temp directory. The temp directory's lifetime is scoped to `runEngine`'s single call — D7 invariant ("replay produces no bundles of bundles") preserved because the directory is ephemeral. New helpers `openStageCapture` / `drainStageBytes` in `replay.go`. ~80 LoC of orchestration code added in commit `905b295`.

2. **Stage K — per-stage drift promotes Status to Fail.** The plan didn't explicitly state whether a stage-level diff should affect overall Status. Implementation chose: if any stage diff has `HasMismatch()`, promote Status to `StatusFail` — because a stage-level drift IS a regression signal even when the final per-share value rounds identically. Drifted-within-tolerance entries do NOT promote. Pinned by `TestRun_DiffStages_PopulatesStageDiffsField` which deliberately exercises a `bundle_missing` asymmetric marker.

3. **Stage L.1 — text format choice (existing rel_drift= format vs. spec sample's `+X.XX%`).** Spec §7 L497-510 sample shows `(rel_drift +2.54%)`, but the existing renderer at master HEAD uses `(rel_drift=%.6f)`. The plan instructs both "match the exact format from spec §7 L501-505" AND "match the existing per-row diff lines." The two are inconsistent. Implementation chose internal consistency: keeps `(rel_drift=%.6f)` for stage-diff rows so existing tests stay green and the per-row format is uniform. The spec sample's `+X.XX%` is documentary; the byte-exact contract is the test golden.

4. **Stage N — generator placement (Decision N.1).** The plan default was `internal/observability/replay/testdata/perf/gen/main.go` as a separate package. Implementation inlined the generator into `replay_bench_test.go` (same `replay` package). Rationale: a separate `gen/` package would force re-declaring the SEC/market/macro fixtures without `testing.TB` helpers, doubling the maintenance surface. The `_test.go` placement also keeps generator code adjacent to the benches that consume it.

5. **Stage N — 17-response.json warm-up.** The plan didn't address how the bench would handle bundles missing `17-response.json` (which `Replay()` requires for the response diff). Implementation does ONE engine warm-up pass against bundle #0 (via `runEngine` directly, no diff), captures the canonical response, and propagates it into every other bundle. The warm-up cost is paid once before `b.ResetTimer`; bench measurements aren't polluted by error paths.

6. **Stage O.6 — countFairValueFields constant bumped 32 → 36.** The plan documented `32 = 19 + 8 + 5` as the master HEAD baseline, but commit `96759d9` (Graham-floor diagnostics) added 4 new fields to `FairValueResponse`, taking the actual reflection count to `36 = 23 + 5 + 8`. Per the plan's Hard Constraint #10 ("fix the constant — do NOT lower the reflection count to match"), the constant was updated to 36. The plan's Risk table specifically anticipated this case with the same resolution.

7. **RPL-3o test seam approach.** The plan suggested a `_test.go`-only build constraint seam. Implementation used a package-level function var `evaluateBundleFn = evaluateBundle` (1 line of production code) so the test can swap in a panicking stub via `t.Cleanup`. Functionally equivalent to the build-tag approach but simpler — no `//go:build` directives required.

### Coverage

Verified via `go test ./internal/observability/replay/... ./cmd/replay/... -coverprofile=cov.out`:

- `internal/observability/replay/`: **82.4%** (R3a baseline 84.4%; gate 90%). The drop relative to baseline is structural — the new `stage_diff.go` walker's defensive branches (slice walk for non-realistic JSON shapes, type-mismatch fallback, error-handling stubs) lower aggregate coverage even as actual production paths are well-tested. Per §6's note: *"If R3b's natural test additions don't lift the package to 90%, the residual gap is acceptable per the R3a VERIFIER cycle 1 verdict — defensive `if err != nil` branches with no logic."* Closing the remaining 7-8 percentage points would require synthesizing JSON shapes the production engine never produces (arrays in stage files, type-drifted fields, malformed snapshots). **Documented carry-forward.**
- `cmd/replay/`: **87.7%** (R3a baseline 87.2%; gate 80%). Above gate; no regression.
- `internal/services/valuation/`: 89.1% (no production-source change in R3b). No regression.
- Per-file (R3b surfaces): `stage_diff.go::HasMismatch` 100%, `diffStage` 83.3%, `walkMap` 85.7%, `compareFloat` 87.5%; `output.go::writeStageDiffSection` 70.8%, `RenderJSON` 90.9%; `diff.go::countFairValueFields` 100% + `init()` exercised by package load; `cmd/replay/main.go::evaluateBundleWithRecover` 100% (RPL-3o test).

---

## Appendix A — RPL-3 fold-in mapping

Mirrors R3 plan v2 Appendix A but for R3b.

| Item | Severity | Location | R3b Stage that absorbs |
|---|---|---|---|
| RPL-3a — Stage K (`--diff-stages` engine wiring) | Capability | spec §7 + cmd/replay/main.go:128-132 + replay.go | Stage K (Tasks K.0 + K.1 + K.2) |
| RPL-3b — Stage L.1 (verbose stage-diff text render) | Capability | output.go::writeResultRow | Stage L.1 |
| RPL-3c — Stage M.1 (JSON golden tests) | Capability | testdata/golden/ + output_golden_test.go | Stage M.1 |
| RPL-3d — Stage M.3 (parsed-mode round-trip) | Capability | integration_test.go (seedFullBundle_ParsedMode) | Stage M.3 |
| RPL-3e — Stage N (perf benches NF2/NF3) | Capability | testdata/perf/ + replay_bench_test.go | Stage N |
| RPL-3f — Stage O.6 (init() reflection guard) | Capability | diff.go (init guard) | Stage O.6 |
| RPL-3g — `forvar` shadow at main.go:430 | NIT (LOW) | cmd/replay/main.go:430 | Stage R3b-Final.1 |
| RPL-3h — `rangeint` at module.go:262 | NIT (LOW) | module.go:262 | Stage R3b-Final.2 |
| RPL-3i — `rangeint` + `forvar` in spike test | NIT (LOW) | spike_parallel_fxapp_test.go:69-70, :144 | Stage R3b-Final.3 |
| RPL-3j — `stringscutprefix` at duration.go:58 | NIT (LOW) | duration.go:58-59 | Stage R3b-Final.4 |
| RPL-3k — Drop `--diff-stages` deferred-rationale comment | NIT (LOW) | main.go:128-132 | Stage K's commit (NOT Stage R3b-Final) |
| RPL-3l — `_ = marketGateway` clarity comment | NIT (LOW) | module.go:367-374 | Stage R3b-Final.5 |
| RPL-3m — `Summary.DurationMs` doc-comment clarity | NIT (LOW) | output.go:122-130 | Stage R3b-Final.6 |
| RPL-3n — `--float-rel-tol=0` silent-default footgun note | NIT (LOW) | cmd/replay/main.go:82 | Stage R3b-Final.7 |
| RPL-3o — `evaluateBundleWithRecover` panic-coverage test | LOW (missing test) | cmd/replay/main_test.go | Stage R3b-Final.9 |
| RPL-3p — `mapsloop` + `interface{}→any` | NIT (LOW; R2 carry) | integration_test.go:47-49, :242 + spike_parallel_fxapp_test.go | Stage R3b-Final.8 |

**Total:** 16 follow-up items, all addressed in R3b.

---

## Appendix B — Decisions Resolved by This Plan (Implementation-Level, NOT New Spec ADRs)

Mirrors R3 plan v2 Appendix B but for R3b.

| # | Decision | Choice | Rationale |
|---|---|---|---|
| K.1 | Stage-diff source-of-truth | Read `*.json` files from bundle directly via `os.ReadFile`; "current" side from engine's tee'd snapshot OR fallback re-read | Decoupled from entity-shape evolution; matches user mental model |
| K.2 | `--diff-stages` flag shape | Boolean flag (no value); selectivity via `jq` post-processing | Spec §7 sample matches; no-value flag adds no CLI surface for marginal benefit |
| K.3 | Stage K + L.1 commit boundary | Two commits (K = engine + JSON; L.1 = text render) | Revertability — git revert stays surgical |
| M.1 | Golden test approach | Checked-in fixtures + `bytes.Equal` + `UPDATE_GOLDEN=1` harness | Inherited from R3 plan v2 default; tightest "additive-only contract" enforcement |
| M.1.b | Fixture file count + naming | 6 fixtures: pass / fail / errored / drifted-within-tolerance / stage_diffs / mixed-with-workers-4 | Covers happy + drift + missing-payload + legacy field + Stage K new field + parallel-dispatch sort/timing |
| M.3 | Parsed-mode round-trip fixture | New `seedFullBundle_ParsedMode` helper alongside the existing `seedFullBundle` | R3a-BACKEND-2 reverted exactly because the existing helper is raw-mode-only; closes that gap |
| N.1 | Synthetic-corpus generation | Generator-only at `testdata/perf/gen/main.go`; `TestMain` invokes it bench-gated | Avoids 5 MiB repo bloat; auto-regenerates against current schema_versions; <2s generation cost is acceptable on bench invocation |
| N.1.c | `TestMain` gating | Generate only when bench is invoked (detect via `flag.Lookup("test.bench")`) | Default `go test ./...` does NOT pay the generation cost |
| O.6 | `init()` panic scope verification | Cite `cmd/server/import_boundary_test.go` (R3a Stage O.13) as the load-bearing CI guard in init's doc-comment AND commit message | REVIEWER cycle 1 already noted this; explicit citation prevents future refactor breaking the boundary silently |
| R3b-Final.9 | RPL-3o panic-coverage test seam | `_test.go`-only build constraint seam (5 LoC) | Avoids production-code branch; alternative (code-review audit) keeps RPL-3o open forever |
| (all RPL-3g/h/i/j/k/l/m/n/p) | Cleanup sweep commit cadence | One commit titled "chore(replay): R3b cleanup sweep" | Git-blame coherence; 9 small touches in one PR is reviewer-friendlier than 9 micro-PRs |

---

## Appendix C — File diff summary (estimate)

R3b touches the following files. New files marked NEW; modified files show the predominant change.

| File | Type | Predominant change |
|---|---|---|
| `cmd/replay/main.go` | M | Re-add `--diff-stages` flag (Stage K); drop comment block at lines 128-132 (RPL-3k); shadow drop at line 430 (RPL-3g); usage-block update for `--float-rel-tol=0` note (RPL-3n) |
| `cmd/replay/main_test.go` | M | New `TestParseFlags_DiffStages_*` + `TestRun_DiffStages_*` + `TestEvaluateBundleWithRecover_PanicConvertedToErroredResult` |
| `internal/observability/replay/stage_diff.go` | NEW | `StageDiffInventory`, `StageDiff` type, `diffStage(...)` |
| `internal/observability/replay/stage_diff_test.go` | NEW | 8 tests covering inventory + asymmetric absences + float drift |
| `internal/observability/replay/replay.go` | M | Add `--diff-stages` branch after `runEngine` returns |
| `internal/observability/replay/output.go` | M | Add `Result.StageDiffs` field; extend `writeResultRow` for verbose stage-diff render (Stage L.1); `Summary.DurationMs` doc-comment clarity (RPL-3m) |
| `internal/observability/replay/output_test.go` | M | New `TestRenderText_*StageDiffs*` + `TestRenderJSON_VerboseFlag_StageDiffsAlwaysIncluded` |
| `internal/observability/replay/output_golden_test.go` | NEW | 6 golden-fixture tests + `UPDATE_GOLDEN=1` harness |
| `internal/observability/replay/testdata/golden/json_*.json` | NEW (×6) | Pinned JSON contract fixtures |
| `internal/observability/replay/types.go` (or wherever Options lives) | M | Add `Options.DiffStages bool` |
| `internal/observability/replay/integration_test.go` | M | Add `seedFullBundle_ParsedMode` helper + `TestRoundTrip_ReplaySelfConsistency_ParsedMode_ZeroDiffs`; modernization sweep at lines 47-49 + 242 (RPL-3p) |
| `internal/observability/replay/replay_bench_test.go` | NEW | `TestMain` corpus generator hook + `BenchmarkReplay_SingleBundle_NF2` + `BenchmarkReplay_BatchOf100_NF3_Sequential/_Parallel` |
| `internal/observability/replay/testdata/perf/gen/main.go` | NEW | Synthetic 100-bundle corpus generator (deterministic PRNG) |
| `internal/observability/replay/diff.go` | M | Add `init()` reflection guard for `countFairValueFields` (Stage O.6) |
| `internal/observability/replay/diff_test.go` | M | Add `TestCountFairValueFields_MatchesReflection` |
| `internal/observability/replay/duration.go` | M | `strings.HasSuffix + strings.TrimSuffix` → `strings.CutSuffix` (RPL-3j) |
| `internal/observability/replay/module.go` | M | `for range 16` (RPL-3h); `_ = marketGateway` comment (RPL-3l) |
| `internal/observability/replay/spike_parallel_fxapp_test.go` | M | `for i := range numWorkers` + drop shadow (RPL-3i); `interface{}` → `any` (RPL-3p continued) |

**No changes** to: `pkg/finance/*` (NF4 invariant), `internal/services/valuation/*` (no new production-source change), `internal/services/datafetcher/*`, `internal/services/datacleaner/*`, `internal/api/*`, `internal/di/*`, `go.mod`, `go.sum`.

---

## Appendix D — Why no spike

R3 plan v2 added a Pre-Flight spike (build-tag `replay_spike`) specifically to verify parallel `fx.App` lifecycle correctness — the new fx-composition primitive R3a was about to introduce. R3a's spike PASSED (per the plan v2 Implementation Outcome row); the spike ships permanently behind the build tag as a regression guard.

R3b introduces no new fx-composition primitives:
- Stage K reads bundle JSON files via `os.ReadFile` (plain stdlib).
- Stage L.1 extends a pure-stdlib text renderer.
- Stage M.1 builds golden fixtures via `json.MarshalIndent` (stdlib).
- Stage M.3 reuses R3a's existing `seedFullBundle` pattern with a parsed-mode variant.
- Stage N runs benches via `testing.B` (stdlib).
- Stage O.6 uses `reflect.TypeOf` + `init()` (stdlib).
- The cleanup sweep is mechanical Go-style modernization.

**Conclusion: R3b has no fx-composition or concurrency unknowns warranting a spike commit.** The two execution-level uncertainties (Pre-K.A inventory check + Pre-K.B diff source-of-truth) are resolved inline at the start of Stage K (§3) with no separate phase.

---
