# Suggested repo structure (draft)

.
├── README.md
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
│   └── README.md
└── .forge/
    ├── boot/
    │   ├── agent-boot.md
    │   └── architect-boot.md
    ├── pcc/
    │   ├── user/
    │   │   └── README.md
    │   └── app/
    │       └── README.md
    └── tasks/
        └── README.md

Notes:
- `artifacts/` is for userland repo outputs that are safe to commit.
- `.forge/` contains Forge state, boot prompts, PCC skeleton, and tasks scaffolding.
