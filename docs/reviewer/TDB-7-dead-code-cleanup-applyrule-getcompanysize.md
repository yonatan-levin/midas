# TDB-7 — Delete dead code: applyRule chain, getCompanySize, orphaned IntegrationService

**Status:** OPEN — filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P3 — Tier 3 (cleanup; **zero behavior change, lowest risk — the cleanest quick win**).
**Type:** Enhancement / tech-debt.
**Mirrored as GitHub issue:** `[TDB-7]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down (residue **R3**). Consolidates dead code proven unreachable during the Financial-Extraction and Company-Size investigations, plus the orphan from the performance-handler deletion.

---

## Context — confirmed-dead targets

| Target | Location | Evidence it is dead |
|---|---|---|
| `applyRule` + `apply{Exclusion,Writedown,Reclassify,TreatAsDebt,Flag}Rule` chain (~335 lines) | `datacleaner/service.go:712-1047` | `s.applyRule(` has **zero callers**; all tagged `nolint:unused`. Subsumes the old "Generic Rule Implementation x2" catalog TODO (`:794,:867` live inside this chain). |
| `getCompanySize` + `CleaningContext.CompanySize` | `datacleaner/service.go:1160`, `:164` | Producer-only — stamped once, read by **zero** production consumers. |
| `company_size` flag rule | `config/datacleaner/flag_conditions.json:517` | `set_field` output token has zero `internal/` Go references. |
| `profile.Facts.MarketCap` | `valuation/profile/facts.go:47` | Never populated (`valuation/service.go:972` leaves nil) and never read by the resolver. |
| `alerting.IntegrationService` | `internal/services/alerting/integration_service.go` | Only external consumer was the performance handler, deleted 2026-06-06; now self-referenced only. |

## Why it matters

Pure maintainability: ~400+ lines of misleading dead code (estimate logic that looks live, orphaned types). No functional impact — which is exactly why it is low risk and a good warm-up before the harder TDB-1/TDB-2 work.

## Scope / Tasks

| ID | Task | Effort |
|---|---|---|
| TDB-7.1 | Delete the `applyRule` chain (`service.go:712-1047`) + `nolint:unused` tags | S |
| TDB-7.2 | Delete `getCompanySize`, `CleaningContext.CompanySize`, the `company_size` flag rule | S |
| TDB-7.3 | Delete unpopulated `profile.Facts.MarketCap` (+ nil set site) | XS |
| TDB-7.4 | Delete orphaned `alerting.IntegrationService` (verify no remaining refs first) | S |

## Acceptance
- [ ] All listed dead code removed
- [ ] `go build ./...` + `go vet ./...` + `go test ./... -count=1` green
- [ ] No load-bearing invariant regressions (DDM bit-for-bit, shadow snapshots, ledger basket)
