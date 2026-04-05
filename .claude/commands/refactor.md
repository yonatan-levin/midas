### Mode: REFACTOR

**Goal:** Improve structure and maintainability while preserving behavior.


```
┌─────────────────────────────────────────────────────────────────┐
│                         PLANNING PHASE                          │
├─────────────────────────────────────────────────────────────────┤
│  1. ARCH (recommended for larger refactors)                     │
│     ├── Analyze current design pain points                      │
│     ├── Define behaviors that MUST remain unchanged             │
│     ├── Produce refactor plan with safety constraints           │
│     └── HANDOFF_TO: BACKEND / FRONTEND                          │
│                                                                 │
│  ARCH Output:                                                   │
│  - `# Current State` - Existing architecture                    │
│  - `# Problems` - Pain points identified                        │
│  - `# Refactor Plan` - Step-by-step approach                    │
│  - `# Safety Constraints` - What must NOT change                │
├─────────────────────────────────────────────────────────────────┤
│                      IMPLEMENTATION PHASE                       │
├─────────────────────────────────────────────────────────────────┤
│  2. BACKEND / FRONTEND                                          │
│     ├── Follow refactor plan mechanically                       │
│     ├── Keep changes small and reviewable                       │
│     ├── Update tests to match structural changes                │
│     └── HANDOFF_TO: REVIEWER                                    │
├─────────────────────────────────────────────────────────────────┤
│                       VALIDATION CYCLE                          │
│         (max 2-3 iterations before escalating to HUMAN)         │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────┐    APPROVE    ┌──────────────┐    PASS       │
│  │   REVIEWER   │ ───────────── │      QA      │ ──────── HUMAN│
│  │ (structure)  │               │ (regression) │                │
│  └──────────────┘               └──────────────┘                │
│         │                              │                        │
│         │ REJECT                       │ FAIL                   │
│         ▼                              ▼                        │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │              BACKEND / FRONTEND                          │   │
│  │  ├── Fix structural issues or regressions                │   │
│  │  └── HANDOFF_TO: REVIEWER                                │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---