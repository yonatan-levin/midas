# RPL-7 — `--from=raw` errors on every production bundle (per-FRED-series files not snapshotted)

**Status:** OPEN — filed 2026-05-14 by QA cycle 1 of the replay-fidelity debug.
**Severity:** MAJOR (UX) — `--from=raw` is documented as the default mode but currently fails on every real production bundle.
**Origin:** Discovered during debug Phase 3c QA re-verify (cycle 1) while verifying the cycle-1 capture-side fix.

## Symptom

```powershell
./replay --from=raw artifacts/2026-05-14/MXL/req_*/
# ERROR: failed to fetch macro data: no macro data:
# replay: bundle missing required payload file
# (hint: this bundle may only have parsed snapshots; try --from=parsed)
```

The hint added in commit `2d623c9` (replay UX polish, RPL-4b dispatch) tells the operator the right workaround, but the underlying issue remains: `--from=raw` is the documented default mode and it fails for every production-captured bundle.

## Root cause

The replay-side macro gateway at `internal/observability/replay/gateway_macro.go:99-135` (ModeRaw) walks `07-fetch-macro-<seriesID>.raw.json` files (one per FRED series like `DGS10`, `T10Y2Y`, etc.).

The production capture path at `internal/infra/gateways/macro/gateway.go:115-132` only snapshots the AGGREGATED `07-fetch-macro.parsed.json` — NOT per-series raw files.

So the bundle never contains what the replay raw-mode gateway expects.

## Why the misleading JSON summary

In raw mode against a real bundle, the JSON output shows `summary.fields_changed: 0, errored: 1`. At first glance the `fields_changed: 0` reads as success. The `errored: 1` is the actual signal but it's easy to miss. (See MINOR-1 in QA's cycle 1 report.)

## Fix options

| Option | Approach | Pros | Cons |
|--------|----------|------|------|
| **A** | Production captures per-FRED-series raw files alongside the aggregated parsed | True raw-mode replay works | Adds N files per bundle (where N is the FRED-series count, typically 3-7) |
| **B** | Drop raw-mode support for macro; route raw-mode to parsed payload when per-series files don't exist, with a WARN | Simpler, no capture-side change needed | Loses semantic distinction between raw + parsed for macro specifically |
| **C** | Make the JSON summary's `fields_changed` field `-1` or omit it when status is errored, so CI scripts don't false-positive | Cosmetic UX fix; doesn't address the underlying capture gap | Patches the symptom not the cause |

**Recommended**: Option B (simpler, lower-risk) for the immediate fix, plus C for the UX hardening. Option A is the architecturally cleaner long-term fix but adds bundle-bloat for negligible operator benefit.

## Acceptance criteria

- [ ] `./replay --from=raw <production-bundle>` runs without erroring for any current production bundle.
- [ ] Test pinning the raw-mode-fallback-to-parsed behavior added.
- [ ] JSON summary's errored status is unambiguous from `fields_changed` (Option C).

## Traceability

- Discovered by: QA cycle 1 of the replay-fidelity debug (2026-05-14)
- Related commits: `2d623c9` (added the `--from=parsed` hint in `annotateMissingPayloadHint`), `dbce508` (the merge that closed the parent debug cycle)
- File:line references:
  - `internal/observability/replay/gateway_macro.go:99-135` (raw-mode walker)
  - `internal/infra/gateways/macro/gateway.go:115-132` (production capture path)
