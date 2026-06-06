# TDB-10 — Residual sub-TODOs in XBRL matcher & flag evaluator

**Status:** OPEN — filed 2026-06-06 (TODO-catalog burn-down pass).
**Priority:** P4 — Tier 4 (polish within already-"complete" systems).
**Type:** Enhancement.
**Mirrored as GitHub issue:** `[TDB-10]` (yonatan-levin/midas).
**Origin:** 2026-06-06 burn-down — in-code residue found while grepping TODOs. These live inside HIGH-priority systems the catalog already marks COMPLETE (XBRL tag matching, flag conditions), so they are sub-feature polish, not formal catalog items.

---

## Context — residual sub-TODOs

**`internal/services/datacleaner/xbrl_matcher.go`:**
- `:276` add date/duration validation
- `:378` implement regex pattern matching
- `:388` implement consistency checks (e.g. assets = liabilities + equity)

**`internal/services/datacleaner/flag_evaluator.go`:**
- `:384` implement date-condition evaluation
- `:487` implement different log levels
- `:495` implement alert mechanism (email, webhook, etc.)
- `:502` implement data-transformation logic

## Scope

Each sub-TODO is independently small. The right disposition for several may be **explicit de-scope** (e.g. the flag-evaluator alert mechanism may be redundant with the existing alerting service) rather than implementation. Treat this as a triage-then-act ticket.

## Acceptance
- [ ] Each sub-TODO is either implemented or explicitly de-scoped with an inline note
- [ ] Tests cover any newly-implemented behavior
- [ ] No remaining bare `TODO` comments without a tracking reference in these two files
