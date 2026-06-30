# RPL-11 — Replay `CurrentSchemaVersions` missing AssumptionProfileManifest + GuidanceResolution

**Status:** RESOLVED — fixed 2026-06-29 on `fix/rpl8-ddm-cleaner-snapshot` (commit `445bd3a`). GitHub issue: #28.
**Severity:** MEDIUM — replay/observability ergonomics; `--allow-schema-drift` workaround existed. No valuation impact.
**Origin:** Discovered during the RPL-8 (#25) live test (2026-06-29).

## Symptom

A freshly-captured bundle (bank OR non-bank) refused `cmd/replay --from=parsed` with exit 2:

```
ERROR: schema drift detected (use --allow-schema-drift to proceed)
  - schema:AssumptionProfileManifest  bundle=1 current=0 (unknown to current code)
  - schema:GuidanceResolution         bundle=1 current=0 (unknown to current code)
```

`FinancialData` was NOT in the drift list (the RPL-8 fix was working). The control AAPL bundle failed
identically, isolating the cause as unrelated to RPL-8/banks — it affected EVERY fresh bundle.

## Root cause

`internal/observability/replay/schema.go::CurrentSchemaVersions` (the hand-maintained map replay
compares a bundle's `manifest.schema_versions` against) registered 7 entities but OMITTED two that the
artifact bundle now stamps:
- `AssumptionProfileManifest` (v1) — `internal/observability/artifact/bundle.go::SetAssumptionProfileManifest` (Tier-2 assumption profiles, `08-assumption-profile.json`).
- `GuidanceResolution` (v1) — `bundle.go::SetGuidanceResolution` (Layer-B Phase 2 guidance, `09-guidance.json`).

Both producers were added AFTER the registry's "Phase R1" snapshot and the map was never updated.
`CompareSchemaVersions` flags any bundle-stamped entity absent from the map as `MissingFromCurrent`
("unknown to current code") drift → refusal without `--allow-schema-drift`. The pre-existing static pin
`TestCurrentSchemaVersions_HasAllKnownProducers` was a hand-maintained list that itself omitted the two
producers, so it never caught the gap.

## Fix (commit `445bd3a`)

- `internal/observability/replay/schema.go` — added `"AssumptionProfileManifest": 1` +
  `"GuidanceResolution": 1` to `CurrentSchemaVersions` (v1 = exactly what `bundle.go` stamps); refreshed
  the producer-list comment.
- `internal/observability/replay/testdata/happy/00-manifest.json` — added both stamps so the fixture
  represents a CURRENT bundle (keeps `cmd/replay/main_test.go::TestRun_HappyBundle` drift-free).
- `internal/observability/replay/testdata/schema-drift/00-manifest.json` — added both stamps so the
  fixture's ONLY drift remains the deliberate `FinancialData:999` (keeps `TestRun_SchemaDriftRefused` crisp).
- `internal/observability/replay/schema_test.go` — extended the static `HasAllKnownProducers` list, AND
  added `TestCurrentSchemaVersions_RegistersEveryStampedEntity`: a hermetic `artifact.OpenBundle`
  round-trip that drives the real `SetAssumptionProfileManifest` + `SetGuidanceResolution` +
  `AddSchemaVersion("FinancialData", …)` producers, reads the manifest back off disk, and asserts every
  stamped key is registered — so a future stamped-but-unregistered entity fails CI (teeth-proven: removing
  either map entry fails both the static and dynamic guards).

No production behavior change beyond the registry map. `go build ./...` exit 0; all observability + cmd/replay
packages green; RPL-8 + DDM bit-for-bit invariants unaffected.

## Acceptance criteria — ALL MET

- [x] `CurrentSchemaVersions` registers `AssumptionProfileManifest: 1` + `GuidanceResolution: 1`.
- [x] Replay fixtures updated atomically; replay package suite stays green.
- [x] Guard test asserts every entity a fresh bundle stamps is registered (dynamic round-trip, teeth-proven).
- [x] Fresh-bundle `cmd/replay --from=parsed` succeeds WITHOUT `--allow-schema-drift` (live-confirmed on
      fresh JPM — only the benign `as_of` clock field differs).

## Traceability

- GitHub issue: #28 (title corrected RPL-10 → RPL-11; `RPL10` was already taken by an archived tracker).
- Branch: `fix/rpl8-ddm-cleaner-snapshot`, commit `445bd3a`.
- Parent: RPL-8 (#25) — this gap blocked RPL-8's acceptance criterion #3; fixed in the same branch.
- Files: `internal/observability/replay/schema.go`, `schema_test.go`,
  `testdata/{happy,schema-drift}/00-manifest.json`.
