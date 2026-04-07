# Repo Layout and Rationale

Date: 2026-02-25

## Layout (relevant paths)

.
├── docs/
│   ├── ARCHITECTURE.md
│   ├── SCOPE.md
│   ├── DEV.md
│   ├── DECISIONS/
│   │   ├── README.md
│   │   └── ADR-INDEX.md
│   └── SPECS/
│       └── README.md
├── artifacts/
│   └── plan/
├── outputs/
│   ├── repo-layout.md
│   ├── bootstrap-steps.md
│   └── open-questions.md
└── .agentrc/
    ├── boot/
    ├── pcc/
    ├── tasks/
    ├── logs/
    └── bootstrap.md

## Rationale
- `docs/` contains human-authored canonical project docs.
- `outputs/` contains deliverables produced for task review.
- `artifacts/` contains planning/support artifacts used by agent workflows.
- `.agentrc/` contains Volon runtime state and orchestration metadata.
- Existing Volon platform docs are preserved; context-service docs are introduced as focused stubs.
