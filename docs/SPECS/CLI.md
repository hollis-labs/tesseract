# CLI Spec

Status: pivot-aligned draft (Task 5)

## Binary
`contextd` (placeholder name)

## Global flags
- `--client-id <id>`: required for write/promote commands.
- `--output json|table` (default `json`).

## Commands

### `context namespace register`
Register namespace ownership policy.

Flags:
- `--namespace <value>`
- `--owner-type user|app`
- `--owner-id <value>`
- `--policy-file <path>`

### `context put`
Append a revision.

Flags:
- `--client-id <id>`
- `--actor user|app:<id>|system`
- `--namespace <value>`
- `--key <value>`
- `--json <payload-json>` or `--file <path>`

### `context get`
Get head revision.

Flags:
- `--namespace <value>`
- `--key <value>`

### `context history`
Read revision history.

Flags:
- `--namespace <value>`
- `--key <value>`
- `--limit <n>`
- `--cursor <value>`

### `context view`
Evaluate deterministic selector.

Flags:
- `--selector <json>` or `--selector-file <path>`
- `--include-payload`
- `--limit <n>`

### `context promote`
Promote record into protected `user/*`.

Flags:
- `--client-id <id>`
- `--actor user`
- `--from-namespace <value>`
- `--from-key <value>`
- `--to-namespace <user/ns>`
- `--to-key <value>`
- `--source-revision <n>` (optional)

## Deterministic CLI behavior
- Default output order follows API deterministic ordering guarantees.
- CLI commands perform no hidden retries that alter semantic ordering.
- Read/view commands are side-effect free.

## Example flows

### Multi-client setup + write + view
1. Register app-owned namespace:
   - `contextd context namespace register --namespace app/editor/session --owner-type app --owner-id editor`
2. Register user-owned namespace:
   - `contextd context namespace register --namespace user/profile --owner-type user --owner-id chris`
3. Write app context record:
   - `contextd context put --client-id editor-ui --actor app:editor-ui --namespace app/editor/session --key goal --json '{\"text\":\"ship deterministic specs\"}'`
4. Evaluate deterministic selector view:
   - `contextd context view --selector '{\"namespaces\":[\"app/editor/*\",\"user/*\"],\"keys\":[\"goal\",\"summary\"],\"revision_scope\":\"head\",\"order\":[\"namespace\",\"key\",\"revision\"]}' --limit 50 --include-payload --output json`

### Promotion into protected `user/*`
1. App writes candidate summary in app namespace:
   - `contextd context put --client-id editor-ui --actor app:editor-ui --namespace app/editor/session --key summary --json '{\"text\":\"candidate summary\"}'`
2. User promotes approved content into `user/*`:
   - `contextd context promote --client-id user-shell --actor user --from-namespace app/editor/session --from-key summary --to-namespace user/profile --to-key summary`
3. Read promoted head:
   - `contextd context get --namespace user/profile --key summary --output json`

Consistency notes:
- `context view` examples must use selector ordering compatible with `docs/SPECS/VIEWS.md`.
- Promote flow requires `--actor user` and `--to-namespace user/*` to match API policy semantics.
- Actor/namespace policy matrix: see `docs/SPECS/API.md#actor-namespace-contract-matrix`.
