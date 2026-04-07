---
intent: system_doc
audience: humans
---

# Model Configuration — v0.1

## Purpose
Volon assigns model tiers to use-cases so that the right model is used for each job.
Cheap/fast models handle reads and scans; capable models handle planning and authoring.
This reduces token cost and latency without sacrificing quality where it matters.

## Use-case taxonomy

| Tier | Use-case type | Examples | Default model |
|---|---|---|---|
| `read_scan` | Read files, list tasks, bootstrap read | pcc-refresh read pass, task-list, bootstrap read | `claude-haiku-4-5-20251001` |
| `summarize` | Classify, extract, scan for patterns | quality-run modes, decision log extract | `claude-haiku-4-5-20251001` |
| `generate` | Author artifacts, write skill/doc content | workflow stages (ideation, PRD, spec) | `claude-sonnet-4-6` |
| `plan` | Architectural breakdown, sprint planning | stage-6-plan, investigation workflows | `claude-sonnet-4-6` |
| `orchestrate` | Orchestrator loop, task selection, verification | orchestrator session | `claude-sonnet-4-6` |
| `complex_reasoning` | Multi-step analysis, ADR authoring, proposals | multi-agent concurrency proposal | `claude-opus-4-6` |

## Hierarchy of overrides

Resolution order (lowest to highest precedence):

```
global default (models.default)
  └── use-case tier (models.overrides.<tier>)
        └── workflow override (models.workflows.<workflow-name>)
              └── task frontmatter (model: claude-sonnet-4-6)
                    └── user CLI flag (--model) ← highest precedence
```

Each level inherits from the level above unless explicitly set.

## agentrc.yaml schema

```yaml
models:
  default: claude-sonnet-4-6
  fallback: claude-haiku-4-5-20251001
  overrides:
    read_scan: claude-haiku-4-5-20251001
    summarize: claude-haiku-4-5-20251001
    generate: claude-sonnet-4-6
    plan: claude-sonnet-4-6
    orchestrate: claude-sonnet-4-6
    complex_reasoning: claude-opus-4-6
  workflows:
    quality_run: claude-sonnet-4-6       # accuracy over cost for scans
    pcc_refresh: claude-haiku-4-5-20251001
  agent_caps:
    worker: claude-haiku-4-5-20251001    # workers do bounded analysis only
    reviewer: claude-sonnet-4-6
  large_context_threshold_tokens: 50000
  large_context_action: warn             # warn | downgrade | block
```

## SKILL.md annotation

Skills declare their model tier in frontmatter:

```yaml
model-tier: read_scan
```

The orchestrator reads this at dispatch time and resolves the model via the override hierarchy.
If `model-tier` is absent, the `models.default` is used.

## Fallback model

- If the primary model is rate-limited or returns an API error, fall back to `models.fallback`.
- Fallback must not silently upgrade (Haiku fallback → Opus is forbidden).
- Fallback events are logged in the run log: `[fallback] used claude-haiku-4-5-20251001 (primary: claude-sonnet-4-6, reason: rate_limit)`

## Special rules

### Large-context guard
If the estimated token count of a prompt exceeds `large_context_threshold_tokens`:
- `warn` (default): log a warning and continue with the configured model
- `downgrade`: switch to the fallback model automatically
- `block`: halt and require user confirmation before proceeding

### Quality scan exception
`quality_run` overrides `models.overrides.summarize` with `models.workflows.quality_run`.
Scan accuracy takes priority over cost for security and correctness checks.

### Sub-agent caps
Workers and reviewers are capped at their configured tier (`agent_caps`).
The orchestrator may use a higher-tier model for planning, but caps apply when dispatching sub-agents.

### Dry-run mode
Model selection applies in dry-run. Results are not committed, but the model choice
is recorded in the run log — useful for cost estimation before a full run.

## Enforcement note

In v0.1, model selection is advisory: agents read the config and honor the tier.
There is no runtime enforcement layer. True enforcement (e.g. a wrapper that
intercepts API calls) is out of scope for v0.1 and may be addressed in a future epic.

## References

- Config schema: `docs/01_config.md`
- Orchestrator dispatch: `docs/08_orchestrator.md`
- Sub-agent caps: `docs/07_subagents.md`
- Current Anthropic model IDs: claude-opus-4-6, claude-sonnet-4-6, claude-haiku-4-5-20251001
