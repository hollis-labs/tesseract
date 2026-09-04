import { Pill, type PillTone } from "@hollis-labs/sysop-ui";

type Variant = "primary" | "ok" | "warn" | "danger" | "muted";

const VARIANT_MAP: Record<string, Variant> = {
  success: "ok",
  ready: "ok",
  applied: "ok",
  approved: "primary",
  pending: "warn",
  running: "warn",
  degraded: "warn",
  failed: "danger",
  error: "danger",
  revoked: "danger",
  expired: "muted",
  unknown: "muted",
};

interface Props {
  status: string;
  variant?: Variant;
}

export function StatusBadge({ status, variant }: Props) {
  const v = variant ?? VARIANT_MAP[status.toLowerCase()] ?? "muted";
  const tones: Record<Variant, PillTone> = {
    primary: "info",
    ok: "success",
    warn: "warning",
    danger: "danger",
    muted: "neutral",
  };
  return <Pill tone={tones[v]}>{status}</Pill>;
}
