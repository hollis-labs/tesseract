# workflows/user/

This directory holds **user-authored workflows** — reusable, parameterizable
sequences of steps that solve arbitrary user problems.

User workflows use the same format as volon workflows but are managed by you,
not by the Volon core team.

## Creating a workflow

Until `/workflow-create` is available, create workflows manually:

1. Copy the template from `docs/08_workflow-authoring.md`
2. Save as `workflows/user/<name>.md`
3. Set `domain: user`, `status: draft`
4. Fill in steps and invariants
5. Validate manually; set `status: active` when ready

## Listing workflows

```
/backlog-task list        # list backlog items
ls workflows/user/        # list user workflow definitions
```

## Example use cases

- `app-investigation` — understand an unfamiliar codebase (see EPIC-003)
- `weekly-review` — personal retrospective workflow
- `release-prep` — project-specific release checklist

See `docs/08_workflow-authoring.md` for the full authoring guide.
