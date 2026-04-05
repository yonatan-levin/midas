### Mode: PLAN_AND_CREATE

**Goal:** Turn a high-level idea into a clear plan, then implement with full review.


```
┌─────────────────────────────────────────────────────────────────┐
│                         PLANNING PHASE                          │
├─────────────────────────────────────────────────────────────────┤
│  1. ARCH                                                        │
│     ├── Clarify requirements and constraints                    │
│     ├── Produce spec-level plan (goals, architecture, tasks)    │
│     ├── Update ARCHITECTURE.md, CONTRACTS.md if needed          │
│     └── HANDOFF_TO: UX_UI (if UI) or BACKEND/FRONTEND           │
│                                                                 │
│  2. UX_UI (if applicable)                                       │
│     ├── Define user flows, screens, component hierarchy         │
│     ├── Update UX_SPEC.md                                       │
│     └── HANDOFF_TO: FRONTEND or BACKEND                         │
├─────────────────────────────────────────────────────────────────┤
│                      IMPLEMENTATION PHASE                       │
├─────────────────────────────────────────────────────────────────┤
│  3. BACKEND / FRONTEND                                          │
│     ├── Implement based on ARCH plan                            │
│     ├── Write/update tests                                      │
│     └── HANDOFF_TO: REVIEWER                                    │
├─────────────────────────────────────────────────────────────────┤
│                       VALIDATION CYCLE                          │
│         (max 2-3 iterations before escalating to HUMAN)         │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────┐    APPROVE    ┌──────────────┐    PASS       │
│  │   REVIEWER   │ ───────────── │      QA      │ ──────── HUMAN│
│  └──────────────┘               └──────────────┘                │
│         │                              │                        │
│         │ REJECT                       │ FAIL                   │
│         │ (issues found)               │ (bugs/regressions)     │
│         ▼                              ▼                        │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │              BACKEND / FRONTEND                          │   │
│  │  ├── Receive issue report from REVIEWER or QA            │   │
│  │  ├── Fix the identified issues/bugs                      │   │
│  │  ├── Update tests if needed                              │   │
│  │  └── HANDOFF_TO: REVIEWER (restart cycle)                │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**ARCH Output:**
- `# Summary` - Goals and non-goals
- `# Requirements` - Functional and non-functional
- `# Architecture` - Decisions and trade-offs
- `# API Contracts` - Requests/responses, status codes
- `# Tasks by Agent` - Breakdown for each role
- `# Spec Updates` - Proposed changes to docs

---
