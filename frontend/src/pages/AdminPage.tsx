import {
  Button,
  Callout,
  CopyableId,
  cn,
  EmptyState,
  Pill,
  SettingsField,
  SettingsGrid,
  SettingsPanel,
  SummaryCards,
} from "@hollis-labs/sysop-ui";
import { type ColumnDef, DataTable } from "@hollis-labs/sysop-ui/data";
import { ListPageLayout, TabStrip, type TabStripItem } from "@hollis-labs/sysop-ui/layout";
import {
  AlertTriangle,
  Archive,
  Check,
  Copy,
  Database,
  FileSliders,
  HardDrive,
  HeartPulse,
  Key,
  MapIcon,
  Plus,
  RefreshCw,
  Scissors,
  ScrollText,
  Search,
  ServerCog,
  Settings,
  ShieldCheck,
  Tags,
  Trash2,
  Wrench,
} from "lucide-react";
import {
  type ComponentProps,
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { toast } from "sonner";
import {
  applyAdminSettings,
  backfillAdminQueue,
  cleanupExpiredTTL,
  compactRecords,
  createAdminConfigBackup,
  createToken,
  getAdminConfigBackups,
  getAdminNamespaceHistory,
  getAdminQueue,
  getAdminQueueFailures,
  getAdminSettings,
  getAdminSetup,
  getAdminStorage,
  getAuditEvents,
  getHealth,
  getMetrics,
  listNamespaces,
  listTokens,
  previewAdminSettings,
  previewNamespacePolicy,
  registerNamespace,
  repairConsistency,
  retryAdminQueueFailed,
  restoreAdminConfigBackup,
  revokeToken,
  scanConsistency,
  trimRecords,
  updateNamespacePolicy,
} from "../api/client";
import type {
  AdminQueueBackfillResponse,
  AdminQueueFailureInfo,
  AdminQueueFailuresResponse,
  AdminQueueRetryFailedResponse,
  AdminNamespaceHistoryResponse,
  AdminNamespacePreviewResponse,
  AdminConfigBackupInfo,
  AdminConfigBackupsResponse,
  AdminSettingsMutationResponse,
  AdminQueueResponse,
  AdminSettingsResponse,
  AdminSetupResponse,
  AdminStorageNamespaceInfo,
  AdminStorageResponse,
  AuditEvent,
  AuthToken,
  CompactResponse,
  ConsistencyRepairResponse,
  ConsistencyScanResponse,
  HealthStatus,
  MetricsResponse,
  NamespaceListItem,
  NamespacePolicy,
  RouteMetric,
  TokenCreateResponse,
  TrimResponse,
  TTLCleanupResponse,
} from "../api/types";

type AdminTab =
  | "setup"
  | "config"
  | "management"
  | "namespaces"
  | "access"
  | "metrics"
  | "audit"
  | "roadmap";

interface LoadState {
  configBackups: AdminConfigBackupsResponse | null;
  queueFailures: AdminQueueFailuresResponse | null;
  settings: AdminSettingsResponse | null;
  setup: AdminSetupResponse | null;
  queue: AdminQueueResponse | null;
  storage: AdminStorageResponse | null;
  health: HealthStatus | null;
  metrics: MetricsResponse | null;
  namespaces: NamespaceListItem[];
  tokens: AuthToken[];
  tokenCount: number | null;
  auditEvents: AuditEvent[];
  auditNextCursor: number | null;
  consistency: ConsistencyScanResponse | null;
  errors: string[];
}

interface SetupRow {
  key: string;
  label: string;
  value: string;
  state: "ready" | "warning" | "planned";
  note: string;
}

interface ManagementRow {
  key: string;
  area: string;
  surface: string;
  state: "ready" | "warning" | "planned";
  next: string;
}

interface RoadmapRow {
  key: string;
  phase: string;
  status: "now" | "next" | "later";
  outcome: string;
}

type EditableAdminSettings = AdminSettingsResponse["config"];
type EditableNamespacePolicy = NamespacePolicy["policy"];

const AVAILABLE_SCOPES = [
  "read",
  "write",
  "repair",
  "namespace.register",
  "promote.request",
  "promote.approve",
  "promote.apply",
  "admin",
];

const OWNER_TYPES = ["user", "app", "system"];

const setupColumns: ColumnDef<SetupRow>[] = [
  {
    key: "label",
    header: "Check",
    cell: (row) => row.label,
    sortValue: (row) => row.label,
  },
  {
    key: "value",
    header: "Value",
    width: "fill",
    cell: (row) => <CopyableId id={row.value} label={row.value} />,
    sortValue: (row) => row.value,
  },
  {
    key: "state",
    header: "State",
    cell: (row) => <Pill tone={toneForState(row.state)}>{labelForState(row.state)}</Pill>,
    sortValue: (row) => row.state,
  },
  {
    key: "note",
    header: "Note",
    width: "fill",
    cell: (row) => <span className="text-text-soft">{row.note}</span>,
    sortValue: (row) => row.note,
  },
];

const managementColumns: ColumnDef<ManagementRow>[] = [
  {
    key: "area",
    header: "Area",
    cell: (row) => row.area,
    sortValue: (row) => row.area,
  },
  {
    key: "surface",
    header: "Current Surface",
    width: "fill",
    cell: (row) => <span className="font-mono text-[11px] text-text-soft">{row.surface}</span>,
    sortValue: (row) => row.surface,
  },
  {
    key: "state",
    header: "State",
    cell: (row) => <Pill tone={toneForState(row.state)}>{labelForState(row.state)}</Pill>,
    sortValue: (row) => row.state,
  },
  {
    key: "next",
    header: "Admin Roadmap",
    width: "fill",
    cell: (row) => <span className="text-text-soft">{row.next}</span>,
    sortValue: (row) => row.next,
  },
];

const roadmapColumns: ColumnDef<RoadmapRow>[] = [
  {
    key: "phase",
    header: "Phase",
    cell: (row) => row.phase,
    sortValue: (row) => row.phase,
  },
  {
    key: "status",
    header: "Status",
    cell: (row) => <Pill tone={row.status === "now" ? "success" : "neutral"}>{row.status}</Pill>,
    sortValue: (row) => row.status,
  },
  {
    key: "outcome",
    header: "Outcome",
    width: "fill",
    cell: (row) => <span className="text-text-soft">{row.outcome}</span>,
    sortValue: (row) => row.outcome,
  },
];

const tokenColumns = (
  onRevoke: (token: AuthToken) => void,
  revokingID: string | null,
): ColumnDef<AuthToken>[] => [
  {
    key: "name",
    header: "Name",
    width: "fill",
    cell: (token) => (
      <span className="flex min-w-0 items-center gap-2">
        <Key className="h-3.5 w-3.5 shrink-0 text-text-subtle" />
        <span className="truncate">{token.name}</span>
      </span>
    ),
    sortValue: (token) => token.name,
  },
  {
    key: "client",
    header: "Client",
    width: "fill",
    cell: (token) => <CopyableId id={token.client_id} label={token.client_id} />,
    sortValue: (token) => token.client_id,
  },
  {
    key: "scopes",
    header: "Scopes",
    width: "fill",
    cell: (token) => (
      <span className="flex max-w-72 flex-wrap gap-1">
        {token.scopes.map((scope) => (
          <Pill key={scope} tone="neutral">
            {scope}
          </Pill>
        ))}
      </span>
    ),
    sortValue: (token) => token.scopes.join(","),
  },
  {
    key: "status",
    header: "Status",
    cell: (token) => {
      const expired = new Date(token.expires_at).getTime() < Date.now();
      return (
        <Pill tone={token.revoked ? "danger" : expired ? "warning" : "success"}>
          {token.revoked ? "revoked" : expired ? "expired" : "active"}
        </Pill>
      );
    },
    sortValue: (token) => (token.revoked ? "revoked" : token.expires_at),
  },
  {
    key: "expires",
    header: "Expires",
    cell: (token) => new Date(token.expires_at).toLocaleDateString(),
    sortValue: (token) => token.expires_at,
  },
  {
    key: "actions",
    header: "",
    cell: (token) =>
      token.revoked ? null : (
        <Button
          variant="ghost"
          size="xs"
          onClick={() => onRevoke(token)}
          disabled={revokingID === token.id}
        >
          <Trash2 className={cn("h-3 w-3", revokingID === token.id && "animate-pulse")} />
          Revoke
        </Button>
      ),
  },
];

const auditColumns: ColumnDef<AuditEvent>[] = [
  {
    key: "event",
    header: "Event",
    width: "fill",
    cell: (event) => (
      <span className="flex min-w-0 items-center gap-2">
        <Pill tone={toneForAuditEvent(event.event_type)}>{event.event_type}</Pill>
      </span>
    ),
    sortValue: (event) => event.event_type,
  },
  {
    key: "actor",
    header: "Actor",
    cell: (event) => event.actor || "system",
    sortValue: (event) => event.actor,
  },
  {
    key: "namespace",
    header: "Namespace",
    width: "fill",
    cell: (event) =>
      event.namespace ? (
        <CopyableId id={event.namespace} label={event.namespace} />
      ) : (
        <span className="text-text-subtle">none</span>
      ),
    sortValue: (event) => event.namespace,
  },
  {
    key: "key",
    header: "Key",
    width: "fill",
    cell: (event) =>
      event.key ? (
        <span className="font-mono text-[11px] text-text-soft">{event.key}</span>
      ) : (
        <span className="text-text-subtle">none</span>
      ),
    sortValue: (event) => event.key,
  },
  {
    key: "revision",
    header: "Rev",
    cell: (event) => (event.revision > 0 ? event.revision : "none"),
    sortValue: (event) => event.revision,
  },
  {
    key: "created",
    header: "Created",
    cell: (event) => formatDateTime(event.created_at),
    sortValue: (event) => event.created_at,
  },
];

const topNamespaceColumns: ColumnDef<AdminStorageNamespaceInfo>[] = [
  {
    key: "namespace",
    header: "Namespace",
    width: "fill",
    cell: (row) => <CopyableId id={row.namespace} label={row.namespace} />,
    sortValue: (row) => row.namespace,
  },
  {
    key: "revisions",
    header: "Revisions",
    cell: (row) => row.revisions,
    sortValue: (row) => row.revisions,
  },
  {
    key: "keys",
    header: "Keys",
    cell: (row) => row.keys,
    sortValue: (row) => row.keys,
  },
  {
    key: "oldest",
    header: "Oldest",
    cell: (row) => (row.oldest_created_at ? formatDateTime(row.oldest_created_at) : "none"),
    sortValue: (row) => row.oldest_created_at ?? "",
  },
  {
    key: "newest",
    header: "Newest",
    cell: (row) => (row.newest_created_at ? formatDateTime(row.newest_created_at) : "none"),
    sortValue: (row) => row.newest_created_at ?? "",
  },
];

const routeMetricColumns: ColumnDef<RouteMetric>[] = [
  {
    key: "route",
    header: "Route",
    width: "fill",
    cell: (route) => (
      <span className="flex min-w-0 items-center gap-2">
        <Pill tone="neutral">{route.method}</Pill>
        <span className="min-w-0 truncate font-mono text-[11px] text-text-soft">{route.path}</span>
      </span>
    ),
    sortValue: (route) => `${route.method} ${route.path}`,
  },
  {
    key: "requests",
    header: "Requests",
    cell: (route) => route.requests,
    sortValue: (route) => route.requests,
  },
  {
    key: "errors",
    header: "Errors",
    cell: (route) => <Pill tone={route.errors > 0 ? "warning" : "success"}>{route.errors}</Pill>,
    sortValue: (route) => route.errors,
  },
  {
    key: "error_rate",
    header: "Error Rate",
    cell: (route) => formatPercent(route.requests > 0 ? route.errors / route.requests : 0),
    sortValue: (route) => (route.requests > 0 ? route.errors / route.requests : 0),
  },
  {
    key: "avg_latency",
    header: "Avg Latency",
    cell: (route) => formatLatency(route.latency_ns_avg),
    sortValue: (route) => route.latency_ns_avg,
  },
  {
    key: "statuses",
    header: "Statuses",
    width: "fill",
    cell: (route) => (
      <span className="flex max-w-64 flex-wrap gap-1">
        {Object.entries(route.status_counts)
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([status, count]) => (
            <Pill key={status} tone={Number(status) >= 400 ? "warning" : "neutral"}>
              {status}: {count}
            </Pill>
          ))}
      </span>
    ),
    sortValue: (route) => Object.entries(route.status_counts).length,
  },
  {
    key: "requests_ids",
    header: "Recent IDs",
    width: "fill",
    cell: (route) =>
      route.recent_request_ids?.length ? (
        <span className="flex max-w-72 flex-wrap gap-1">
          {route.recent_request_ids.map((id) => (
            <CopyableId key={id} id={id} label={id} />
          ))}
        </span>
      ) : (
        <span className="text-text-subtle">none</span>
      ),
    sortValue: (route) => route.recent_request_ids?.join(",") ?? "",
  },
];

