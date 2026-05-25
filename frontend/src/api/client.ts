import { demo, isDemoMode } from "../demo/data";
import type {
  AdminQueueBackfillResponse,
  AdminQueueFailuresResponse,
  AdminQueueRetryFailedResponse,
  AdminNamespaceHistoryResponse,
  AdminNamespacePreviewResponse,
  AdminConfigBackupResponse,
  AdminConfigBackupsResponse,
  AdminConfigRestoreResponse,
  AdminSettingsMutationResponse,
  AdminQueueResponse,
  AdminSettingsResponse,
  AdminSettingsUpdateRequest,
  AdminSetupResponse,
  AdminStorageResponse,
  AuditResponse,
  AuthToken,
  BrokerPlanRequest,
  BrokerPlanResponse,
  CompactRequest,
  CompactResponse,
  TesseractLookupRequest,
  TesseractLookupResponse,
  ConsistencyRepairResponse,
  ConsistencyScanResponse,
  EstimateResponse,
  HealthStatus,
  KnowledgeRevision,
  KnowledgeWriteRequest,
  MemoryDeprecateRequest,
  MemoryDeprecateResponse,
  MemoryPromoteRequest,
  MemoryRevision,
  MemoryWriteRequest,
  MetricsResponse,
  NamespaceListResponse,
  NamespacePolicy,
  PacketRequest,
  PacketResponse,
  PromoteApplyPayload,
  PromoteApprovePayload,
  PromoteRequestPayload,
  RecallResponse,
  Record,
  Selector,
  SynthesisAskRequest,
  SynthesisAskResponse,
  TokenCreateRequest,
  TokenCreateResponse,
  TrimRequest,
  TrimResponse,
  TTLCleanupResponse,
  ViewResponse,
  WriteRequest,
  WriteResponse,
} from "./types";

// ── Base URL management ─────────────────────────────────────────────

let _base = ""; // empty = same origin

export function setBaseURL(url: string) {
  _base = url;
}

export function getBaseURL(): string {
  return _base;
}

// ── Generic fetch helper ────────────────────────────────────────────

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(`${_base}${path}`, {
    headers: { "Content-Type": "application/json", ...init?.headers },
    ...init,
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ message: resp.statusText }));
    throw new Error(err.message ?? err.code ?? `HTTP ${resp.status}`);
  }
  return resp.json() as Promise<T>;
}

// ── Health ──────────────────────────────────────────────────────────

export async function getHealth(): Promise<HealthStatus> {
  if (isDemoMode()) return demo.getHealth();
  return apiFetch<HealthStatus>("/v1/health/readiness");
}

export async function getAdminSetup(): Promise<AdminSetupResponse> {
  if (isDemoMode()) return demo.getAdminSetup();
  return apiFetch<AdminSetupResponse>("/v1/admin/setup");
}

export async function getAdminSettings(): Promise<AdminSettingsResponse> {
  if (isDemoMode()) {
    const setup = await demo.getAdminSetup();
    return {
      app: setup.app,
      config_file: setup.paths.find((path) => path.label === "config-file")?.path ?? "",
      paths: setup.paths,
      auth: setup.auth,
      runtime: {
        ...setup.runtime,
        queue_enabled: false,
        webui_embedded: true,
      },
      config: {
        ...setup.config,
        synthesis_system_prompt: "",
      },
      providers: {
        embedding: {
          kind: "embedding",
          provider: setup.config.embedding_provider,
          model: setup.config.embedding_model,
          env_var: "OPENAI_API_KEY",
          configured: Boolean(setup.config.embedding_provider),
          supported: setup.config.embedding_provider === "openai",
          env_present: false,
          runtime_ready: setup.runtime.memory_store_enabled,
          available: false,
          reason: "demo mode",
        },
        synthesis: {
          kind: "synthesis",
          provider: setup.config.synthesis_provider,
          model: setup.config.synthesis_model,
          env_var:
            setup.config.synthesis_provider === "anthropic"
              ? "ANTHROPIC_API_KEY"
              : "OPENAI_API_KEY",
          configured: Boolean(setup.config.synthesis_provider),
          supported:
            setup.config.synthesis_provider === "openai" ||
            setup.config.synthesis_provider === "anthropic",
          env_present: false,
          runtime_ready: setup.runtime.synthesis_enabled,
          available: false,
          reason: "demo mode",
        },
      },
    };
  }
  return apiFetch<AdminSettingsResponse>("/v1/admin/settings");
}

