# FEEDBACK-LOG.md — Agent Corrections & Preferences

Append-only log of **corrections and validated preferences** the user has given to AI agents. Items here should survive any single session — they represent how to work with this user on this project.

This file is distinct from `memory/MEMORY.md`:
- **MEMORY.md** = durable facts about the project and user (who, what, why)
- **FEEDBACK-LOG.md** = how-we-work rules learned through interaction (corrections and validated choices)

Items that recur often should be **promoted to `MEMORY.md`** during weekly curation.

---

## Format

Each entry should include:

```markdown
### YYYY-MM-DD — <short rule>

**Rule:** <the instruction itself, imperative form>

**Why:** <the reason the user gave, usually a past incident or strong preference>

**How to apply:** <when/where this guidance kicks in>

**Source:** <conversation where this was established, optional>
```

Lead with the rule. The **Why** lets future sessions judge edge cases. The **How to apply** specifies scope so the rule doesn't over-generalize.

---

## Active Rules

*(Empty. Entries will be appended here as corrections are given.)*

---

## Archive (Promoted to MEMORY.md or Obsolete)

*(Empty. Move items here when they are promoted to `memory/MEMORY.md` or are no longer relevant.)*

---

## Curation Rhythm

- **Per correction:** append immediately while context is fresh
- **Weekly:** review active rules; promote recurring ones to `MEMORY.md`; move promoted entries to Archive
- **Quarterly:** prune Archive entries older than 6 months that no longer apply

---

## Example Entries (Format Reference — Not Active Rules)

> The entries below are illustrative examples, not actual rules. Delete or ignore when the first real entry is added.

```markdown
### 2026-04-20 — Don't introduce backwards-compat shims

**Rule:** When removing code or renaming APIs, delete cleanly. Do not add `// removed` comments, rename-only `_var` stubs, or compatibility re-exports.

**Why:** User prefers tight diffs. Backwards-compat cruft hides the real change in code review.

**How to apply:** Applies to any refactor or cleanup inside `internal/`. Not applicable to public API changes where an announced deprecation window is needed.
```

```markdown
### 2026-04-21 — Single bundled PR preferred for refactors in internal/

**Rule:** For refactors touching multiple files in `internal/`, ship one bundled PR rather than a chain of small commits.

**Why:** User confirmed on 2026-04-21 that a 12-file bundled PR was the right call; splitting it would have been churn. Validated judgment, not a correction.

**How to apply:** Applies only to `internal/` refactors. Cross-package changes spanning `internal/` + `pkg/` + `cmd/` should still be split by package.
```

---

## Change Log

| Date | Change |
|------|--------|
| 2026-04-18 | Initial empty template. No active rules yet. |
