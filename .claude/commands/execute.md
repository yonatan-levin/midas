### Mode: EXECUTE

**Goal:** Implement a well-defined solution using existing specs.


```
┌─────────────────────────────────────────────────────────────────┐
│                      IMPLEMENTATION PHASE                       │
├─────────────────────────────────────────────────────────────────┤
│  1. BACKEND / FRONTEND                                          │
│     ├── Read existing specs                                     │
│     ├── Implement focused changes                               │
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
│         ▼                              ▼                        │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │              BACKEND / FRONTEND                          │   │
│  │  ├── Fix issues from REVIEWER/QA report                  │   │
│  │  └── HANDOFF_TO: REVIEWER                                │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---