const namespaceColumns = (
  onEdit: (row: NamespaceListItem) => void,
  editingNamespace: string | null,
): ColumnDef<NamespaceListItem>[] => [
  {
    key: "namespace",
    header: "Namespace",
    width: "fill",
    cell: (row) => <CopyableId id={row.namespace} label={row.namespace} />,
    sortValue: (row) => row.namespace,
  },
  {
    key: "owner",
    header: "Owner",
    width: "fill",
    cell: (row) => (
      <span className="flex min-w-0 items-center gap-2">
        <Pill tone="neutral">{row.owner_type}</Pill>
        <span className="min-w-0 truncate font-mono text-[11px] text-text-soft">
          {row.owner_id}
        </span>
      </span>
    ),
    sortValue: (row) => `${row.owner_type}:${row.owner_id}`,
  },
  {
    key: "tier",
    header: "Tier",
    cell: (row) => <Pill tone="neutral">{policyText(row.policy, "tier") || "unset"}</Pill>,
    sortValue: (row) => policyText(row.policy, "tier"),
  },
  {
    key: "retention",
    header: "Retention",
    cell: (row) => policyText(row.policy, "retention") || "none",
    sortValue: (row) => policyText(row.policy, "retention"),
  },
  {
    key: "revisions",
    header: "Max Revisions",
    cell: (row) => policyText(row.policy, "max_revisions") || "none",
    sortValue: (row) => policyNumber(row.policy, "max_revisions"),
  },
  {
    key: "bytes",
    header: "Max Bytes/Key",
    cell: (row) => {
      const bytes = policyNumber(row.policy, "max_bytes_per_key");
      return bytes > 0 ? formatBytes(bytes) : "none";
    },
    sortValue: (row) => policyNumber(row.policy, "max_bytes_per_key"),
  },
  {
    key: "updated",
    header: "Updated",
    cell: (row) => (row.updated_at ? formatDateTime(row.updated_at) : "unknown"),
    sortValue: (row) => row.updated_at ?? "",
  },
  {
    key: "actions",
    header: "",
    cell: (row) => (
      <Button
        variant="ghost"
        size="xs"
        onClick={() => onEdit(row)}
        disabled={editingNamespace === row.namespace}
      >
        <Settings className="h-3 w-3" />
        Edit
      </Button>
    ),
  },
];

function toneForState(state: SetupRow["state"]) {
  if (state === "ready") return "success";
  if (state === "warning") return "warning";
  return "neutral";
}

function toneForAuditEvent(eventType: string) {
  if (eventType.includes("repair") || eventType.includes("revoke")) return "warning";
  if (eventType.includes("trim") || eventType.includes("compact")) return "warning";
  if (eventType.includes("delete") || eventType.includes("deprecate")) return "danger";
  if (eventType.includes("create") || eventType.includes("write") || eventType.includes("apply")) {
    return "success";
  }
  return "neutral";
}

function labelForState(state: SetupRow["state"]) {
  if (state === "ready") return "available";
  if (state === "warning") return "check";
  return "planned";
}

function formatLatency(ns: number): string {
  if (!Number.isFinite(ns) || ns <= 0) return "0 ms";
  return `${(ns / 1_000_000).toFixed(1)} ms`;
}

function formatPercent(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0%";
  return `${(value * 100).toFixed(1)}%`;
}

function policyText(policy: NamespaceListItem["policy"], key: string): string {
  const value = policy?.[key];
  if (value === undefined || value === null || value === "") return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return "";
}

function policyNumber(policy: NamespaceListItem["policy"], key: string): number {
  const value = policy?.[key];
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string") {
    const parsed = Number.parseFloat(value);
    return Number.isFinite(parsed) ? parsed : 0;
  }
  return 0;
}