export async function previewAdminSettings(
  req: AdminSettingsUpdateRequest,
): Promise<AdminSettingsMutationResponse> {
  if (isDemoMode()) {
    const settings = await getAdminSettings();
    return {
      config_file: settings.config_file,
      config: req.config,
      providers: settings.providers,
      changed_fields: ["demo.settings"],
      warnings: ["demo mode does not persist settings"],
      restart_required: true,
      applied: false,
    };
  }
  return apiFetch<AdminSettingsMutationResponse>("/v1/admin/settings/preview", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function applyAdminSettings(
  req: AdminSettingsUpdateRequest,
): Promise<AdminSettingsMutationResponse> {
  if (isDemoMode()) {
    const preview = await previewAdminSettings(req);
    return { ...preview, applied: true };
  }
  return apiFetch<AdminSettingsMutationResponse>("/v1/admin/settings/apply", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function getAdminConfigBackups(): Promise<AdminConfigBackupsResponse> {
  if (isDemoMode()) {
    const settings = await getAdminSettings();
    return {
      config_file: settings.config_file,
      backup_dir: "/demo/config/backups",
      items: [],
    };
  }
  return apiFetch<AdminConfigBackupsResponse>("/v1/admin/config/backups");
}

export async function createAdminConfigBackup(): Promise<AdminConfigBackupResponse> {
  if (isDemoMode()) {
    return {
      config_file: "/demo/config/config.yaml",
      backup_dir: "/demo/config/backups",
      backup: {
        name: "config-demo.yaml",
        path: "/demo/config/backups/config-demo.yaml",
        size: 512,
        source: "demo",
        created_at: new Date().toISOString(),
      },
    };
  }
  return apiFetch<AdminConfigBackupResponse>("/v1/admin/config/backup", {
    method: "POST",
    body: JSON.stringify({}),
  });
}

export async function restoreAdminConfigBackup(path: string): Promise<AdminConfigRestoreResponse> {
  if (isDemoMode()) {
    const settings = await getAdminSettings();
    return {
      config_file: settings.config_file,
      restored_from: {
        name: "config-demo.yaml",
        path,
        size: 512,
        source: "demo",
        created_at: new Date().toISOString(),
      },
      pre_restore_backup: {
        name: "config-demo-pre-restore.yaml",
        path: "/demo/config/backups/config-demo-pre-restore.yaml",
        size: 512,
        source: "pre-restore",
        created_at: new Date().toISOString(),
      },
      config: settings.config,
      providers: settings.providers,
      restart_required: true,
    };
  }
  return apiFetch<AdminConfigRestoreResponse>("/v1/admin/config/restore", {
    method: "POST",
    body: JSON.stringify({ path }),
  });
}

export async function getAdminQueue(): Promise<AdminQueueResponse> {
  if (isDemoMode()) return demo.getAdminQueue();
  return apiFetch<AdminQueueResponse>("/v1/admin/queue");
}

export async function getAdminQueueFailures(limit = 25): Promise<AdminQueueFailuresResponse> {
  if (isDemoMode()) {
    return {
      count: 1,
      items: [
        {
          id: 1,
          queue: "tesseract",
          type: "embed",
          error: "demo embedding provider timeout",
          attempts: 3,
          failed_at: new Date().toISOString(),
          payload: '{"revision_id":"demo-rev"}',
        },
      ],
    };
  }
  return apiFetch<AdminQueueFailuresResponse>(`/v1/admin/queue/failures?limit=${limit}`);
}

export async function retryAdminQueueFailed(id?: number): Promise<AdminQueueRetryFailedResponse> {
  if (isDemoMode()) return { retried: id ? 1 : 1 };
  return apiFetch<AdminQueueRetryFailedResponse>("/v1/admin/queue/retry-failed", {
    method: "POST",
    body: JSON.stringify(id ? { id } : {}),
  });
}

export async function backfillAdminQueue(params?: {
  namespace?: string;
  limit?: number;
}): Promise<AdminQueueBackfillResponse> {
  if (isDemoMode()) {
    return {
      queued: params?.limit ?? 3,
      ...(params?.namespace ? { namespace: params.namespace } : {}),
      limit: params?.limit ?? 3,
    };
  }
  return apiFetch<AdminQueueBackfillResponse>("/v1/admin/queue/backfill", {
    method: "POST",
    body: JSON.stringify({
      namespace: params?.namespace ?? "",
      limit: params?.limit ?? 0,
    }),
  });
}

export async function getAdminStorage(): Promise<AdminStorageResponse> {
  if (isDemoMode()) return demo.getAdminStorage();
  return apiFetch<AdminStorageResponse>("/v1/admin/storage");
}

// ── Views ───────────────────────────────────────────────────────────

export async function evaluateView(
  selector: Selector,
  includePayload = false,
): Promise<ViewResponse> {
  if (isDemoMode()) return demo.evaluateView(selector.namespaces);
  return apiFetch<ViewResponse>("/v1/views/evaluate", {
    method: "POST",
    body: JSON.stringify({ selector, include_payload: includePayload }),
  });
}

// ── Estimate ────────────────────────────────────────────────────────

export async function estimate(selector: Selector): Promise<EstimateResponse> {
  if (isDemoMode()) return demo.estimate();
  return apiFetch<EstimateResponse>("/v1/context/estimate", {
    method: "POST",
    body: JSON.stringify({ selector }),
  });
}

// ── Records ─────────────────────────────────────────────────────────

export async function getHead(namespace: string, key: string): Promise<{ record: Record }> {
  if (isDemoMode()) return demo.getHead(namespace, key);
  const q = new URLSearchParams({ namespace, key });
  return apiFetch<{ record: Record }>(`/v1/context/head?${q}`);
}

export async function getHistory(
  namespace: string,
  key: string,
  limit?: number,
): Promise<{ items: Record[]; next_cursor: null }> {
  if (isDemoMode()) return demo.getHistory(namespace, key);
  const q = new URLSearchParams({ namespace, key });
  if (limit) q.set("limit", String(limit));
  return apiFetch<{ items: Record[]; next_cursor: null }>(`/v1/context/history?${q}`);
}

// ── Write ───────────────────────────────────────────────────────────

export async function writeRecord(req: WriteRequest): Promise<WriteResponse> {
  if (isDemoMode()) return demo.writeRecord();
  return apiFetch<WriteResponse>("/v1/context/write", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// ── Promote ─────────────────────────────────────────────────────────

export async function promoteRequest(req: PromoteRequestPayload): Promise<unknown> {
  if (isDemoMode()) return demo.promoteRequest();
  return apiFetch("/v1/context/promote/request", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function promoteApprove(req: PromoteApprovePayload): Promise<unknown> {
  if (isDemoMode()) return demo.promoteApprove();
  return apiFetch("/v1/context/promote/approve", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function promoteApply(req: PromoteApplyPayload): Promise<unknown> {
  if (isDemoMode()) return demo.promoteApply();
  return apiFetch("/v1/context/promote/apply", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// ── Packet ──────────────────────────────────────────────────────────

export async function buildPacket(req: PacketRequest): Promise<PacketResponse> {
  if (isDemoMode()) return demo.buildPacket();
  return apiFetch<PacketResponse>("/v1/context/packet", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// ── Audit ───────────────────────────────────────────────────────────

export async function getAuditEvents(params?: {
  limit?: number;
  cursor?: number;
  namespace?: string;
  event_type?: string;
  actor?: string;
  // RFC3339 inclusive lower / upper bounds on created_at. Backend rejects
  // non-RFC3339 strings.
  since?: string;
  until?: string;
}): Promise<AuditResponse> {
  if (isDemoMode()) return demo.getAuditEvents(params?.limit);
  const q = new URLSearchParams();
  if (params?.limit) q.set("limit", String(params.limit));
  if (params?.cursor) q.set("cursor", String(params.cursor));
  if (params?.namespace) q.set("namespace", params.namespace);
  if (params?.event_type) q.set("event_type", params.event_type);
  if (params?.actor) q.set("actor", params.actor);
  if (params?.since) q.set("since", params.since);
  if (params?.until) q.set("until", params.until);
  const qs = q.toString() ? `?${q}` : "";
  return apiFetch<AuditResponse>(`/v1/context/audit${qs}`);
}

// ── Metrics ─────────────────────────────────────────────────────────

export async function getMetrics(): Promise<MetricsResponse> {
  if (isDemoMode()) return demo.getMetrics();
  return apiFetch<MetricsResponse>("/v1/metrics");
}

// ── Namespaces / Policy ─────────────────────────────────────────────

export async function getNamespacePolicy(namespace: string): Promise<NamespacePolicy> {
  if (isDemoMode()) return demo.getNamespacePolicy(namespace);
  return apiFetch<NamespacePolicy>(`/v1/namespaces/get?namespace=${encodeURIComponent(namespace)}`);
}

export async function registerNamespace(
  namespace: string,
  ownerType: string,
  ownerId: string,
  policy: NamespacePolicy["policy"],
): Promise<NamespacePolicy> {
  if (isDemoMode()) return demo.registerNamespace(namespace);
  return apiFetch<NamespacePolicy>("/v1/namespaces/register", {
    method: "POST",
    body: JSON.stringify({
      namespace,
      owner_type: ownerType,
      owner_id: ownerId,
      policy,
    }),
  });
}

export async function previewNamespacePolicy(
  namespace: string,
  ownerType: string,
  ownerId: string,
  policy: NamespacePolicy["policy"],
): Promise<AdminNamespacePreviewResponse> {
  if (isDemoMode()) {
    return {
      entry: { namespace, owner_type: ownerType, owner_id: ownerId, policy },
      exists: false,
      changed_fields: ["namespace", "policy"],
      warnings: [],
    };
  }
  return apiFetch<AdminNamespacePreviewResponse>("/v1/admin/namespaces/preview", {
    method: "POST",
    body: JSON.stringify({
      namespace,
      owner_type: ownerType,
      owner_id: ownerId,
      policy,
    }),
  });
}

export async function updateNamespacePolicy(
  namespace: string,
  ownerType: string,
  ownerId: string,
  policy: NamespacePolicy["policy"],
): Promise<AdminNamespacePreviewResponse> {
  if (isDemoMode()) {
    return {
      entry: { namespace, owner_type: ownerType, owner_id: ownerId, policy },
      exists: true,
      changed_fields: ["policy"],
      warnings: [],
    };
  }
  return apiFetch<AdminNamespacePreviewResponse>("/v1/admin/namespaces/update", {
    method: "POST",
    body: JSON.stringify({
      namespace,
      owner_type: ownerType,
      owner_id: ownerId,
      policy,
    }),
  });
}

export async function getAdminNamespaceHistory(
  namespace: string,
  limit = 20,
): Promise<AdminNamespaceHistoryResponse> {
  if (isDemoMode()) {
    return {
      namespace,
      count: 0,
      items: [],
    };
  }
  const q = new URLSearchParams({ namespace, limit: String(limit) });
  return apiFetch<AdminNamespaceHistoryResponse>(`/v1/admin/namespaces/history?${q.toString()}`);
}

export async function listNamespaces(params?: {
  prefix?: string;
  limit?: number;
}): Promise<NamespaceListResponse> {
  if (isDemoMode()) return demo.listNamespaces(params?.prefix);
  const q = new URLSearchParams();
  if (params?.prefix) q.set("prefix", params.prefix);
  if (params?.limit) q.set("limit", String(params.limit));
  const qs = q.toString() ? `?${q.toString()}` : "";
  return apiFetch<NamespaceListResponse>(`/v1/namespaces/list${qs}`);
}

// ── Memory ──────────────────────────────────────────────────────────

export async function getMemoryCurrent(
  namespace: string,
  memoryKey: string,
): Promise<MemoryRevision> {
  if (isDemoMode()) return demo.getMemoryCurrent(namespace, memoryKey);
  const q = new URLSearchParams({ namespace, memory_key: memoryKey });
  return apiFetch<MemoryRevision>(`/v1/memory/current?${q.toString()}`);
}

export async function getMemoryHistory(
  namespace: string,
  memoryKey: string,
): Promise<MemoryRevision[]> {
  if (isDemoMode()) return demo.getMemoryHistory(namespace, memoryKey);
  const q = new URLSearchParams({ namespace, memory_key: memoryKey });
  return apiFetch<MemoryRevision[]>(`/v1/memory/history?${q.toString()}`);
}

// ── Knowledge ───────────────────────────────────────────────────────

export async function getKnowledgeCurrent(
  namespace: string,
  memoryKey: string,
): Promise<KnowledgeRevision> {
  if (isDemoMode()) return demo.getKnowledgeCurrent(namespace, memoryKey);
  const q = new URLSearchParams({ namespace, memory_key: memoryKey });
  return apiFetch<KnowledgeRevision>(`/v1/knowledge/current?${q.toString()}`);
}

export async function getKnowledgeHistory(
  namespace: string,
  memoryKey: string,
): Promise<KnowledgeRevision[]> {
  if (isDemoMode()) return demo.getKnowledgeHistory(namespace, memoryKey);
  const q = new URLSearchParams({ namespace, memory_key: memoryKey });
  return apiFetch<KnowledgeRevision[]>(`/v1/knowledge/history?${q.toString()}`);
}

export async function knowledgeWrite(req: KnowledgeWriteRequest): Promise<KnowledgeRevision> {
  if (isDemoMode()) return demo.knowledgeWrite(req);
  return apiFetch<KnowledgeRevision>("/v1/knowledge/write", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// ── Memory write / promote / deprecate ──────────────────────────────

export async function memoryWrite(req: MemoryWriteRequest): Promise<MemoryRevision> {
  if (isDemoMode()) return demo.memoryWrite(req);
  return apiFetch<MemoryRevision>("/v1/memory/write", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function memoryPromote(req: MemoryPromoteRequest): Promise<MemoryRevision> {
  if (isDemoMode()) return demo.memoryPromote(req);
  return apiFetch<MemoryRevision>("/v1/memory/promote", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function memoryDeprecate(
  req: MemoryDeprecateRequest,
): Promise<MemoryDeprecateResponse> {
  if (isDemoMode()) return demo.memoryDeprecate(req);
  return apiFetch<MemoryDeprecateResponse>("/v1/memory/deprecate", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// ── Synthesis (LLM-backed answer) ───────────────────────────────────

export async function synthesisAsk(req: SynthesisAskRequest): Promise<SynthesisAskResponse> {
  if (isDemoMode()) return demo.synthesisAsk(req);
  return apiFetch<SynthesisAskResponse>("/v1/synthesis/ask", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// ── Tesseract lookup (unified search) ─────────────────────────────────

export async function tesseractLookup(req: TesseractLookupRequest): Promise<TesseractLookupResponse> {
  if (isDemoMode()) return demo.tesseractLookup(req);
  return apiFetch<TesseractLookupResponse>("/v1/tesseract/lookup", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// ── Auth Tokens ─────────────────────────────────────────────────────

export async function createToken(req: TokenCreateRequest): Promise<TokenCreateResponse> {
  if (isDemoMode()) return demo.createToken();
  return apiFetch<TokenCreateResponse>("/v1/auth/tokens/create", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function listTokens(): Promise<{ tokens: AuthToken[] }> {
  if (isDemoMode()) return demo.listTokens();
  return apiFetch<{ tokens: AuthToken[] }>("/v1/auth/tokens/list");
}

export async function revokeToken(id: string): Promise<{ id: string; revoked: boolean }> {
  if (isDemoMode()) return demo.revokeToken();
  return apiFetch<{ id: string; revoked: boolean }>("/v1/auth/tokens/revoke", {
    method: "POST",
    body: JSON.stringify({ id }),
  });
}

// ── Consistency ─────────────────────────────────────────────────────

export async function scanConsistency(): Promise<ConsistencyScanResponse> {
  if (isDemoMode()) return demo.scanConsistency();
  return apiFetch<ConsistencyScanResponse>("/v1/context/consistency/scan");
}

export async function repairConsistency(): Promise<ConsistencyRepairResponse> {
  if (isDemoMode()) return demo.repairConsistency();
  return apiFetch<ConsistencyRepairResponse>("/v1/context/consistency/repair", {
    method: "POST",
  });
}

// ── Maintenance ─────────────────────────────────────────────────────

export async function trimRecords(req: TrimRequest): Promise<TrimResponse> {
  if (isDemoMode()) return demo.trimRecords(req.dry_run);
  return apiFetch<TrimResponse>("/v1/maintenance/trim", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function compactRecords(req: CompactRequest): Promise<CompactResponse> {
  if (isDemoMode()) return demo.compactRecords(req.dry_run);
  return apiFetch<CompactResponse>("/v1/maintenance/compact", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function cleanupExpiredTTL(): Promise<TTLCleanupResponse> {
  if (isDemoMode()) return demo.cleanupExpiredTTL();
  return apiFetch<TTLCleanupResponse>("/v1/maintenance/ttl-cleanup", {
    method: "POST",
  });
}

// ── Recall ──────────────────────────────────────────────────────────

export async function recall(params: {
  namespace: string;
  tags?: string[];
  limit?: number;
  format?: "brief" | "full";
}): Promise<RecallResponse> {
  if (isDemoMode()) return demo.recall(params);
  const q = new URLSearchParams({ namespace: params.namespace });
  if (params.tags && params.tags.length > 0) q.set("tags", params.tags.join(","));
  if (params.limit) q.set("limit", String(params.limit));
  if (params.format) q.set("format", params.format);
  return apiFetch<RecallResponse>(`/v1/recall?${q.toString()}`);
}

// ── Broker ──────────────────────────────────────────────────────────

export async function brokerPlan(req: BrokerPlanRequest): Promise<BrokerPlanResponse> {
  if (isDemoMode()) return demo.brokerPlan();
  return apiFetch<BrokerPlanResponse>("/v1/broker/plan", {
    method: "POST",
    body: JSON.stringify(req),
  });
}
