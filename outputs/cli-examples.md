# CLI Examples (Task 5)

## Register app namespace
```bash
contextd context namespace register \
  --namespace app/editor/session \
  --owner-type app \
  --owner-id editor
```

## Write context
```bash
contextd context put \
  --client-id editor \
  --actor app:editor \
  --namespace app/editor/session \
  --key active_file \
  --json '{"path":"/tmp/a.txt"}'
```

## Evaluate view selector
```bash
contextd context view \
  --selector '{"namespaces":["app/editor/*","user/goals"],"order":["namespace","key","revision"]}' \
  --include-payload
```

## Promote into protected user namespace
```bash
contextd context promote \
  --client-id editor \
  --actor user \
  --from-namespace app/editor/session \
  --from-key summary \
  --to-namespace user/notes \
  --to-key project_summary
```