function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function formatBytes(bytes?: number): string {
  if (!bytes || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function isHealthy(status?: string): boolean {
  return status === "healthy" || status === "ready" || status === "ok";
}

function isFulfilled<T>(result: PromiseSettledResult<T>): result is PromiseFulfilledResult<T> {
  return result.status === "fulfilled";
}

function errorMessage(label: string, result: PromiseSettledResult<unknown>): string | null {
  if (result.status === "fulfilled") return null;
  const err = result.reason;
  return `${label}: ${err instanceof Error ? err.message : String(err)}`;
}

function trimKey(namespacePattern: string, retention: string): string {
  return `${namespacePattern.trim() || "*"}::${retention.trim()}`;
}

function compactKey(namespacePattern: string, maxRevisions: string): string {
  return `${namespacePattern.trim() || "*"}::${Number.parseInt(maxRevisions, 10) || 10}`;
}

function emptyEditableSettings(): EditableAdminSettings {
  return {
    embedding_provider: "openai",
    embedding_model: "text-embedding-3-large",
    dedup_similarity_threshold: 0.85,
    synthesis_provider: "",
    synthesis_model: "",
    synthesis_max_tokens: 0,
    synthesis_temperature: 0,
    synthesis_system_prompt: "",
    synthesis_system_prompt_set: false,
  };
}

function settingsDraftKey(config: EditableAdminSettings): string {
  return JSON.stringify({
    embedding_provider: config.embedding_provider,
    embedding_model: config.embedding_model,
    dedup_similarity_threshold: config.dedup_similarity_threshold,
    synthesis_provider: config.synthesis_provider,
    synthesis_model: config.synthesis_model,
    synthesis_max_tokens: config.synthesis_max_tokens,
    synthesis_temperature: config.synthesis_temperature,
    synthesis_system_prompt: config.synthesis_system_prompt,
  });
}

function buildNamespacePolicy(
  tier: string,
  retention: string,
  maxRevisions: string,
  maxBytes: string,
): EditableNamespacePolicy {
  const policy: EditableNamespacePolicy = {};
  if (tier.trim()) policy.tier = tier.trim();
  if (retention.trim()) policy.retention = retention.trim();
  const revisions = Number.parseInt(maxRevisions, 10);
  if (Number.isFinite(revisions) && revisions > 0) policy.max_revisions = revisions;
  const bytes = Number.parseInt(maxBytes, 10);
  if (Number.isFinite(bytes) && bytes > 0) policy.max_bytes_per_key = bytes;
  return policy;
}

function AdminField({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <label
      className="grid gap-1.5 text-[11px] uppercase tracking-[.16em] text-text-muted"
      htmlFor={id}
    >
      {label}
      {children}
    </label>
  );
}

function AdminInput(props: ComponentProps<"input">) {
  return (
    <input
      {...props}
      className={cn(
        "h-8 min-w-0 border border-border bg-bg px-2.5 font-mono text-[12px] text-text outline-none transition-colors placeholder:text-text-subtle focus:border-border-strong",
        props.className,
      )}
    />
  );
}

function AdminTextarea(props: ComponentProps<"textarea">) {
  return (
    <textarea
      {...props}
      className={cn(
        "min-h-28 min-w-0 border border-border bg-bg px-2.5 py-2 font-mono text-[12px] text-text outline-none transition-colors placeholder:text-text-subtle focus:border-border-strong",
        props.className,
      )}
    />
  );
}

export function AdminPage() {
  const [tab, setTab] = useState<AdminTab>("setup");
  const [loading, setLoading] = useState(true);
  const [state, setState] = useState<LoadState>({
    configBackups: null,
    queueFailures: null,
    setup: null,
    settings: null,
    queue: null,
    storage: null,
    health: null,
    metrics: null,
    namespaces: [],
    tokens: [],
    tokenCount: null,
    auditEvents: [],
    auditNextCursor: null,
    consistency: null,
    errors: [],
  });
  const [workflowError, setWorkflowError] = useState<string | null>(null);
  const [settingsDraft, setSettingsDraft] = useState<EditableAdminSettings>(emptyEditableSettings);
  const [settingsPreview, setSettingsPreview] = useState<AdminSettingsMutationResponse | null>(null);
  const [settingsPreviewKey, setSettingsPreviewKey] = useState<string | null>(null);
  const [previewingSettings, setPreviewingSettings] = useState(false);
  const [applyingSettings, setApplyingSettings] = useState(false);
  const [creatingConfigBackup, setCreatingConfigBackup] = useState(false);
  const [restoringConfigBackup, setRestoringConfigBackup] = useState<string | null>(null);
  const [queueBackfillNamespace, setQueueBackfillNamespace] = useState("");
  const [queueBackfillLimit, setQueueBackfillLimit] = useState("25");
  const [queueBackfillResult, setQueueBackfillResult] = useState<AdminQueueBackfillResponse | null>(null);
  const [queueRetryResult, setQueueRetryResult] = useState<AdminQueueRetryFailedResponse | null>(null);
  const [runningQueueBackfill, setRunningQueueBackfill] = useState(false);
  const [retryingQueueFailureID, setRetryingQueueFailureID] = useState<number | null>(null);
  const [namespaceName, setNamespaceName] = useState("");
  const [namespaceOwnerType, setNamespaceOwnerType] = useState("app");
  const [namespaceOwnerID, setNamespaceOwnerID] = useState("");
  const [namespaceTier, setNamespaceTier] = useState("");
  const [namespaceRetention, setNamespaceRetention] = useState("");
  const [namespaceMaxRevisions, setNamespaceMaxRevisions] = useState("");
  const [namespaceMaxBytes, setNamespaceMaxBytes] = useState("");
  const [namespacePreview, setNamespacePreview] = useState<AdminNamespacePreviewResponse | null>(null);
  const [namespaceHistory, setNamespaceHistory] = useState<AdminNamespaceHistoryResponse | null>(null);
  const [editingNamespace, setEditingNamespace] = useState<string | null>(null);
  const [previewingNamespace, setPreviewingNamespace] = useState(false);
  const [updatingNamespace, setUpdatingNamespace] = useState(false);
  const [registeringNamespace, setRegisteringNamespace] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [repairing, setRepairing] = useState(false);
  const [repairResult, setRepairResult] = useState<ConsistencyRepairResponse | null>(null);
  const [trimPattern, setTrimPattern] = useState("*");
  const [trimRetention, setTrimRetention] = useState("720h");
  const [trimSubmitting, setTrimSubmitting] = useState(false);
  const [trimResult, setTrimResult] = useState<TrimResponse | null>(null);
  const [trimDryRunKey, setTrimDryRunKey] = useState<string | null>(null);
  const [compactPattern, setCompactPattern] = useState("*");
  const [compactMaxRevisions, setCompactMaxRevisions] = useState("10");
  const [compactSubmitting, setCompactSubmitting] = useState(false);
  const [compactResult, setCompactResult] = useState<CompactResponse | null>(null);
  const [compactDryRunKey, setCompactDryRunKey] = useState<string | null>(null);
  const [ttlSubmitting, setTTLSubmitting] = useState(false);
  const [ttlResult, setTTLResult] = useState<TTLCleanupResponse | null>(null);
  const [revokingID, setRevokingID] = useState<string | null>(null);
  const [tokenName, setTokenName] = useState("");
  const [tokenClientID, setTokenClientID] = useState("");
  const [tokenScopes, setTokenScopes] = useState<string[]>(["read"]);
  const [tokenNamespaces, setTokenNamespaces] = useState("*");
  const [tokenTTL, setTokenTTL] = useState("720h");
  const [creatingToken, setCreatingToken] = useState(false);
  const [createdToken, setCreatedToken] = useState<TokenCreateResponse | null>(null);
  const [tokenCopied, setTokenCopied] = useState(false);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const load = useCallback(() => {
    let cancelled = false;
    setLoading(true);

    Promise.allSettled([
      getAdminSetup(),
      getAdminSettings(),
      getAdminConfigBackups(),
      getAdminQueue(),
      getAdminQueueFailures(),
      getAdminStorage(),
      getHealth(),
      getMetrics(),
      listNamespaces({ limit: 500 }),
      listTokens(),
      getAuditEvents({ limit: 50 }),
      scanConsistency(),
    ])
      .then(
        ([
          setupResult,
          settingsResult,
          backupsResult,
          queueResult,
          queueFailuresResult,
          storageResult,
          healthResult,
          metricsResult,
          namespacesResult,
          tokensResult,
          auditResult,
          consistencyResult,
        ]) => {
          if (cancelled) return;
          setState({
            configBackups: isFulfilled(backupsResult) ? backupsResult.value : null,
            settings: isFulfilled(settingsResult) ? settingsResult.value : null,
            setup: isFulfilled(setupResult) ? setupResult.value : null,
            queue: isFulfilled(queueResult) ? queueResult.value : null,
            queueFailures: isFulfilled(queueFailuresResult) ? queueFailuresResult.value : null,
            storage: isFulfilled(storageResult) ? storageResult.value : null,
            health: isFulfilled(healthResult) ? healthResult.value : null,
            metrics: isFulfilled(metricsResult) ? metricsResult.value : null,
            namespaces: isFulfilled(namespacesResult) ? namespacesResult.value.items : [],
            tokens: isFulfilled(tokensResult) ? tokensResult.value.tokens : [],
            tokenCount: isFulfilled(tokensResult) ? tokensResult.value.tokens.length : null,
            auditEvents: isFulfilled(auditResult) ? auditResult.value.items : [],
            auditNextCursor: isFulfilled(auditResult) ? auditResult.value.next_cursor : null,
            consistency: isFulfilled(consistencyResult) ? consistencyResult.value : null,
            errors: [
              errorMessage("admin setup", setupResult),
              errorMessage("admin settings", settingsResult),
              errorMessage("admin config backups", backupsResult),
              errorMessage("admin queue", queueResult),
              errorMessage("admin queue failures", queueFailuresResult),
              errorMessage("admin storage", storageResult),
              errorMessage("readiness", healthResult),
              errorMessage("metrics", metricsResult),
              errorMessage("namespaces", namespacesResult),
              errorMessage("tokens", tokensResult),
              errorMessage("audit", auditResult),
              errorMessage("consistency", consistencyResult),
            ].filter((msg): msg is string => Boolean(msg)),
          });
        },
      )
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => load(), [load]);

  useEffect(() => {
    if (!state.settings) return;
    setSettingsDraft(state.settings.config);
    setSettingsPreview(null);
    setSettingsPreviewKey(null);
  }, [state.settings]);

  const refreshConfigBackups = useCallback(async () => {
    const result = await getAdminConfigBackups();
    setState((current) => ({
      ...current,
      configBackups: result,
    }));
  }, []);

  const refreshQueueAdmin = useCallback(async () => {
    const [queueResult, failuresResult] = await Promise.all([
      getAdminQueue(),
      getAdminQueueFailures(),
    ]);
    setState((current) => ({
      ...current,
      queue: queueResult,
      queueFailures: failuresResult,
    }));
  }, []);

  const refreshNamespaceInventory = useCallback(async () => {
    const [namespacesResult, storageResult] = await Promise.all([
      listNamespaces({ limit: 500 }),
      getAdminStorage(),
    ]);
    setState((current) => ({
      ...current,
      namespaces: namespacesResult.items,
      storage: storageResult,
    }));
  }, []);

  const refreshAudit = useCallback(async () => {
    const result = await getAuditEvents({ limit: 50 });
    setState((current) => ({
      ...current,
      auditEvents: result.items,
      auditNextCursor: result.next_cursor,
    }));
  }, []);

  const handleLoadMoreAudit = useCallback(async () => {
    if (state.auditNextCursor === null) return;
    setWorkflowError(null);
    try {
      const result = await getAuditEvents({ limit: 50, cursor: state.auditNextCursor });
      setState((current) => ({
        ...current,
        auditEvents: [...current.auditEvents, ...result.items],
        auditNextCursor: result.next_cursor,
      }));
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Audit load failed: ${message}`);
    }
  }, [state.auditNextCursor]);

  const loadNamespaceEditor = useCallback(async (row: NamespaceListItem) => {
    setEditingNamespace(row.namespace);
    setNamespaceName(row.namespace);
    setNamespaceOwnerType(row.owner_type);
    setNamespaceOwnerID(row.owner_id);
    setNamespaceTier(policyText(row.policy, "tier") || "");
    setNamespaceRetention(policyText(row.policy, "retention") || "");
    setNamespaceMaxRevisions(
      policyNumber(row.policy, "max_revisions") > 0 ? String(policyNumber(row.policy, "max_revisions")) : "",
    );
    setNamespaceMaxBytes(
      policyNumber(row.policy, "max_bytes_per_key") > 0
        ? String(policyNumber(row.policy, "max_bytes_per_key"))
        : "",
    );
    setNamespacePreview(null);
    try {
      const history = await getAdminNamespaceHistory(row.namespace);
      setNamespaceHistory(history);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
    }
  }, []);

  const handlePreviewNamespace = useCallback(async () => {
    if (!namespaceName.trim() || !namespaceOwnerID.trim()) return;
    setPreviewingNamespace(true);
    setWorkflowError(null);
    try {
      const result = await previewNamespacePolicy(
        namespaceName.trim(),
        namespaceOwnerType,
        namespaceOwnerID.trim(),
        buildNamespacePolicy(
          namespaceTier,
          namespaceRetention,
          namespaceMaxRevisions,
          namespaceMaxBytes,
        ),
      );
      setNamespacePreview(result);
      if (result.entry.namespace) {
        const history = await getAdminNamespaceHistory(result.entry.namespace);
        setNamespaceHistory(history);
      }
      toast.success(
        result.changed_fields.length === 0
          ? "No namespace policy changes detected"
          : `Preview ready: ${result.changed_fields.length} field${result.changed_fields.length === 1 ? "" : "s"} changed`,
      );
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Namespace preview failed: ${message}`);
    } finally {
      setPreviewingNamespace(false);
    }
  }, [
    namespaceMaxBytes,
    namespaceMaxRevisions,
    namespaceName,
    namespaceOwnerID,
    namespaceOwnerType,
    namespaceRetention,
    namespaceTier,
  ]);

  const handleUpdateNamespace = useCallback(async () => {
    if (!namespaceName.trim() || !namespaceOwnerID.trim()) return;
    setUpdatingNamespace(true);
    setWorkflowError(null);
    try {
      const result = await updateNamespacePolicy(
        namespaceName.trim(),
        namespaceOwnerType,
        namespaceOwnerID.trim(),
        buildNamespacePolicy(
          namespaceTier,
          namespaceRetention,
          namespaceMaxRevisions,
          namespaceMaxBytes,
        ),
      );
      setNamespacePreview(result);
      await refreshNamespaceInventory();
      const history = await getAdminNamespaceHistory(namespaceName.trim());
      setNamespaceHistory(history);
      setEditingNamespace(namespaceName.trim());
      toast.success(
        result.exists
          ? `Namespace "${namespaceName.trim()}" updated`
          : `Namespace "${namespaceName.trim()}" registered`,
      );
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Namespace save failed: ${message}`);
    } finally {
      setUpdatingNamespace(false);
    }
  }, [
    namespaceMaxBytes,
    namespaceMaxRevisions,
    namespaceName,
    namespaceOwnerID,
    namespaceOwnerType,
    namespaceRetention,
    namespaceTier,
    refreshNamespaceInventory,
  ]);

  const handlePreviewSettings = useCallback(async () => {
    setPreviewingSettings(true);
    setWorkflowError(null);
    try {
      const result = await previewAdminSettings({ config: settingsDraft });
      setSettingsPreview(result);
      setSettingsPreviewKey(settingsDraftKey(settingsDraft));
      toast.success(
        result.changed_fields.length === 0
          ? "No config changes detected"
          : `Preview ready: ${result.changed_fields.length} field${result.changed_fields.length === 1 ? "" : "s"} changed`,
      );
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Settings preview failed: ${message}`);
    } finally {
      setPreviewingSettings(false);
    }
  }, [settingsDraft]);

  const handleApplySettings = useCallback(async () => {
    const currentKey = settingsDraftKey(settingsDraft);
    if (settingsPreviewKey !== currentKey) {
      setWorkflowError("Run a matching settings preview before applying.");
      return;
    }
    if (
      !window.confirm(
        "Apply these config changes to config.yaml? Provider and runtime changes require a daemon restart.",
      )
    ) {
      return;
    }
    setApplyingSettings(true);
    setWorkflowError(null);
    try {
      const result = await applyAdminSettings({ config: settingsDraft });
      setSettingsPreview(result);
      setSettingsPreviewKey(currentKey)
      setState((current) =>
        current.settings
          ? {
              ...current,
              settings: {
                ...current.settings,
                config_file: result.config_file,
                config: result.config,
                providers: result.providers,
              },
            }
          : current,
      );
      await refreshConfigBackups();
      toast.success("Config saved to config.yaml. Restart the daemon to apply provider/runtime changes.");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Settings apply failed: ${message}`);
    } finally {
      setApplyingSettings(false);
    }
  }, [refreshConfigBackups, settingsDraft, settingsPreviewKey]);

  const handleCreateConfigBackup = useCallback(async () => {
    setCreatingConfigBackup(true);
    setWorkflowError(null);
    try {
      const result = await createAdminConfigBackup();
      await refreshConfigBackups();
      toast.success(`Created config backup ${result.backup.name}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Config backup failed: ${message}`);
    } finally {
      setCreatingConfigBackup(false);
    }
  }, [refreshConfigBackups]);

  const handleRestoreConfigBackup = useCallback(
    async (backup: AdminConfigBackupInfo) => {
      if (
        !window.confirm(
          `Restore config from "${backup.name}"? A pre-restore safety backup will be created and a daemon restart will still be required.`,
        )
      ) {
        return;
      }
      setRestoringConfigBackup(backup.path);
      setWorkflowError(null);
      try {
        const result = await restoreAdminConfigBackup(backup.path);
        setState((current) =>
          current.settings
            ? {
                ...current,
                settings: {
                  ...current.settings,
                  config_file: result.config_file,
                  config: result.config,
                  providers: result.providers,
                },
              }
            : current,
        );
        setSettingsDraft(result.config);
        setSettingsPreview(null);
        setSettingsPreviewKey(null);
        await refreshConfigBackups();
        toast.success(
          `Restored ${backup.name}. Restart the daemon to apply provider/runtime changes.`,
        );
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        setWorkflowError(message);
        toast.error(`Config restore failed: ${message}`);
      } finally {
        setRestoringConfigBackup(null);
      }
    },
    [refreshConfigBackups],
  );

  const handleRetryQueueFailure = useCallback(
    async (failure: AdminQueueFailureInfo) => {
      setRetryingQueueFailureID(failure.id);
      setWorkflowError(null);
      try {
        const result = await retryAdminQueueFailed(failure.id);
        setQueueRetryResult(result);
        await refreshQueueAdmin();
        toast.success(`Retried ${result.retried} failed queue job${result.retried === 1 ? "" : "s"}`);
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        setWorkflowError(message);
        toast.error(`Queue retry failed: ${message}`);
      } finally {
        setRetryingQueueFailureID(null);
      }
    },
    [refreshQueueAdmin],
  );

  const handleQueueBackfill = useCallback(async () => {
    setRunningQueueBackfill(true);
    setWorkflowError(null);
    try {
      const result = await backfillAdminQueue({
        ...(queueBackfillNamespace.trim()
          ? { namespace: queueBackfillNamespace.trim() }
          : {}),
        limit: Number.parseInt(queueBackfillLimit, 10) || 0,
      });
      setQueueBackfillResult(result);
      await refreshQueueAdmin();
      toast.success(`Queued ${result.queued} embedding backfill job${result.queued === 1 ? "" : "s"}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Queue backfill failed: ${message}`);
    } finally {
      setRunningQueueBackfill(false);
    }
  }, [queueBackfillLimit, queueBackfillNamespace, refreshQueueAdmin]);

  const handleRegisterNamespace = useCallback(async () => {
    if (!namespaceName.trim() || !namespaceOwnerID.trim()) return;
    setRegisteringNamespace(true);
    setWorkflowError(null);
    try {
      await registerNamespace(
        namespaceName.trim(),
        namespaceOwnerType,
        namespaceOwnerID.trim(),
        buildNamespacePolicy(
          namespaceTier,
          namespaceRetention,
          namespaceMaxRevisions,
          namespaceMaxBytes,
        ),
      );
      await refreshNamespaceInventory();
      setNamespacePreview(null);
      setNamespaceHistory(null);
      setEditingNamespace(null);
      setNamespaceName("");
      setNamespaceOwnerID("");
      setNamespaceTier("");
      setNamespaceRetention("");
      setNamespaceMaxRevisions("");
      setNamespaceMaxBytes("");
      toast.success(`Namespace "${namespaceName.trim()}" registered`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Namespace register failed: ${message}`);
    } finally {
      setRegisteringNamespace(false);
    }
  }, [
    namespaceMaxBytes,
    namespaceMaxRevisions,
    namespaceName,
    namespaceOwnerID,
    namespaceOwnerType,
    namespaceRetention,
    namespaceTier,
    refreshNamespaceInventory,
  ]);

  const handleScan = useCallback(async () => {
    setScanning(true);
    setWorkflowError(null);
    setRepairResult(null);
    try {
      const result = await scanConsistency();
      setState((current) => ({ ...current, consistency: result }));
      if (result.count === 0) {
        toast.success("No consistency issues found");
      } else {
        toast.warning(`Found ${result.count} consistency issue${result.count === 1 ? "" : "s"}`);
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Scan failed: ${message}`);
    } finally {
      setScanning(false);
    }
  }, []);

  const handleRepair = useCallback(async () => {
    const count = state.consistency?.count ?? 0;
    if (count <= 0) return;
    if (!window.confirm(`Repair ${count} consistency issue(s) by rebuilding heads?`)) return;
    setRepairing(true);
    setWorkflowError(null);
    try {
      const result = await repairConsistency();
      setRepairResult(result);
      setState((current) => ({
        ...current,
        consistency: { count: result.remaining_issues, issues: result.issues },
      }));
      toast.success(`Repair complete: ${result.rebuilt_heads} heads rebuilt`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Repair failed: ${message}`);
    } finally {
      setRepairing(false);
    }
  }, [state.consistency?.count]);

  const handleTrim = useCallback(
    async (dryRun: boolean) => {
      const currentKey = trimKey(trimPattern, trimRetention);
      if (!dryRun && trimDryRunKey !== currentKey) {
        setWorkflowError("Run a matching trim dry run before applying.");
        return;
      }
      if (
        !dryRun &&
        !window.confirm(
          `Trim records matching "${trimPattern.trim() || "*"}" outside retention ${trimRetention.trim()}?`,
        )
      ) {
        return;
      }
      setTrimSubmitting(true);
      setWorkflowError(null);
      try {
        const result = await trimRecords({
          namespace_pattern: trimPattern.trim() || "*",
          retention: trimRetention.trim(),
          dry_run: dryRun,
        });
        setTrimResult(result);
        if (dryRun) {
          setTrimDryRunKey(currentKey);
          toast.success(`Dry run: ${result.trimmed} records would be trimmed`);
        } else {
          setTrimDryRunKey(null);
          await refreshAudit();
          toast.success(`Trimmed ${result.trimmed} records`);
        }
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        setWorkflowError(message);
        toast.error(`Trim failed: ${message}`);
      } finally {
        setTrimSubmitting(false);
      }
    },
    [refreshAudit, trimDryRunKey, trimPattern, trimRetention],
  );

  const handleCompact = useCallback(
    async (dryRun: boolean) => {
      const currentKey = compactKey(compactPattern, compactMaxRevisions);
      if (!dryRun && compactDryRunKey !== currentKey) {
        setWorkflowError("Run a matching compact dry run before applying.");
        return;
      }
      if (
        !dryRun &&
        !window.confirm(
          `Compact records matching "${compactPattern.trim() || "*"}" to ${Number.parseInt(compactMaxRevisions, 10) || 10} revisions per key?`,
        )
      ) {
        return;
      }
      setCompactSubmitting(true);
      setWorkflowError(null);
      try {
        const result = await compactRecords({
          namespace_pattern: compactPattern.trim() || "*",
          max_revisions: Number.parseInt(compactMaxRevisions, 10) || 10,
          dry_run: dryRun,
        });
        setCompactResult(result);
        if (dryRun) {
          setCompactDryRunKey(currentKey);
          toast.success(`Dry run: ${result.compacted} revisions would be compacted`);
        } else {
          setCompactDryRunKey(null);
          await refreshAudit();
          toast.success(`Compacted ${result.compacted} revisions`);
        }
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        setWorkflowError(message);
        toast.error(`Compact failed: ${message}`);
      } finally {
        setCompactSubmitting(false);
      }
    },
    [compactDryRunKey, compactMaxRevisions, compactPattern, refreshAudit],
  );

  const handleTTLCleanup = useCallback(async () => {
    if (
      !window.confirm(
        "Clean up all expired TTL records? This removes expired records and does not have a dry-run mode yet.",
      )
    ) {
      return;
    }
    setTTLSubmitting(true);
    setWorkflowError(null);
    try {
      const result = await cleanupExpiredTTL();
      setTTLResult(result);
      toast.success(
        `Cleaned ${result.cleaned} expired TTL record${result.cleaned === 1 ? "" : "s"}`,
      );
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`TTL cleanup failed: ${message}`);
    } finally {
      setTTLSubmitting(false);
    }
  }, []);

  const toggleTokenScope = useCallback((scope: string) => {
    setTokenScopes((current) =>
      current.includes(scope) ? current.filter((item) => item !== scope) : [...current, scope],
    );
  }, []);

  const refreshTokens = useCallback(async () => {
    const result = await listTokens();
    setState((current) => ({
      ...current,
      tokens: result.tokens,
      tokenCount: result.tokens.length,
    }));
  }, []);

  const handleCreateToken = useCallback(async () => {
    if (!tokenName.trim() || !tokenClientID.trim() || tokenScopes.length === 0) return;
    setCreatingToken(true);
    setWorkflowError(null);
    try {
      const result = await createToken({
        name: tokenName.trim(),
        client_id: tokenClientID.trim(),
        scopes: tokenScopes,
        namespace_globs: tokenNamespaces
          .split(",")
          .map((item) => item.trim())
          .filter(Boolean),
        ttl: tokenTTL.trim(),
      });
      setCreatedToken(result);
      await refreshTokens();
      await refreshAudit();
      toast.success("Token created. Copy the token value now.");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setWorkflowError(message);
      toast.error(`Token create failed: ${message}`);
    } finally {
      setCreatingToken(false);
    }
  }, [
    refreshAudit,
    refreshTokens,
    tokenClientID,
    tokenName,
    tokenNamespaces,
    tokenScopes,
    tokenTTL,
  ]);

  const handleRevokeToken = useCallback(
    async (token: AuthToken) => {
      if (!window.confirm(`Revoke token "${token.name}"? This cannot be undone.`)) return;
      setRevokingID(token.id);
      setWorkflowError(null);
      try {
        await revokeToken(token.id);
        await refreshTokens();
        await refreshAudit();
        toast.success(`Token "${token.name}" revoked`);
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        setWorkflowError(message);
        toast.error(`Revoke failed: ${message}`);
      } finally {
        setRevokingID(null);
      }
    },
    [refreshAudit, refreshTokens],
  );

  const handleCopyCreatedToken = useCallback(async () => {
    if (!createdToken) return;
    await navigator.clipboard.writeText(createdToken.token);
    setTokenCopied(true);
    toast.success("Token copied");
    setTimeout(() => setTokenCopied(false), 1800);
  }, [createdToken]);

  const namespaceTiers = useMemo(() => {
    const counts = new globalThis.Map<string, number>();
    for (const item of state.namespaces) {
      const { tier: tierValue } = item.policy ?? {};
      const tier = typeof tierValue === "string" ? tierValue : "unset";
      counts.set(tier, (counts.get(tier) ?? 0) + 1);
    }
    return Array.from(counts.entries()).sort(([a], [b]) => a.localeCompare(b));
  }, [state.namespaces]);

  const topRoutes = useMemo(() => {
    return [...(state.metrics?.routes ?? [])].sort((a, b) => b.requests - a.requests).slice(0, 5);
  }, [state.metrics?.routes]);

  const setupRows: SetupRow[] = (state.setup?.paths ?? []).map((path) => ({
    key: path.label,
    label: path.label,
    value: path.path || "unset",
    state: path.exists ? "ready" : path.writable ? "warning" : "planned",
    note: path.exists
      ? `${path.kind}${path.writable ? ", writable" : ", read-only"}`
      : path.writable
        ? "missing, parent writable"
        : "missing or parent not writable",
  }));
  setupRows.push(
    {
      key: "readiness-api",
      label: "readiness-api",
      value: "/v1/health/readiness",
      state: state.health ? "ready" : "warning",
      note: state.health?.status ?? "unavailable",
    },
    {
      key: "namespace-api",
      label: "namespace-api",
      value: "/v1/namespaces/list",
      state: "ready",
      note: `${state.namespaces.length} loaded in admin preflight.`,
    },
  );

  const managementRows: ManagementRow[] = [
    {
      key: "consistency",
      area: "Consistency",
      surface: "/v1/context/consistency/scan + repair",
      state: "ready",
      next: "Fold scan and guarded repair into this admin route.",
    },
    {
      key: "maintenance",
      area: "Retention",
      surface: "/v1/maintenance/trim + compact + ttl-cleanup",
      state: "ready",
      next: "Trim/compact/TTL cleanup are in Admin; audit now shows recent maintenance outcomes.",
    },
    {
      key: "tokens",
      area: "Auth Tokens",
      surface: "/v1/auth/tokens/*",
      state: "ready",
      next: "Access tab now supports token create/list/revoke; next add scoped presets.",
    },
    {
      key: "setup",
      area: "Setup",
      surface: "/v1/admin/settings + preview/apply",
      state: "ready",
      next: "Config tab now previews and writes provider/dedup/synthesis settings; next add backups and runtime restart flow.",
    },
    {
      key: "queues",
      area: "Memory Queues",
      surface: "/v1/admin/queue",
      state: "ready",
      next: "Embedding queue health is in Admin; next add worker error history.",
    },
  ];

  const roadmapRows: RoadmapRow[] = [
    {
      key: "phase-1",
      phase: "Admin Hub Foundation",
      status: "now",
      outcome:
        "Create /admin with live readiness, metrics, namespaces, tokens, consistency, and roadmap tabs.",
    },
    {
      key: "phase-2",
      phase: "Setup And Config Inventory",
      status: "next",
      outcome: "Add backend setup/config endpoint and path checks modeled after Tether settings.",
    },
    {
      key: "phase-3",
      phase: "Management Workflows",
      status: "next",
      outcome: "Consolidate guarded maintenance, repair, token, and audit workflows in /admin.",
    },
    {
      key: "phase-4",
      phase: "System Observability",
      status: "next",
      outcome: "Add request latency, storage growth, queue health, and namespace retention views.",
    },
    {
      key: "phase-5",
      phase: "Style Migration",
      status: "later",
      outcome: "Move user-facing pages from legacy HUD bodies to sysop-ui page primitives.",
    },
  ];

  const tabs: TabStripItem<AdminTab>[] = [
    { key: "setup", label: "Setup", icon: <FileSliders className="h-3.5 w-3.5" /> },
    { key: "config", label: "Config", icon: <Settings className="h-3.5 w-3.5" /> },
    { key: "management", label: "Management", icon: <Wrench className="h-3.5 w-3.5" /> },
    { key: "namespaces", label: "Namespaces", icon: <Tags className="h-3.5 w-3.5" /> },
    { key: "access", label: "Access", icon: <Key className="h-3.5 w-3.5" /> },
    { key: "metrics", label: "Metrics", icon: <HeartPulse className="h-3.5 w-3.5" /> },
    { key: "audit", label: "Audit", icon: <ScrollText className="h-3.5 w-3.5" /> },
    { key: "roadmap", label: "Roadmap", icon: <MapIcon className="h-3.5 w-3.5" /> },
  ];

  const summaryCards = [
    {
      label: "Readiness",
      value: state.health?.status ?? "...",
      accentColor: isHealthy(state.health?.status)
        ? "var(--color-status-done)"
        : "var(--color-status-blocked)",
    },
    { label: "Records", value: state.health?.record_count ?? "..." },
    { label: "Namespaces", value: state.namespaces.length || "..." },
    {
      label: "Consistency",
      value: state.consistency?.count ?? "...",
      accentColor:
        (state.consistency?.count ?? 0) > 0
          ? "var(--color-status-blocked)"
          : "var(--color-status-done)",
    },
    { label: "Tokens", value: state.tokenCount ?? "..." },
    { label: "Queue", value: state.queue?.total ?? "..." },
    { label: "Storage", value: state.storage ? formatBytes(state.storage.total_bytes) : "..." },
    { label: "Audit", value: state.auditEvents.length || "..." },
    { label: "Requests", value: state.metrics?.totals.requests ?? "..." },
    {
      label: "Error Rate",
      value: state.metrics
        ? formatPercent(
            state.metrics.totals.requests > 0
              ? state.metrics.totals.errors / state.metrics.totals.requests
              : 0,
          )
        : "...",
      accentColor:
        (state.metrics?.totals.errors ?? 0) > 0
          ? "var(--color-status-blocked)"
          : "var(--color-status-done)",
    },
  ];

  return (
    <ListPageLayout
      header={null}
      scrollRef={scrollRef}
      tabs={
        <TabStrip
          tabs={tabs}
          value={tab}
          onChange={setTab}
          actions={
            <Button variant="outline" size="sm" onClick={() => load()} disabled={loading}>
              <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
              Refresh
            </Button>
          }
        />
      }
      summary={<SummaryCards cards={summaryCards} />}
      filters={
        <p className="shrink-0 border-b border-border-strong bg-bg px-4 py-1.5 text-[11px] text-text-subtle">
          {state.settings?.config_file ??
            state.setup?.paths.find((path) => path.label === "config-file")?.path ??
            state.health?.db_path ??
            "Loading admin preflight..."}
        </p>
      }
    >
      {state.errors.length > 0 && (
        <SettingsPanel title="Preflight Warnings" icon={<AlertTriangle className="h-3.5 w-3.5" />}>
          <SettingsGrid>
            {state.errors.map((error) => (
              <SettingsField key={error} label="Warning">
                {error}
              </SettingsField>
            ))}
          </SettingsGrid>
        </SettingsPanel>
      )}

      {tab === "setup" && (
        <DataTable
          items={setupRows}
          columns={setupColumns}
          getRowId={(row) => row.key}
          initialSort={{ key: "label", dir: "asc" }}
          scrollRootRef={scrollRef}
          emptyState={
            <EmptyState
              variant="empty"
              title={loading ? "Loading setup..." : "No setup checks"}
              description="The admin preflight returned no setup rows."
            />
          }
        />
      )}

      {tab === "config" && (
        <>
          <SettingsPanel title="Storage" icon={<Database className="h-3.5 w-3.5" />}>
            <SettingsGrid>
              <SettingsField label="Readiness">
                <Pill tone={isHealthy(state.health?.status) ? "success" : "warning"}>
                  {state.health?.status ?? "unknown"}
                </Pill>
              </SettingsField>
              <SettingsField label="DB path">
                <CopyableId
                  id={
                    state.settings?.paths.find((path) => path.label === "main-db")?.path ??
                    state.setup?.paths.find((path) => path.label === "main-db")?.path ??
                    state.health?.db_path ??
                    "unknown"
                  }
                  label={
                    state.settings?.paths.find((path) => path.label === "main-db")?.path ??
                    state.setup?.paths.find((path) => path.label === "main-db")?.path ??
                    state.health?.db_path ??
                    "unknown"
                  }
                />
              </SettingsField>
              <SettingsField label="Records dir">
                <CopyableId
                  id={
                    state.settings?.paths.find((path) => path.label === "records")?.path ??
                    state.setup?.paths.find((path) => path.label === "records")?.path ??
                    state.health?.records_dir ??
                    "unknown"
                  }
                  label={
                    state.settings?.paths.find((path) => path.label === "records")?.path ??
                    state.setup?.paths.find((path) => path.label === "records")?.path ??
                    state.health?.records_dir ??
                    "unknown"
                  }
                />
              </SettingsField>
              <SettingsField label="Schema version">
                {state.health?.schema_version ?? "unknown"}
              </SettingsField>
              <SettingsField label="Records">
                {state.health?.record_count ?? "unknown"}
              </SettingsField>
            </SettingsGrid>
          </SettingsPanel>

          <SettingsPanel title="Access And Policy" icon={<ShieldCheck className="h-3.5 w-3.5" />}>
            <SettingsGrid>
              <SettingsField label="Managed tokens">{state.tokenCount ?? "unknown"}</SettingsField>
              <SettingsField label="Namespace policies">{state.namespaces.length}</SettingsField>
              <SettingsField label="Policy tiers">
                {namespaceTiers.length
                  ? namespaceTiers.map(([tier, count]) => `${tier}: ${count}`).join(", ")
                  : "none loaded"}
              </SettingsField>
              <SettingsField label="Retention policies">
                {state.storage
                  ? `${state.storage.namespace_policy.with_retention} retention, ${state.storage.namespace_policy.with_max_revisions} revision caps, ${state.storage.namespace_policy.without_policy_limits} without limits`
                  : "unknown"}
              </SettingsField>
              <SettingsField label="Auth mode">
                {state.settings?.auth.mode ?? state.setup?.auth.mode ?? "unknown"}
              </SettingsField>
            </SettingsGrid>
          </SettingsPanel>

          <SettingsPanel title="Storage Footprint" icon={<HardDrive className="h-3.5 w-3.5" />}>
            <SettingsGrid>
              <SettingsField label="Total">{formatBytes(state.storage?.total_bytes)}</SettingsField>
              <SettingsField label="Record revisions">
                {state.storage?.records.revisions ?? "unknown"}
              </SettingsField>
              <SettingsField label="Head records">
                {state.storage?.records.heads ?? "unknown"}
              </SettingsField>
              <SettingsField label="Expired TTL records">
                {state.storage?.records.expired ?? "unknown"}
              </SettingsField>
              <SettingsField label="Oldest record">
                {state.storage?.records.oldest_created_at
                  ? formatDateTime(state.storage.records.oldest_created_at)
                  : "none"}
              </SettingsField>
              <SettingsField label="Newest record">
                {state.storage?.records.newest_created_at
                  ? formatDateTime(state.storage.records.newest_created_at)
                  : "none"}
              </SettingsField>
              {state.storage?.paths.map((path) => (
                <SettingsField key={path.label} label={`${path.label} size`}>
                  {path.exists ? formatBytes(path.bytes) : "missing"}
                </SettingsField>
              ))}
            </SettingsGrid>
          </SettingsPanel>

          <SettingsPanel title="Runtime" icon={<ServerCog className="h-3.5 w-3.5" />}>
            <SettingsGrid>
              <SettingsField label="Metrics">
                <Pill tone={state.settings?.runtime.metrics_enabled ? "success" : "warning"}>
                  {state.settings?.runtime.metrics_enabled ? "enabled" : "not available"}
                </Pill>
              </SettingsField>
              <SettingsField label="Request logging">
                {state.settings
                  ? `${state.settings.runtime.request_logging_enabled ? "enabled" : "disabled"} (${state.settings.runtime.request_log_mode})`
                  : "unknown"}
              </SettingsField>
              <SettingsField label="Memory store">
                <Pill tone={state.settings?.runtime.memory_store_enabled ? "success" : "warning"}>
                  {state.settings?.runtime.memory_store_enabled ? "enabled" : "unavailable"}
                </Pill>
              </SettingsField>
              <SettingsField label="Knowledge store">
                <Pill tone={state.settings?.runtime.knowledge_store_enabled ? "success" : "warning"}>
                  {state.settings?.runtime.knowledge_store_enabled ? "enabled" : "unavailable"}
                </Pill>
              </SettingsField>
              <SettingsField label="Route count">
                {state.metrics?.routes.length ?? "unknown"}
              </SettingsField>
              <SettingsField label="Request errors">
                {state.metrics?.totals.errors ?? "unknown"}
              </SettingsField>
              <SettingsField label="Embedding">
                {state.settings
                  ? `${state.settings.config.embedding_provider || "default"} / ${state.settings.config.embedding_model}`
                  : "unknown"}
              </SettingsField>
              <SettingsField label="Dedup threshold">
                {state.settings?.config.dedup_similarity_threshold ?? "unknown"}
              </SettingsField>
              <SettingsField label="Synthesis">
                {state.settings?.runtime.synthesis_enabled
                  ? `${state.settings.config.synthesis_provider} / ${state.settings.config.synthesis_model}`
                  : "disabled"}
              </SettingsField>
              <SettingsField label="Queue DB">
                <CopyableId
                  id={
                    state.settings?.paths.find((path) => path.label === "queue-db")?.path ??
                    "unknown"
                  }
                  label={
                    state.settings?.paths.find((path) => path.label === "queue-db")?.path ??
                    "unknown"
                  }
                />
              </SettingsField>
              <SettingsField label="Web UI embedded">
                <Pill tone={state.settings?.runtime.webui_embedded ? "success" : "warning"}>
                  {state.settings?.runtime.webui_embedded ? "yes" : "no"}
                </Pill>
              </SettingsField>
            </SettingsGrid>
          </SettingsPanel>

          <SettingsPanel title="Editable Settings" icon={<FileSliders className="h-3.5 w-3.5" />}>
            <div className="grid gap-3 px-4 py-3">
              <div className="grid gap-3 md:grid-cols-3">
                <AdminField id="admin-settings-embedding-provider" label="Embedding Provider">
                  <AdminInput
                    id="admin-settings-embedding-provider"
                    value={settingsDraft.embedding_provider}
                    onChange={(event) =>
                      setSettingsDraft((current) => ({
                        ...current,
                        embedding_provider: event.target.value,
                      }))
                    }
                    placeholder="openai"
                  />
                </AdminField>
                <AdminField id="admin-settings-embedding-model" label="Embedding Model">
                  <AdminInput
                    id="admin-settings-embedding-model"
                    value={settingsDraft.embedding_model}
                    onChange={(event) =>
                      setSettingsDraft((current) => ({
                        ...current,
                        embedding_model: event.target.value,
                      }))
                    }
                    placeholder="text-embedding-3-large"
                  />
                </AdminField>
                <AdminField id="admin-settings-dedup" label="Dedup Threshold">
                  <AdminInput
                    id="admin-settings-dedup"
                    type="number"
                    min="0"
                    max="1"
                    step="0.01"
                    value={String(settingsDraft.dedup_similarity_threshold)}
                    onChange={(event) =>
                      setSettingsDraft((current) => ({
                        ...current,
                        dedup_similarity_threshold:
                          Number.parseFloat(event.target.value) || 0,
                      }))
                    }
                    placeholder="0.85"
                  />
                </AdminField>
              </div>

              <div className="grid gap-3 md:grid-cols-4">
                <AdminField id="admin-settings-synthesis-provider" label="Synthesis Provider">
                  <AdminInput
                    id="admin-settings-synthesis-provider"
                    value={settingsDraft.synthesis_provider}
                    onChange={(event) =>
                      setSettingsDraft((current) => ({
                        ...current,
                        synthesis_provider: event.target.value,
                      }))
                    }
                    placeholder="openai or anthropic"
                  />
                </AdminField>
                <AdminField id="admin-settings-synthesis-model" label="Synthesis Model">
                  <AdminInput
                    id="admin-settings-synthesis-model"
                    value={settingsDraft.synthesis_model}
                    onChange={(event) =>
                      setSettingsDraft((current) => ({
                        ...current,
                        synthesis_model: event.target.value,
                      }))
                    }
                    placeholder="gpt-4.1-mini"
                  />
                </AdminField>
                <AdminField id="admin-settings-synthesis-max" label="Synthesis Max Tokens">
                  <AdminInput
                    id="admin-settings-synthesis-max"
                    type="number"
                    min="0"
                    step="1"
                    value={String(settingsDraft.synthesis_max_tokens)}
                    onChange={(event) =>
                      setSettingsDraft((current) => ({
                        ...current,
                        synthesis_max_tokens: Number.parseInt(event.target.value, 10) || 0,
                      }))
                    }
                    placeholder="1024"
                  />
                </AdminField>
                <AdminField id="admin-settings-synthesis-temp" label="Synthesis Temperature">
                  <AdminInput
                    id="admin-settings-synthesis-temp"
                    type="number"
                    min="0"
                    max="2"
                    step="0.1"
                    value={String(settingsDraft.synthesis_temperature)}
                    onChange={(event) =>
                      setSettingsDraft((current) => ({
                        ...current,
                        synthesis_temperature: Number.parseFloat(event.target.value) || 0,
                      }))
                    }
                    placeholder="0.2"
                  />
                </AdminField>
              </div>

              <AdminField id="admin-settings-synthesis-prompt" label="Synthesis System Prompt">
                <AdminTextarea
                  id="admin-settings-synthesis-prompt"
                  value={settingsDraft.synthesis_system_prompt}
                  onChange={(event) =>
                    setSettingsDraft((current) => ({
                      ...current,
                      synthesis_system_prompt: event.target.value,
                      synthesis_system_prompt_set: event.target.value.trim().length > 0,
                    }))
                  }
                  placeholder="Use only supplied sources and cite claims."
                />
              </AdminField>

              <div className="flex flex-wrap gap-2 border-t border-border pt-3">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handlePreviewSettings}
                  disabled={previewingSettings}
                >
                  <Search className={cn("h-3.5 w-3.5", previewingSettings && "animate-pulse")} />
                  Preview Changes
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleApplySettings}
                  disabled={applyingSettings || settingsPreviewKey !== settingsDraftKey(settingsDraft)}
                >
                  <Check className={cn("h-3.5 w-3.5", applyingSettings && "animate-pulse")} />
                  Apply To Config
                </Button>
                <Pill tone="neutral">
                  {state.settings?.config_file || state.settings?.paths.find((path) => path.label === "config-file")?.path || "config.yaml"}
                </Pill>
              </div>

              {settingsPreview && (
                <SettingsGrid>
                  <SettingsField label="Changed fields">
                    {settingsPreview.changed_fields.length > 0
                      ? settingsPreview.changed_fields.join(", ")
                      : "none"}
                  </SettingsField>
                  <SettingsField label="Restart required">
                    <Pill tone={settingsPreview.restart_required ? "warning" : "success"}>
                      {settingsPreview.restart_required ? "yes" : "no"}
                    </Pill>
                  </SettingsField>
                  <SettingsField label="Warnings">
                    {settingsPreview.warnings.length > 0
                      ? settingsPreview.warnings.join(" ")
                      : "none"}
                  </SettingsField>
                  <SettingsField label="Preview target">
                    <CopyableId id={settingsPreview.config_file} label={settingsPreview.config_file} />
                  </SettingsField>
                </SettingsGrid>
              )}
            </div>
          </SettingsPanel>

          <SettingsPanel title="Config Backups" icon={<Archive className="h-3.5 w-3.5" />}>
            <div className="grid gap-3 px-4 py-3">
              <SettingsGrid>
                <SettingsField label="Backup dir">
                  <CopyableId
                    id={state.configBackups?.backup_dir || "unknown"}
                    label={state.configBackups?.backup_dir || "unknown"}
                  />
                </SettingsField>
                <SettingsField label="Snapshots">
                  {state.configBackups?.items.length ?? 0}
                </SettingsField>
              </SettingsGrid>

              <div className="flex flex-wrap gap-2 border-t border-border pt-3">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleCreateConfigBackup}
                  disabled={creatingConfigBackup}
                >
                  <Archive className={cn("h-3.5 w-3.5", creatingConfigBackup && "animate-pulse")} />
                  Create Backup
                </Button>
              </div>

              {state.configBackups?.items.length ? (
                <div className="grid gap-2">
                  {state.configBackups.items.map((backup) => (
                    <div
                      key={backup.path}
                      className="flex flex-wrap items-center justify-between gap-3 border border-border bg-bg px-3 py-2"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="font-mono text-[12px] text-text">{backup.name}</div>
                        <div className="text-[11px] text-text-soft">
                          {formatDateTime(backup.created_at)} · {formatBytes(backup.size)} · {backup.source}
                        </div>
                      </div>
                      <div className="flex flex-wrap gap-2">
                        <Button
                          variant="ghost"
                          size="xs"
                          onClick={() => handleRestoreConfigBackup(backup)}
                          disabled={restoringConfigBackup === backup.path}
                        >
                          <RefreshCw
                            className={cn(
                              "h-3 w-3",
                              restoringConfigBackup === backup.path && "animate-pulse",
                            )}
                          />
                          Restore
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <EmptyState
                  variant="empty"
                  title="No config backups"
                  description="Create a snapshot before larger provider or runtime changes."
                />
              )}
            </div>
          </SettingsPanel>

          <SettingsPanel title="Provider Readiness" icon={<Settings className="h-3.5 w-3.5" />}>
            <SettingsGrid>
              <SettingsField label="Embedding provider">
                {settingsPreview?.providers.embedding.provider ||
                  state.settings?.providers.embedding.provider ||
                  "unconfigured"}
              </SettingsField>
              <SettingsField label="Embedding env">
                {(settingsPreview?.providers.embedding.env_var ||
                  state.settings?.providers.embedding.env_var)
                  ? `${settingsPreview?.providers.embedding.env_var || state.settings?.providers.embedding.env_var}: ${(settingsPreview?.providers.embedding.env_present ?? state.settings?.providers.embedding.env_present) ? "present" : "missing"}`
                  : settingsPreview?.providers.embedding.reason ||
                    state.settings?.providers.embedding.reason ||
                    "n/a"}
              </SettingsField>
              <SettingsField label="Embedding availability">
                <Pill
                  tone={
                    (settingsPreview?.providers.embedding.available ??
                      state.settings?.providers.embedding.available)
                      ? "success"
                      : "warning"
                  }
                >
                  {(settingsPreview?.providers.embedding.available ??
                    state.settings?.providers.embedding.available)
                    ? "available"
                    : "not ready"}
                </Pill>
              </SettingsField>
              <SettingsField label="Synthesis provider">
                {settingsPreview?.providers.synthesis.provider ||
                  state.settings?.providers.synthesis.provider ||
                  "unconfigured"}
              </SettingsField>
              <SettingsField label="Synthesis env">
                {(settingsPreview?.providers.synthesis.env_var ||
                  state.settings?.providers.synthesis.env_var)
                  ? `${settingsPreview?.providers.synthesis.env_var || state.settings?.providers.synthesis.env_var}: ${(settingsPreview?.providers.synthesis.env_present ?? state.settings?.providers.synthesis.env_present) ? "present" : "missing"}`
                  : settingsPreview?.providers.synthesis.reason ||
                    state.settings?.providers.synthesis.reason ||
                    "n/a"}
              </SettingsField>
              <SettingsField label="Synthesis availability">
                <Pill
                  tone={
                    (settingsPreview?.providers.synthesis.available ??
                      state.settings?.providers.synthesis.available)
                      ? "success"
                      : "warning"
                  }
                >
                  {(settingsPreview?.providers.synthesis.available ??
                    state.settings?.providers.synthesis.available)
                    ? "available"
                    : "not ready"}
                </Pill>
              </SettingsField>
            </SettingsGrid>
          </SettingsPanel>

          <SettingsPanel title="Embedding Queue" icon={<Archive className="h-3.5 w-3.5" />}>
            <SettingsGrid>
              <SettingsField label="State">
                <Pill tone={state.queue?.enabled ? "success" : "warning"}>
                  {state.queue?.enabled ? "enabled" : "unavailable"}
                </Pill>
              </SettingsField>
              <SettingsField label="Queue">{state.queue?.queue ?? "unknown"}</SettingsField>
              <SettingsField label="Active jobs">{state.queue?.total ?? "unknown"}</SettingsField>
              <SettingsField label="Available">{state.queue?.available ?? "unknown"}</SettingsField>
              <SettingsField label="Delayed">{state.queue?.delayed ?? "unknown"}</SettingsField>
              <SettingsField label="Reserved">{state.queue?.reserved ?? "unknown"}</SettingsField>
              <SettingsField label="Failed">{state.queue?.failed ?? "unknown"}</SettingsField>
              <SettingsField label="Worker">
                {state.queue
                  ? `${state.queue.worker.configured ? "configured" : "unconfigured"}, ${state.queue.worker.concurrency} worker, max ${state.queue.worker.max_tries} tries`
                  : "unknown"}
              </SettingsField>
              <SettingsField label="Retry cadence">
                {state.queue
                  ? `retry ${state.queue.worker.retry_after}, poll ${state.queue.worker.poll_interval}`
                  : "unknown"}
              </SettingsField>
              <SettingsField label="Oldest job">
                {state.queue?.oldest_created_at
                  ? formatDateTime(state.queue.oldest_created_at)
                  : "none"}
              </SettingsField>
              <SettingsField label="Next available">
                {state.queue?.next_available_at
                  ? formatDateTime(state.queue.next_available_at)
                  : "none"}
              </SettingsField>
              <SettingsField label="Active by type">
                {state.queue?.active_by_type.length
                  ? state.queue.active_by_type
                      .map((item) => `${item.type}: ${item.count}`)
                      .join(", ")
                  : "none"}
              </SettingsField>
            </SettingsGrid>
          </SettingsPanel>

          <SettingsPanel title="Queue Controls" icon={<RefreshCw className="h-3.5 w-3.5" />}>
            <div className="grid gap-3 px-4 py-3">
              <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_10rem_auto]">
                <AdminField id="admin-queue-backfill-namespace" label="Backfill Namespace">
                  <AdminInput
                    id="admin-queue-backfill-namespace"
                    value={queueBackfillNamespace}
                    onChange={(event) => setQueueBackfillNamespace(event.target.value)}
                    placeholder="app/demo"
                  />
                </AdminField>
                <AdminField id="admin-queue-backfill-limit" label="Backfill Limit">
                  <AdminInput
                    id="admin-queue-backfill-limit"
                    type="number"
                    min="0"
                    value={queueBackfillLimit}
                    onChange={(event) => setQueueBackfillLimit(event.target.value)}
                    placeholder="25"
                  />
                </AdminField>
                <div className="flex items-end">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={handleQueueBackfill}
                    disabled={runningQueueBackfill}
                  >
                    <Archive className={cn("h-3.5 w-3.5", runningQueueBackfill && "animate-pulse")} />
                    Queue Backfill
                  </Button>
                </div>
              </div>

              {(queueBackfillResult || queueRetryResult) && (
                <SettingsGrid className="border-t border-border px-0 pt-3">
                  <SettingsField label="Backfill queued">
                    {queueBackfillResult?.queued ?? "none"}
                  </SettingsField>
                  <SettingsField label="Retry result">
                    {queueRetryResult?.retried ?? "none"}
                  </SettingsField>
                </SettingsGrid>
              )}
            </div>
          </SettingsPanel>

          <SettingsPanel title="Queue Failures" icon={<AlertTriangle className="h-3.5 w-3.5" />}>
            <div className="grid gap-2 px-4 py-3">
              {state.queueFailures?.items.length ? (
                state.queueFailures.items.map((failure) => (
                  <div
                    key={failure.id}
                    className="flex flex-wrap items-center justify-between gap-3 border border-border bg-bg px-3 py-2"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="font-mono text-[12px] text-text">
                        #{failure.id} {failure.type}
                      </div>
                      <div className="text-[11px] text-text-soft">
                        {formatDateTime(failure.failed_at)} · attempts {failure.attempts}
                      </div>
                      <div className="text-[11px] text-text-soft">{failure.error}</div>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        variant="ghost"
                        size="xs"
                        onClick={() => handleRetryQueueFailure(failure)}
                        disabled={retryingQueueFailureID === failure.id}
                      >
                        <RefreshCw
                          className={cn(
                            "h-3 w-3",
                            retryingQueueFailureID === failure.id && "animate-pulse",
                          )}
                        />
                        Retry
                      </Button>
                    </div>
                  </div>
                ))
              ) : (
                <EmptyState
                  variant="empty"
                  title="No failed queue jobs"
                  description="Failed embed jobs will appear here for retry."
                />
              )}
            </div>
          </SettingsPanel>

          {topRoutes.length > 0 && (
            <SettingsPanel title="Top Routes" icon={<HeartPulse className="h-3.5 w-3.5" />}>
              <SettingsGrid>
                {topRoutes.map((route) => (
                  <SettingsField
                    key={`${route.method}-${route.path}`}
                    label={`${route.method} ${route.path}`}
                  >
                    {route.requests} requests, {route.errors} errors, avg{" "}
                    {formatLatency(route.latency_ns_avg)}
                  </SettingsField>
                ))}
              </SettingsGrid>
            </SettingsPanel>
          )}

          <DataTable
            items={state.storage?.top_namespaces ?? []}
            columns={topNamespaceColumns}
            getRowId={(row) => row.namespace}
            initialSort={{ key: "revisions", dir: "desc" }}
            scrollRootRef={scrollRef}
            emptyState={
              <EmptyState
                variant="empty"
                title={loading ? "Loading namespace storage..." : "No namespace storage"}
                description="Namespace record distribution will appear after records are written."
              />
            }
          />
        </>
      )}

      {tab === "management" && (
        <>
          {workflowError && (
            <SettingsPanel title="Workflow Error" icon={<AlertTriangle className="h-3.5 w-3.5" />}>
              <SettingsGrid>
                <SettingsField label="Error">{workflowError}</SettingsField>
              </SettingsGrid>
            </SettingsPanel>
          )}

          <SettingsPanel title="Consistency" icon={<Search className="h-3.5 w-3.5" />}>
            <div className="grid gap-3 px-4 py-3 text-[12px] text-text-soft">
              <div className="flex flex-wrap items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleScan}
                  disabled={scanning || repairing}
                >
                  <Search className={cn("h-3.5 w-3.5", scanning && "animate-pulse")} />
                  Scan
                </Button>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={handleRepair}
                  disabled={repairing || (state.consistency?.count ?? 0) <= 0}
                >
                  <Wrench className={cn("h-3.5 w-3.5", repairing && "animate-pulse")} />
                  Repair
                </Button>
                <Pill tone={(state.consistency?.count ?? 0) > 0 ? "warning" : "success"}>
                  {state.consistency?.count ?? 0} issue
                  {(state.consistency?.count ?? 0) === 1 ? "" : "s"}
                </Pill>
              </div>

              {repairResult && (
                <SettingsGrid className="border-t border-border px-0 pt-3">
                  <SettingsField label="Rebuilt heads">{repairResult.rebuilt_heads}</SettingsField>
                  <SettingsField label="Remaining issues">
                    {repairResult.remaining_issues}
                  </SettingsField>
                </SettingsGrid>
              )}

              {(state.consistency?.issues.length ?? 0) > 0 && (
                <div className="max-h-48 overflow-auto border border-border bg-bg">
                  <table className="w-full text-left text-[11px]">
                    <thead className="sticky top-0 bg-panel text-text-muted">
                      <tr>
                        <th className="border-b border-border px-2 py-1.5 font-medium uppercase tracking-[.14em]">
                          Type
                        </th>
                        <th className="border-b border-border px-2 py-1.5 font-medium uppercase tracking-[.14em]">
                          Namespace
                        </th>
                        <th className="border-b border-border px-2 py-1.5 font-medium uppercase tracking-[.14em]">
                          Key
                        </th>
                        <th className="border-b border-border px-2 py-1.5 font-medium uppercase tracking-[.14em]">
                          Details
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {state.consistency?.issues.map((issue) => (
                        <tr key={`${issue.type}-${issue.namespace}-${issue.key}-${issue.details}`}>
                          <td className="border-b border-border/70 px-2 py-1.5">
                            <Pill tone="warning">{issue.type}</Pill>
                          </td>
                          <td className="border-b border-border/70 px-2 py-1.5 font-mono">
                            {issue.namespace}
                          </td>
                          <td className="border-b border-border/70 px-2 py-1.5 font-mono">
                            {issue.key}
                          </td>
                          <td className="border-b border-border/70 px-2 py-1.5">{issue.details}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </SettingsPanel>

          <SettingsPanel title="Retention Trim" icon={<Scissors className="h-3.5 w-3.5" />}>
            <div className="grid gap-3 px-4 py-3">
              <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_12rem_auto_auto]">
                <AdminField id="admin-trim-pattern" label="Namespace Pattern">
                  <AdminInput
                    id="admin-trim-pattern"
                    value={trimPattern}
                    onChange={(event) => setTrimPattern(event.target.value)}
                  />
                </AdminField>
                <AdminField id="admin-trim-retention" label="Retention">
                  <AdminInput
                    id="admin-trim-retention"
                    value={trimRetention}
                    onChange={(event) => setTrimRetention(event.target.value)}
                    placeholder="720h"
                  />
                </AdminField>
                <div className="flex items-end">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => handleTrim(true)}
                    disabled={trimSubmitting}
                  >
                    <Scissors className={cn("h-3.5 w-3.5", trimSubmitting && "animate-pulse")} />
                    Dry Run
                  </Button>
                </div>
                <div className="flex items-end">
                  <Button
                    variant="destructive"
                    size="sm"
                    onClick={() => handleTrim(false)}
                    disabled={
                      trimSubmitting || trimDryRunKey !== trimKey(trimPattern, trimRetention)
                    }
                  >
                    Apply
                  </Button>
                </div>
              </div>
              <p className="text-[11px] text-text-subtle">
                Apply is enabled only after a matching dry run for the current pattern and
                retention.
              </p>
              {trimResult && (
                <SettingsGrid className="border-t border-border px-0 pt-3">
                  <SettingsField label={trimResult.dry_run ? "Would trim" : "Trimmed"}>
                    {trimResult.trimmed}
                  </SettingsField>
                  <SettingsField label="Pattern">{trimResult.namespace_pattern}</SettingsField>
                  <SettingsField label="Duration">{trimResult.duration_ms} ms</SettingsField>
                </SettingsGrid>
              )}
            </div>
          </SettingsPanel>

          <SettingsPanel title="Revision Compact" icon={<Archive className="h-3.5 w-3.5" />}>
            <div className="grid gap-3 px-4 py-3">
              <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_12rem_auto_auto]">
                <AdminField id="admin-compact-pattern" label="Namespace Pattern">
                  <AdminInput
                    id="admin-compact-pattern"
                    value={compactPattern}
                    onChange={(event) => setCompactPattern(event.target.value)}
                  />
                </AdminField>
                <AdminField id="admin-compact-max" label="Max Revisions">
                  <AdminInput
                    id="admin-compact-max"
                    type="number"
                    min="1"
                    value={compactMaxRevisions}
                    onChange={(event) => setCompactMaxRevisions(event.target.value)}
                  />
                </AdminField>
                <div className="flex items-end">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => handleCompact(true)}
                    disabled={compactSubmitting}
                  >
                    <Archive className={cn("h-3.5 w-3.5", compactSubmitting && "animate-pulse")} />
                    Dry Run
                  </Button>
                </div>
                <div className="flex items-end">
                  <Button
                    variant="destructive"
                    size="sm"
                    onClick={() => handleCompact(false)}
                    disabled={
                      compactSubmitting ||
                      compactDryRunKey !== compactKey(compactPattern, compactMaxRevisions)
                    }
                  >
                    Apply
                  </Button>
                </div>
              </div>
              <p className="text-[11px] text-text-subtle">
                Apply is enabled only after a matching dry run for the current pattern and revision
                cap.
              </p>
              {compactResult && (
                <SettingsGrid className="border-t border-border px-0 pt-3">
                  <SettingsField label={compactResult.dry_run ? "Would compact" : "Compacted"}>
                    {compactResult.compacted}
                  </SettingsField>
                  <SettingsField label="Pattern">{compactResult.namespace_pattern}</SettingsField>
                  <SettingsField label="Duration">{compactResult.duration_ms} ms</SettingsField>
                </SettingsGrid>
              )}
            </div>
          </SettingsPanel>

          <SettingsPanel title="TTL Cleanup" icon={<Wrench className="h-3.5 w-3.5" />}>
            <div className="grid gap-3 px-4 py-3">
              <Callout tone="warning" title="No dry-run endpoint yet">
                TTL cleanup removes every record whose TTL is already expired. Use this after
                confirming TTL-backed records are intended to be disposable.
              </Callout>
              <div className="flex flex-wrap items-center gap-2">
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={handleTTLCleanup}
                  disabled={ttlSubmitting}
                >
                  <Wrench className={cn("h-3.5 w-3.5", ttlSubmitting && "animate-pulse")} />
                  Clean Expired TTL
                </Button>
                {ttlResult && (
                  <Pill tone={ttlResult.cleaned > 0 ? "warning" : "success"}>
                    {ttlResult.cleaned} cleaned
                  </Pill>
                )}
              </div>
            </div>
          </SettingsPanel>

          <DataTable
            items={managementRows}
            columns={managementColumns}
            getRowId={(row) => row.key}
            initialSort={{ key: "area", dir: "asc" }}
            scrollRootRef={scrollRef}
            emptyState={
              <EmptyState
                variant="empty"
                title="No management rows"
                description="The admin management inventory returned no rows."
              />
            }
          />
        </>
      )}

      {tab === "namespaces" && (
        <>
          {workflowError && (
            <SettingsPanel title="Namespace Error" icon={<AlertTriangle className="h-3.5 w-3.5" />}>
              <SettingsGrid>
                <SettingsField label="Error">{workflowError}</SettingsField>
              </SettingsGrid>
            </SettingsPanel>
          )}

          <SettingsPanel
            title={editingNamespace ? "Edit Namespace Policy" : "Register Namespace"}
            icon={<Tags className="h-3.5 w-3.5" />}
          >
            <div className="grid gap-3 px-4 py-3">
              <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_9rem_minmax(0,1fr)]">
                <AdminField id="admin-namespace-name" label="Namespace">
                  <AdminInput
                    id="admin-namespace-name"
                    value={namespaceName}
                    onChange={(event) => setNamespaceName(event.target.value)}
                    placeholder="app/my-tool/session"
                  />
                </AdminField>
                <div className="grid gap-1.5">
                  <div className="text-[11px] uppercase tracking-[.16em] text-text-muted">
                    Owner Type
                  </div>
                  <div className="flex h-8 items-center gap-1">
                    {OWNER_TYPES.map((type) => (
                      <button
                        type="button"
                        key={type}
                        onClick={() => setNamespaceOwnerType(type)}
                        className={cn(
                          "h-8 border px-2 font-mono text-[11px] transition-colors",
                          namespaceOwnerType === type
                            ? "border-border-strong bg-panel text-text"
                            : "border-border bg-bg text-text-subtle",
                        )}
                      >
                        {type}
                      </button>
                    ))}
                  </div>
                </div>
                <AdminField id="admin-namespace-owner" label="Owner ID">
                  <AdminInput
                    id="admin-namespace-owner"
                    value={namespaceOwnerID}
                    onChange={(event) => setNamespaceOwnerID(event.target.value)}
                    placeholder="my-tool"
                  />
                </AdminField>
              </div>

              <div className="grid gap-3 md:grid-cols-4">
                <AdminField id="admin-namespace-tier" label="Tier">
                  <AdminInput
                    id="admin-namespace-tier"
                    value={namespaceTier}
                    onChange={(event) => setNamespaceTier(event.target.value)}
                    placeholder="session"
                  />
                </AdminField>
                <AdminField id="admin-namespace-retention" label="Retention">
                  <AdminInput
                    id="admin-namespace-retention"
                    value={namespaceRetention}
                    onChange={(event) => setNamespaceRetention(event.target.value)}
                    placeholder="720h"
                  />
                </AdminField>
                <AdminField id="admin-namespace-revisions" label="Max Revisions">
                  <AdminInput
                    id="admin-namespace-revisions"
                    type="number"
                    min="1"
                    value={namespaceMaxRevisions}
                    onChange={(event) => setNamespaceMaxRevisions(event.target.value)}
                    placeholder="100"
                  />
                </AdminField>
                <AdminField id="admin-namespace-bytes" label="Max Bytes/Key">
                  <AdminInput
                    id="admin-namespace-bytes"
                    type="number"
                    min="1"
                    value={namespaceMaxBytes}
                    onChange={(event) => setNamespaceMaxBytes(event.target.value)}
                    placeholder="1048576"
                  />
                </AdminField>
              </div>

              <div className="flex flex-wrap gap-2 border-t border-border pt-3">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handlePreviewNamespace}
                  disabled={previewingNamespace || !namespaceName.trim() || !namespaceOwnerID.trim()}
                >
                  <Search className={cn("h-3.5 w-3.5", previewingNamespace && "animate-pulse")} />
                  Preview Policy
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleRegisterNamespace}
                  disabled={
                    registeringNamespace || !namespaceName.trim() || !namespaceOwnerID.trim()
                  }
                >
                  <Tags className={cn("h-3.5 w-3.5", registeringNamespace && "animate-pulse")} />
                  Register Namespace
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleUpdateNamespace}
                  disabled={updatingNamespace || !namespaceName.trim() || !namespaceOwnerID.trim()}
                >
                  <Settings className={cn("h-3.5 w-3.5", updatingNamespace && "animate-pulse")} />
                  Save Policy
                </Button>
                {editingNamespace && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setEditingNamespace(null);
                      setNamespacePreview(null);
                      setNamespaceHistory(null);
                      setNamespaceName("");
                      setNamespaceOwnerType("app");
                      setNamespaceOwnerID("");
                      setNamespaceTier("");
                      setNamespaceRetention("");
                      setNamespaceMaxRevisions("");
                      setNamespaceMaxBytes("");
                    }}
                  >
                    Clear
                  </Button>
                )}
                <Pill tone="neutral">{state.namespaces.length} registered</Pill>
              </div>

              {namespacePreview && (
                <SettingsGrid className="border-t border-border px-0 pt-3">
                  <SettingsField label="Mode">
                    <Pill tone={namespacePreview.exists ? "warning" : "success"}>
                      {namespacePreview.exists ? "update" : "new"}
                    </Pill>
                  </SettingsField>
                  <SettingsField label="Changed fields">
                    {namespacePreview.changed_fields.length
                      ? namespacePreview.changed_fields.join(", ")
                      : "none"}
                  </SettingsField>
                  <SettingsField label="Warnings">
                    {namespacePreview.warnings.length
                      ? namespacePreview.warnings.join(" ")
                      : "none"}
                  </SettingsField>
                </SettingsGrid>
              )}
            </div>
          </SettingsPanel>

          {namespaceHistory && (
            <SettingsPanel title="Policy History" icon={<ScrollText className="h-3.5 w-3.5" />}>
              <div className="grid gap-2 px-4 py-3">
                {namespaceHistory.items.length ? (
                  namespaceHistory.items.map((event) => (
                    <div
                      key={event.id}
                      className="flex flex-wrap items-center justify-between gap-3 border border-border bg-bg px-3 py-2"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="font-mono text-[12px] text-text">{event.event_type}</div>
                        <div className="text-[11px] text-text-soft">
                          {formatDateTime(event.created_at)} · {event.actor || "system"}
                        </div>
                      </div>
                    </div>
                  ))
                ) : (
                  <EmptyState
                    variant="empty"
                    title="No policy history"
                    description="Namespace register and update events will appear here."
                  />
                )}
              </div>
            </SettingsPanel>
          )}

          <DataTable
            items={state.namespaces}
            columns={namespaceColumns(loadNamespaceEditor, editingNamespace)}
            getRowId={(row) => row.namespace}
            initialSort={{ key: "namespace", dir: "asc" }}
            scrollRootRef={scrollRef}
            emptyState={
              <EmptyState
                variant="empty"
                title={loading ? "Loading namespaces..." : "No namespaces"}
                description="Registered namespace policy rows will appear here."
              />
            }
          />
        </>
      )}

      {tab === "access" && (
        <>
          {workflowError && (
            <SettingsPanel title="Access Error" icon={<AlertTriangle className="h-3.5 w-3.5" />}>
              <SettingsGrid>
                <SettingsField label="Error">{workflowError}</SettingsField>
              </SettingsGrid>
            </SettingsPanel>
          )}

          <SettingsPanel title="Create Token" icon={<Plus className="h-3.5 w-3.5" />}>
            <div className="grid gap-3 px-4 py-3">
              {createdToken && (
                <div className="border border-status-done/40 bg-status-done/10 p-3">
                  <div className="mb-2 text-[11px] font-semibold uppercase tracking-[.18em] text-status-done">
                    Token created. Copy this value now; it will not be shown again.
                  </div>
                  <div className="flex min-w-0 items-center gap-2 border border-border bg-bg p-2 font-mono text-[12px] text-text">
                    <span className="min-w-0 flex-1 break-all">{createdToken.token}</span>
                    <Button variant="outline" size="icon-sm" onClick={handleCopyCreatedToken}>
                      {tokenCopied ? (
                        <Check className="h-3.5 w-3.5" />
                      ) : (
                        <Copy className="h-3.5 w-3.5" />
                      )}
                    </Button>
                  </div>
                </div>
              )}

              <div className="grid gap-3 md:grid-cols-2">
                <AdminField id="admin-token-name" label="Name">
                  <AdminInput
                    id="admin-token-name"
                    value={tokenName}
                    onChange={(event) => setTokenName(event.target.value)}
                    placeholder="my-agent-token"
                    disabled={Boolean(createdToken)}
                  />
                </AdminField>
                <AdminField id="admin-token-client" label="Client ID">
                  <AdminInput
                    id="admin-token-client"
                    value={tokenClientID}
                    onChange={(event) => setTokenClientID(event.target.value)}
                    placeholder="app:my-agent"
                    disabled={Boolean(createdToken)}
                  />
                </AdminField>
              </div>

              <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_12rem]">
                <AdminField id="admin-token-namespaces" label="Namespace Globs">
                  <AdminInput
                    id="admin-token-namespaces"
                    value={tokenNamespaces}
                    onChange={(event) => setTokenNamespaces(event.target.value)}
                    placeholder="*, user/*, app/demo/*"
                    disabled={Boolean(createdToken)}
                  />
                </AdminField>
                <AdminField id="admin-token-ttl" label="TTL">
                  <AdminInput
                    id="admin-token-ttl"
                    value={tokenTTL}
                    onChange={(event) => setTokenTTL(event.target.value)}
                    placeholder="720h"
                    disabled={Boolean(createdToken)}
                  />
                </AdminField>
              </div>

              <div className="grid gap-1.5">
                <div className="text-[11px] uppercase tracking-[.16em] text-text-muted">Scopes</div>
                <div className="flex flex-wrap gap-2">
                  {AVAILABLE_SCOPES.map((scope) => {
                    const checked = tokenScopes.includes(scope);
                    return (
                      <button
                        type="button"
                        key={scope}
                        onClick={() => toggleTokenScope(scope)}
                        disabled={Boolean(createdToken)}
                        className={cn(
                          "border px-2 py-1 font-mono text-[11px] transition-colors",
                          checked
                            ? "border-border-strong bg-panel text-text"
                            : "border-border bg-bg text-text-subtle",
                        )}
                      >
                        {scope}
                      </button>
                    );
                  })}
                </div>
              </div>

              <div className="flex flex-wrap gap-2 border-t border-border pt-3">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleCreateToken}
                  disabled={
                    creatingToken ||
                    Boolean(createdToken) ||
                    !tokenName.trim() ||
                    !tokenClientID.trim() ||
                    tokenScopes.length === 0
                  }
                >
                  <Key className={cn("h-3.5 w-3.5", creatingToken && "animate-pulse")} />
                  Create Token
                </Button>
                {createdToken && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setCreatedToken(null);
                      setTokenCopied(false);
                      setTokenName("");
                      setTokenClientID("");
                      setTokenScopes(["read"]);
                      setTokenNamespaces("*");
                      setTokenTTL("720h");
                    }}
                  >
                    New Token
                  </Button>
                )}
              </div>
            </div>
          </SettingsPanel>

          <DataTable
            items={state.tokens}
            columns={tokenColumns(handleRevokeToken, revokingID)}
            getRowId={(token) => token.id}
            initialSort={{ key: "name", dir: "asc" }}
            scrollRootRef={scrollRef}
            emptyState={
              <EmptyState
                variant="empty"
                title={loading ? "Loading tokens..." : "No tokens"}
                description="Create a managed auth token to scope API writes."
              />
            }
          />
        </>
      )}

      {tab === "metrics" && (
        <>
          <SettingsPanel title="API Metrics" icon={<HeartPulse className="h-3.5 w-3.5" />}>
            <SettingsGrid>
              <SettingsField label="State">
                <Pill tone={state.setup?.runtime.metrics_enabled ? "success" : "warning"}>
                  {state.setup?.runtime.metrics_enabled ? "enabled" : "not available"}
                </Pill>
              </SettingsField>
              <SettingsField label="Requests">
                {state.metrics?.totals.requests ?? "unknown"}
              </SettingsField>
              <SettingsField label="Errors">
                {state.metrics?.totals.errors ?? "unknown"}
              </SettingsField>
              <SettingsField label="Error rate">
                {state.metrics
                  ? formatPercent(
                      state.metrics.totals.requests > 0
                        ? state.metrics.totals.errors / state.metrics.totals.requests
                        : 0,
                    )
                  : "unknown"}
              </SettingsField>
              <SettingsField label="Observed routes">
                {state.metrics?.routes.length ?? "unknown"}
              </SettingsField>
              <SettingsField label="Slowest route">
                {state.metrics?.routes.length
                  ? [...state.metrics.routes].sort((a, b) => b.latency_ns_avg - a.latency_ns_avg)[0]
                      ?.path
                  : "none"}
              </SettingsField>
            </SettingsGrid>
          </SettingsPanel>

          <DataTable
            items={state.metrics?.routes ?? []}
            columns={routeMetricColumns}
            getRowId={(route) => `${route.method}-${route.path}`}
            initialSort={{ key: "requests", dir: "desc" }}
            scrollRootRef={scrollRef}
            emptyState={
              <EmptyState
                variant="empty"
                title={loading ? "Loading metrics..." : "No route metrics"}
                description="Enable metrics and send API traffic to populate this table."
              />
            }
          />
        </>
      )}

      {tab === "audit" && (
        <>
          {workflowError && (
            <SettingsPanel title="Audit Error" icon={<AlertTriangle className="h-3.5 w-3.5" />}>
              <SettingsGrid>
                <SettingsField label="Error">{workflowError}</SettingsField>
              </SettingsGrid>
            </SettingsPanel>
          )}

          <SettingsPanel title="Recent Operations" icon={<ScrollText className="h-3.5 w-3.5" />}>
            <div className="flex flex-wrap items-center gap-2 px-4 py-3 text-[12px] text-text-soft">
              <Button variant="outline" size="sm" onClick={refreshAudit}>
                <RefreshCw className="h-3.5 w-3.5" />
                Refresh Audit
              </Button>
              <Pill tone="neutral">{state.auditEvents.length} loaded</Pill>
              {state.auditNextCursor !== null && (
                <Button variant="ghost" size="sm" onClick={handleLoadMoreAudit}>
                  Load More
                </Button>
              )}
            </div>
          </SettingsPanel>

          <DataTable
            items={state.auditEvents}
            columns={auditColumns}
            getRowId={(event) => String(event.id)}
            initialSort={{ key: "created", dir: "desc" }}
            scrollRootRef={scrollRef}
            emptyState={
              <EmptyState
                variant="empty"
                title={loading ? "Loading audit events..." : "No audit events"}
                description="Mutating API operations will appear here when audit logging is enabled."
              />
            }
          />
        </>
      )}

      {tab === "roadmap" && (
        <DataTable
          items={roadmapRows}
          columns={roadmapColumns}
          getRowId={(row) => row.key}
          initialSort={{ key: "status", dir: "asc" }}
          scrollRootRef={scrollRef}
          emptyState={
            <EmptyState
              variant="empty"
              title="No roadmap rows"
              description="The admin roadmap returned no rows."
            />
          }
        />
      )}
    </ListPageLayout>
  );
}
