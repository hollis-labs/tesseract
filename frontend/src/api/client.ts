import { demo, isDemoMode } from "../demo/data";
import type {
  AdminQueueResponse,
  AdminSetupResponse,
  AdminStorageResponse,
  AuditResponse,
  AuthToken,
  BrokerPlanRequest,
  BrokerPlanResponse,
  CompactRequest,
  CompactResponse,
  ConduitLookupRequest,
  ConduitLookupResponse,
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

export async function getAdminQueue(): Promise<AdminQueueResponse> {
  if (isDemoMode()) return demo.getAdminQueue();
  return apiFetch<AdminQueueResponse>("/v1/admin/queue");
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

// ── Conduit lookup (unified search) ─────────────────────────────────

export async function conduitLookup(req: ConduitLookupRequest): Promise<ConduitLookupResponse> {
  if (isDemoMode()) return demo.conduitLookup(req);
  return apiFetch<ConduitLookupResponse>("/v1/conduit/lookup", {
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
