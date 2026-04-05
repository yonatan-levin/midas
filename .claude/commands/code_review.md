### Mode: CODE_REVIEW

**Goal:** Review existing changes before commit/PR (no new implementation).


```
┌─────────────────────────────────────────────────────────────────┐
│                        REVIEW PHASE                             │
├─────────────────────────────────────────────────────────────────┤
│  1. REVIEWER                                                    │
│     ├── Read the diff/patch                                     │
│     ├── Check against specs and coding standards                │
│     ├── Evaluate: correctness, readability, security, perf      │
│     └── Output: APPROVE | APPROVE_WITH_NITS | REJECT            │
│                                                                 │
│  If REJECT → HANDOFF_TO: BACKEND/FRONTEND (fix issues)          │
│  If APPROVE → HANDOFF_TO: QA (for complex) or HUMAN (simple)    │
├─────────────────────────────────────────────────────────────────┤
│  2. QA (if needed for complex changes)                          │
│     ├── Verify behavior against requirements                    │
│     ├── Check test coverage                                     │
│     └── Output: PASS | FAIL                                     │
│                                                                 │
│  If FAIL → HANDOFF_TO: BACKEND/FRONTEND                         │
│  If PASS → HANDOFF_TO: HUMAN                                    │
└─────────────────────────────────────────────────────────────────┘
```