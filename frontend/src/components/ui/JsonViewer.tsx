import { JsonViewer as SysopJsonViewer } from "@hollis-labs/sysop-ui";

interface Props {
  data: unknown;
  maxHeight?: string;
}

export function JsonViewer({ data, maxHeight }: Props) {
  return (
    <div style={{ maxHeight, overflow: maxHeight ? "auto" : undefined }}>
      <SysopJsonViewer value={normalizeJson(data)} />
    </div>
  );
}

function normalizeJson(data: unknown): unknown {
  if (typeof data === "string") {
    try {
      return JSON.parse(data);
    } catch {
      return data;
    }
  }
  return data ?? null;
}
