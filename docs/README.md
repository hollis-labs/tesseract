# Tesseract Docs

This directory currently contains both public product docs and older internal planning material from before the public beta push.

If you are evaluating or installing Tesseract, start here:

- [../README.md](../README.md) for the repo overview and fastest install path
- [QUICKSTART.md](QUICKSTART.md) for first run, provider setup, and first record flow
- [AGENT-SETUP.md](AGENT-SETUP.md) for MCP and Claude Code setup
- [guides/tesseract-adoption-and-v0.9-migration.md](guides/tesseract-adoption-and-v0.9-migration.md) for the supported Go/MCP contract and v0.9 migration checklist
- [ARCHITECTURE.md](ARCHITECTURE.md) for the core model and invariants
- [SPECS/API.md](SPECS/API.md) for the HTTP surface
- [SPECS/CLI.md](SPECS/CLI.md) for CLI behavior
- [MCP_TOOLS.md](MCP_TOOLS.md) for the MCP tool catalog
- [RELEASE.md](RELEASE.md) for maintainer release procedure

## Current public-doc set

The intended public beta documentation set is:

- repo README
- this docs index
- quickstart
- agent setup
- v0.9 adoption and migration guide
- architecture
- API / CLI / MCP specs
- changelog

## Historical and maintainer-only material

A significant portion of this tree still reflects older internal workflows, naming, and planning history. Files such as the `docs/0*.md`, `docs/1*.md`, `docs/tesseract-*`, `docs/superpowers/`, and `docs/outputs/` paths should be treated as historical unless a current public doc links to them directly.

That cleanup is in progress. For the active beta-prep checklist, see [BETA_RELEASE_CHECKLIST.md](BETA_RELEASE_CHECKLIST.md).
