# Volon — Agent Boot Instructions

This repository is managed by **Volon**, an agentic development system.

## Boot Sequence

Before any other work:

1. List the available profiles in `.agentrc/boot/` (e.g. `orchestrator`, `architect`, `worker`, `reviewer`)
2. Ask the user which profile to use — present the options clearly, plus "Other: Specify"
3. Wait for the user's selection before proceeding
4. Read `.agentrc/agent-boot.md` (core rules and ground truth sources)
5. Read `.agentrc/boot/<selected-profile>.md`
6. Read `.agentrc/bootstrap.md` (current iteration state)
7. Follow the selected profile's instructions, emit the boot confirmation block, and begin work

## Key Files

| File | Purpose |
|---|---|
| `agentrc.yaml` | System configuration |
| `.agentrc/agent-boot.md` | Core rules, ground truth sources, role definitions |
| `.agentrc/boot/` | Role-specific boot profiles |
| `.agentrc/bootstrap.md` | Current iteration state — start here |
| `.agentrc/pcc/` | Project Context Cache |

## Profiles

| Profile | Use when |
|---|---|
| `orchestrator` | Driving implementation, managing tasks, writing to the repo |
| `architect` | Planning, ADRs, doc-only work — limited write scope |
| `worker` | Bounded read-only analysis (scans, audits, reports) |
| `reviewer` | Structured reviews, verification, targeted investigation |
