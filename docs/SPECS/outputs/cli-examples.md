# CLI Pivot Examples

Status: draft reference note

This note curates pivot-aligned CLI examples. Canonical command contracts remain in [`docs/SPECS/CLI.md`](../CLI.md).

## Namespace ownership setup
```bash
tesseract context namespace register --namespace app/editor/session --owner-type app --owner-id editor
tesseract context namespace register --namespace user/profile --owner-type user --owner-id chris
```

## App write flow
```bash
tesseract context put \
  --client-id editor-ui \
  --actor app:editor-ui \
  --namespace app/editor/session \
  --key goal \
  --json '{"text":"ship deterministic specs"}'
```

## Deterministic view selector flow
```bash
tesseract context view \
  --selector '{"namespaces":["app/editor/*","user/*"],"keys":["goal","summary"],"revision_scope":"head","order":["namespace","key","revision"]}' \
  --limit 50 \
  --include-payload \
  --output json
```

## User promotion flow into `user/*`
```bash
tesseract context promote \
  --client-id user-shell \
  --actor user \
  --from-namespace app/editor/session \
  --from-key summary \
  --to-namespace user/profile \
  --to-key summary
```

## Inspect promoted head
```bash
tesseract context get --namespace user/profile --key summary --output json
```

## Canonical references
- CLI spec: [`docs/SPECS/CLI.md`](../CLI.md)
- API spec: [`docs/SPECS/API.md`](../API.md)
- Views spec: [`docs/SPECS/VIEWS.md`](../VIEWS.md)
