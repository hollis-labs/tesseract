# View Examples

## Example 1: User dashboard context
Selector:
```json
{
  "namespaces": ["user/goals", "user/preferences"],
  "revision_scope": "head",
  "order": ["namespace", "key", "revision"]
}
```
Use case: fetch canonical user priorities and preferences in stable order.

## Example 2: App session + user alignment
Selector:
```json
{
  "namespaces": ["app/editor/session", "user/goals"],
  "keys": ["active_file", "focus_goal"],
  "revision_scope": "head",
  "order": ["namespace", "key", "revision"]
}
```
Use case: contextualize current app activity against user goals.

## Example 3: Audit slice
Selector:
```json
{
  "namespaces": ["app/editor/*"],
  "tags_any": ["session"],
  "revision_scope": "all",
  "order": ["namespace", "key", "revision"],
  "limit": 50
}
```
Use case: deterministic recent audit window for one app namespace family.
