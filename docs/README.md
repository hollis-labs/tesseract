# Tesseract documentation

These documents describe the current public-preview implementation. When a
reference disagrees with the running binary, `tesseract --help`, the registered
HTTP route table, and MCP tool schemas are authoritative; please report the
documentation drift.

## Start here

- [Repository overview](../README.md)
- [Quick start](QUICKSTART.md)
- [Agent and MCP setup](AGENT-SETUP.md)
- [Project integration guidance](CONTEXT-FOR-PROJECTS.md)
- [Operations, support, backup, and upgrades](OPERATIONS.md)
- [Security policy](../SECURITY.md)

## Concepts and contracts

- [Architecture](ARCHITECTURE.md)
- [Vector search](vector-search.md)
- [HTTP API](SPECS/API.md)
- [CLI](SPECS/CLI.md)
- [MCP](SPECS/MCP.md)
- [MCP tool catalog](MCP_TOOLS.md)
- [Namespace rules](SPECS/NAMESPACES.md)
- [Type vocabularies](OPERATIONS.md#type-vocabularies-typesyaml)
- [Promotion](SPECS/PROMOTION.md)
- [Storage](SPECS/STORAGE.md)
- [Views](SPECS/VIEWS.md)

## Maintainers and contributors

- [The knowledge/memory boundary](knowledge-memory-boundary.md) — the rule, and the corpus test behind it
- [Handoff and boot prompt](handoff-and-boot-prompt.md) — the two authored packets: where they live, and what each is not
- [Agent-facing prose audit](agent-facing-prose-audit.md)
- [Development notes](DEV.md)
- [Release procedure](RELEASE.md)
- [Contributing](../CONTRIBUTING.md)
- [Changelog](../CHANGELOG.md)
- [Architecture decisions](DECISIONS/README.md)

## Historical material

Planning snapshots and version-specific migration notes are retained for
provenance, not as current product contracts. They are cataloged in
[HISTORICAL.md](HISTORICAL.md) and are deliberately not part of the onboarding
path above.
