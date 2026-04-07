# Context Service – Pivot Update Task Pack

Date: 2026-02-24

This task pack updates the previously defined **User Context Service** work to reflect
a clarified and expanded scope:

- The service is a **general context registry + working-memory store**
- The *personal cache* is a **first-class namespace** (`user/*`)
- Any tool/agent may opt-in via CLI, HTTP API, or MCP
- Context retrieval is **context-aware via views**
- Storage remains: **SQLite index + file-backed payloads**
