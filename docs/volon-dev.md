# Volon-dev Repository Guide

`volon-dev` is the private development workspace for Volon. Use this repo to iterate on
code, docs, plugins, and automation before exporting clean releases to
[`hollis-labs/volon`](https://github.com/hollis-labs/volon).

## Origin remote plan

- Current remote: `https://github.com/hollis-labs/forge.git` (pre-rebrand)
- Target remote (after repo rename): `git@github.com:hollis-labs/volon-dev.git`

Once GitHub staff (or the org admin) renames the repo:

```bash
git remote set-url origin git@github.com:hollis-labs/volon-dev.git
git remote -v   # verify
```

Update badges/docs accordingly. The public release repo (`volon`) remains separate.

## Migrating legacy clones

If your copy still has `forge.yaml` and `.forge/`, run:

```
scripts/migrate-to-volon.sh
```

This renames config/state to the Volon layout before you continue work.

## Documentation navigation pointers

- Specs index: [`docs/SPECS/README.md`](./SPECS/README.md)
- Pivot/output artifacts index: [`docs/outputs/README.md`](./outputs/README.md)
- Canonical architecture scope:
  - [`docs/ARCHITECTURE.md`](./ARCHITECTURE.md)
  - [`docs/SCOPE.md`](./SCOPE.md)
