### Mode: DEBUG

**Goal:** Investigate and fix bugs, failing tests, production errors, or user-reported issues.

**Trigger:** When the user describes:
- A failing test
- A bug ticket or report
- A production error
- Explicitly asks to "debug"

```
┌─────────────────────────────────────────────────────────────────┐
│                         TRIAGE PHASE                            │
├─────────────────────────────────────────────────────────────────┤
│  1. QA (DEBUG mode)                                             │
│     ├── Triage severity (CRITICAL | HIGH | MEDIUM | LOW)        │
│     ├── Define reproduction steps                               │
│     ├── Analyze error logs, stack traces, test output           │
│     ├── Identify most likely responsible area:                  │
│     │   └── BACKEND | FRONTEND | UX | DATA | UNKNOWN            │
│     └── HANDOFF_TO: BACKEND/FRONTEND (with area + findings)     │
│                                                                 │
│  QA DEBUG Output:                                               │
│  - `# Severity` - CRITICAL/HIGH/MEDIUM/LOW                      │
│  - `# Reproduction Steps` - How to trigger the bug              │
│  - `# Root Cause Analysis` - Initial hypothesis                 │
│  - `# Affected Area` - BACKEND/FRONTEND/UX/DATA/UNKNOWN         │
│  - `# Evidence` - Logs, stack traces, test failures             │
├─────────────────────────────────────────────────────────────────┤
│                          FIX PHASE                              │
├─────────────────────────────────────────────────────────────────┤
│  2. BACKEND / FRONTEND (DEBUG mode)                             │
│     ├── Receive QA's triage report and evidence                 │
│     ├── Investigate root cause deeper                           │
│     ├── Implement fix                                           │
│     ├── Add regression test for the bug                         │
│     └── HANDOFF_TO: QA (for verification)                       │
├─────────────────────────────────────────────────────────────────┤
│                      VERIFICATION CYCLE                         │
│         (max 2-3 iterations before escalating to HUMAN)         │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────┐    FIX VERIFIED    ┌──────────────┐          │
│  │  QA (DEBUG)  │ ──────────────────▶│   REVIEWER   │──▶ HUMAN │
│  │  (verify)    │                    │  (optional)  │          │
│  └──────────────┘                    └──────────────┘          │
│         │                                                       │
│         │ FIX FAILED / NEW ISSUES                               │
│         │ (bug not resolved or regression found)                │
│         ▼                                                       │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │              BACKEND / FRONTEND (DEBUG)                  │   │
│  │  ├── Receive failed verification report                  │   │
│  │  ├── Re-analyze with new evidence                        │   │
│  │  ├── Implement revised fix                               │   │
│  │  └── HANDOFF_TO: QA (restart verification)               │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Severity Guidelines:**
| Severity | Definition | Response |
|----------|------------|----------|
| CRITICAL | Production down, data loss risk, security breach | Immediate fix, skip optional steps |
| HIGH | Major feature broken, affecting many users | Priority fix within cycle |
| MEDIUM | Feature degraded, workaround exists | Fix in current session |
| LOW | Minor issue, cosmetic, edge case | Queue for later or fix if quick |

---