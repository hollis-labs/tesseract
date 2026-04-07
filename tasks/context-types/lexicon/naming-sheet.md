# Tiamat ecosystem naming sheet
Date: 2026-03-04

## Core apps (current)
| Name | Role | Notes |
|---|---|---|
| Project Tiamat (codename) | Unified agentic system | Umbrella integration + shared contracts |
| Fragments Engine | Capture + organization substrate | Atomic fragments; long-term store |
| Hadron | Deterministic executor | Pipelines, schedulers, tool execution |
| Cortex | Context registry / working memory | Deterministic context views + ownership boundaries |
| Volon | Orchestration layer | Plans, routes tasks to Hadron/tools |
| Nanite | Vault + review surface | Drafts, opportunities, tasks |
| Mentat | Cognitive partner agent | Operator-facing synthesis |
| Sigil | Frontend UI generator | Shared shell/components |

## Carrier (rename target for Content Ops)
Positioning: reclaim your data → distill signal → amplify your voice → accelerate output.

Suggested one-liner:
- **Carrier** — “Your signals distilled to amplify your voice.”

### Carrier subsystems (module breakdown)
| Subsystem | Purpose | Notes |
|---|---|---|
| Ingest | Collect signals into canonical artifacts | Adapter pattern; idempotent ledger |
| Abstract | Briefs, summaries, technical write-ups, white papers | Supporting documentation + context |
| Dispatch | Drafts for publication from prompts/templates | Iteration loop + provenance |
| Broadcast | Publish via sinks (MD now; later epub/pdf/doc/api/mcp) | Sink adapter pattern |

## Reserved / adjacent system names (as provided)
| Name | Intended slot | Notes |
|---|---|---|
| Aegis | Guardrails/security/sentinel | Policy enforcement, redaction, approvals |
| Atlas | Map your work | Common |
| Argus | Monitoring/telemetry | Observability + auditing |
| Mnemosyne | Deep memory brand | Strong; overlaps Cortex conceptually |
| Conduit | A2A messaging / flow wiring | Messaging fabric |

## Standalone chat app (candidate)
- **Intercom** — opt-in chat surface that connects to app agents/tools via config (MCP or equivalent).

Role summary:
- Unified conversational UI for Tiamat apps
- Routes user intent to Carrier/Cortex/Hadron/etc agents
- Provides a consistent operator experience across the ecosystem
