# Open Questions (Post-Pivot)

1. Namespace registry format: static config file vs runtime registration endpoint.
2. Promotion approval path into `user/*`: synchronous confirm vs staged pending queue.
3. MCP surface for MVP: read-only first, or full write parity with CLI/API.
4. Selector complexity limit for MVP views (to keep deterministic performance predictable).
5. Required audit retention period for append-only history before compaction strategy is introduced.
