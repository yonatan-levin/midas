# RPL-11 — No current (CalcVersion-4.10) replay baseline corpus → live replay verification runs against confounded fixtures

**Status:** OPEN — operator/capture follow-up (no code change required). Surfaced 2026-06-29 during RPL-9 replay-side consumer live verification.
**Severity:** LOW–MEDIUM (verification debt) — no production symptom. The replay tooling itself is correct and fully tested; the gap is the absence of a clean fixture to *exercise* it end-to-end against today's engine.
**Origin:** RPL-9 (`#26`) live test. Pointed at the only on-disk 1.2 bundles that carry a `00-config.json` (`artifacts/tier2-baseline/2026-06-20/…`), the replay run hit a pre-existing schema-drift gate and (under `--allow-schema-drift`) reported drifted fields — all attributable to engine evolution between the baseline capture and current `master`, NOT to the change under test.

## The problem

The replay corpus under `artifacts/tier2-baseline/` is stale relative to the engine:

- `artifacts/tier2-baseline/2026-05-19/` is `calculation_version 4.1` (pre Phases 2/3/4 + pre-profile-config) — already documented as confounded in CLAUDE.md.
- `artifacts/tier2-baseline/2026-06-20/` carries `AssumptionProfileManifest` / `GuidanceResolution` schema versions **newer** than older code and engine output that predates the VAL-1 reconciliation; replaying it against current `master` trips the schema-drift gate and shows non-zero field drift.
- The engine is now at **`CalculationVersion 4.10`** (VAL-1 reconciliation, post VAL-3 AFFO).

Net effect: there is **no clean, CalcVersion-4.10-current bundle corpus** to run `cmd/replay` against and expect a zero-diff. Any drift seen in a live replay today is confounded across multiple shipped phases, so a live replay cannot currently *confirm* a specific change's fidelity — it can only confirm the binary executes end-to-end (which RPL-9's live test did).

This also blocks the related operator confirmations that need a current baseline:
- RPL-9 override-path: confirm a 1.2 bundle captured under a **non-default** production config replays zero-diff *because* the bundle's `00-config.json` overrides the hand-mirror (the unit golden test `TestReplayConfig_BundleSnapshotOverridesHandMirror` proves the overlay; an end-to-end zero-diff needs a non-default-config 4.10 bundle).
- DC-1 Phase 5: confirm the DDM `DebtLikeClaims` EV correction on a B-rule-firing bank against a current baseline (CLAUDE.md notes this operator follow-up; the 4.1 baseline confounds it).

## Durable fix (capture, not code)

1. Cold-start the stack per `docs/RUNBOOK.md` against current `master` (CalcVersion 4.10).
2. Capture a fresh bundle corpus for the standard 10-ticker basket (the `tier2-baseline` ticker set: AAPL, AMD, EQIX, F, JPM, … — match the existing dir layout) into `artifacts/tier2-baseline/<YYYY-MM-DD>/`. Confirm each bundle is manifest version **1.2** and carries a `00-config.json` (RPL-9 capture side).
3. Capture **at least one bundle under a deliberately non-default production config** (e.g. `DCFMaxGrowthRate` ≠ 0.50) so the RPL-9 override path can be verified end-to-end: replaying it must reproduce the original `17-response.json` **because** the bundle's `00-config.json` drives `replayConfig`, whereas the hand-mirror alone would drift.
4. Run `go run ./cmd/replay --workers=4 --format=json artifacts/tier2-baseline/<new-date>/` and confirm zero-diff (no `--allow-schema-drift` needed).
5. Update CLAUDE.md's `artifacts/tier2-baseline/` references to point at the new current baseline; note the old dirs as historical/confounded.

## Acceptance criteria

- [ ] A `CalculationVersion 4.10` bundle corpus exists under `artifacts/tier2-baseline/<new-date>/`, all manifest v1.2 with `00-config.json`.
- [ ] `cmd/replay` produces zero-diff against the new corpus on current `master` **without** `--allow-schema-drift`.
- [ ] ≥1 bundle captured under a non-default valuation config; replaying it is zero-diff (RPL-9 override path confirmed end-to-end) and would drift if the snapshot were ignored.
- [ ] DC-1 Phase 5 DDM `DebtLikeClaims` EV correction confirmed on a B-rule-firing bank against the new baseline.
- [ ] CLAUDE.md baseline references updated to the current corpus.

## Out of scope

- Any replay/engine code change — the replay tooling (RPL-1…RPL-10) and the RPL-9 consumer are complete and unit-tested. This is purely a fixture-capture/operability task requiring a live stack run.

## Traceability

- Parent: RPL-9 (`docs/reviewer/archive/RPL9-bundle-manifest-config-snapshot.md`, GitHub `#26`) — its override path is unit-proven; this tracker covers the end-to-end fixture.
- Related: the DC-1 Phase 5 operator follow-up (fresh CalcVersion-4.4→now-4.10 baseline + DDM DebtLikeClaims live confirmation) referenced in `midas/CLAUDE.md`.
- GitHub issue: #30
