---
intent: system_doc
audience: humans
---

# Sub-agents — v0.1 (updated for Orchestrator Mode)

## Policy
Sub-agents are **optional** and **bounded**. They exist to parallelize *analysis*, not to mutate state.

## Single-writer rule
Only the Orchestrator may write/update:
- tasks/backlog
- logs
- PCC
- bootstrap/history

Sub-agents are read-only.

## Configuration
See `docs/01_config.md` under `agents.subagents`.

## Model caps
Sub-agents are capped at the tier configured in `models.agent_caps`:
- `worker`: `claude-haiku-4-5-20251001` (default) — bounded analysis, fast and cheap
- `reviewer`: `claude-sonnet-4-6` (default) — scan accuracy matters

The orchestrator may not dispatch a sub-agent at a higher tier than its cap.
See `docs/12_model-config.md` for the full model selection hierarchy.

## Request template (from Orchestrator)
```markdown
Objective: <one sentence>

Inputs:
- Paths: <explicit list>
- Context: <minimal, only what is needed>

Constraints:
- READ ONLY: do not modify files
- Do not update tasks/logs/PCC/bootstrap
- Do not spawn sub-agents
- If commands are allowed, list exactly which ones you will run

Output format:
- <explicit format: bullets/table/json>
```

## Response rules (sub-agent)
- Return only what was asked for
- Include evidence pointers (file paths / command outputs)
- No recommendations beyond the requested scope unless explicitly allowed
