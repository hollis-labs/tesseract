// ── Core record types ───────────────────────────────────────────────

export interface Record {
  record_id: string;
  namespace: string;
  key: string;
  revision: number;
  actor: string;
  payload: unknown;
  metadata?: unknown;
  checksum: string;
  created_at: string;
}

// ── Selector / View types ───────────────────────────────────────────

export interface Selector {
  namespaces?: string[];
  keys?: string[];
  revision_scope?: "head" | "all";
  order?: string[];
  limit?: number;
  tags_any?: string[];
}

export interface EvaluationMeta {
  sort_keys: string[];
  matched_count: number;
  truncated: boolean;
  normalized_scope: string;
}

export interface ViewResponse {
  items: Record[];
  evaluation_meta: EvaluationMeta;
}

// ── Packet types ────────────────────────────────────────────────────

export interface PacketManifest {
  pins_included: number;
  items_total: number;
  bytes: number;
  tokens_estimate: number;
  truncated: boolean;
}

export interface PacketRequest {
  selector?: Selector;
  include_pins?: boolean;
  time_window?: string;
  max_items?: number;
  max_bytes?: number;
  max_tokens_estimate?: number;
  payload_mode?: "full" | "head_only";
}

export interface PacketResponse {
  items: Record[];
  manifest: PacketManifest;
}

// ── Estimate types ──────────────────────────────────────────────────

export interface EstimateRequest {
  selector: Selector;
}

export interface EstimateResponse {
  record_count: number;
  total_bytes: number;
  token_estimate: number;
}

// ── Audit types ─────────────────────────────────────────────────────

export interface AuditEvent {
  id: number;
  event_type: string;
  actor: string;
  namespace: string;
  key: string;
  revision: number;
  record_id: string;
  metadata?: unknown;
  created_at: string;
}

export interface AuditResponse {
  items: AuditEvent[];
  count: number;
  next_cursor: number | null;
}

// ── Health types ────────────────────────────────────────────────────

export interface HealthStatus {
  status: string;
  db_path: string;
  schema_version: number;
  record_count: number;
  consistency_issues: number;
}

// ── Promote types ───────────────────────────────────────────────────

export interface PromoteRequestPayload {
  actor: string;
  source_namespace: string;
  source_key: string;
  target_namespace: string;
  target_key: string;
  reason?: string;
  source_revision?: number;
}

export interface PromoteApprovePayload {
  request_id: string;
  actor: string;
}

export interface PromoteApplyPayload {
  request_id: string;
  actor: string;
}

// ── Auth Token types ────────────────────────────────────────────────

export interface AuthToken {
  id: string;
  name: string;
  client_id: string;
  scopes: string[];
  namespace_globs: string[];
  created_at: string;
  expires_at: string;
  revoked: boolean;
}

export interface TokenCreateRequest {
  name: string;
  client_id: string;
  scopes: string[];
  namespace_globs: string[];
  ttl?: string;
  expires_at?: string;
}

export interface TokenCreateResponse {
  token: string;
  id: string;
  name: string;
  client_id: string;
  scopes: string[];
  namespace_globs: string[];
  created_at: string;
  expires_at: string;
}

// ── Namespace / Policy types ────────────────────────────────────────

export interface NamespacePolicy {
  namespace: string;
  owner_type: string;
  owner_id: string;
  policy: {
    tier?: string;
    retention?: string;
    max_revisions?: number;
    max_bytes_per_key?: number;
    allowed_ops?: string[];
    [key: string]: unknown;
  };
}

// ── Write types ─────────────────────────────────────────────────────

export interface WriteRequest {
  actor: string;
  namespace: string;
  key: string;
  payload: unknown;
  metadata?: unknown;
  reason?: string;
}

export interface WriteResponse {
  record_id: string;
  revision: number;
  head_revision: number;
  timestamp: string;
}

// ── Consistency types ───────────────────────────────────────────────

export interface ConsistencyIssue {
  type: string;
  namespace: string;
  key: string;
  details: string;
}

export interface ConsistencyScanResponse {
  issues: ConsistencyIssue[];
  count: number;
}

export interface ConsistencyRepairResponse {
  rebuilt_heads: number;
  remaining_issues: number;
  issues: ConsistencyIssue[];
}

// ── Maintenance types ───────────────────────────────────────────────

export interface TrimRequest {
  namespace_pattern: string;
  retention: string;
  dry_run: boolean;
}

export interface TrimResponse {
  trimmed: number;
  namespace_pattern: string;
  duration_ms: number;
  dry_run: boolean;
}

export interface CompactRequest {
  namespace_pattern: string;
  max_revisions: number;
  dry_run: boolean;
}

export interface CompactResponse {
  compacted: number;
  namespace_pattern: string;
  duration_ms: number;
  dry_run: boolean;
}

// ── Metrics types ───────────────────────────────────────────────────

export interface RouteMetric {
  method: string;
  path: string;
  requests: number;
  errors: number;
  latency_ns_avg: number;
  status_counts: { [status: string]: number };
}

export interface MetricsResponse {
  enabled: boolean;
  routes: RouteMetric[];
  totals: {
    requests: number;
    errors: number;
  };
}

// ── Recall types ────────────────────────────────────────────────────

export interface RecallBriefItem {
  revision_id: string;
  memory_id: string;
  domain: string;
  namespace: string;
  memory_key?: string;
  tags: string[];
  confidence: number;
  summary: string;
  created_at: string;
}

export interface LookupFacets {
  domains?: { [k: string]: number };
  kinds?: { [k: string]: number };
  sources?: { [k: string]: number };
}

export interface RecallMeta {
  namespace: string;
  limit: number;
  returned: number;
  format: "brief" | "full";
}

export interface RecallResponse {
  // brief = RecallBriefItem[], full = opaque RecallResult[] passed through.
  results: RecallBriefItem[] | unknown[];
  facets: LookupFacets;
  meta: RecallMeta;
}

// ── Memory / Knowledge revision types ───────────────────────────────

export type MemoryStatus = "draft" | "reviewed" | "canonical" | "deprecated";

export interface MemoryAuthor {
  agent_id: string;
  agent_version?: string;
}

export interface MemoryPayload {
  summary: string;
  body?: string;
}

export interface MemoryFacets {
  kind?: string;
  source?: string;
  pointer?: { scheme: string; locator: string; resolved_at?: string };
  [k: string]: unknown;
}

// MemoryRevision and KnowledgeRevision share the same on-wire shape; the
// `domain` discriminator says which store the revision lives in.
export interface MemoryRevision {
  revision_id: string;
  memory_id: string;
  domain: "memory" | "knowledge";
  namespace: string;
  memory_key?: string;
  status: MemoryStatus;
  supersedes?: string;
  created_at: string;
  author: MemoryAuthor;
  trigger?: string;
  session_id?: string;
  origin?: string;
  confidence: number;
  tags: string[];
  ttl_seconds?: number;
  expires_at?: string;
  payload: MemoryPayload;
  facets?: MemoryFacets;
  embedding_model?: string;
  dedup_match?: string;
}

export type KnowledgeRevision = MemoryRevision;

// ── Memory write / promote / deprecate request types ────────────────

export interface MemoryWriteRequest {
  namespace: string;
  memory_key?: string;
  supersedes?: string;
  status?: MemoryStatus;
  author: MemoryAuthor;
  trigger?: string;
  session_id?: string;
  origin?: string;
  confidence?: number;
  tags?: string[];
  ttl_seconds?: number;
  payload: MemoryPayload;
  facets?: MemoryFacets;
  dedup?: string;
  dedup_threshold?: number;
}

export interface MemoryPromoteRequest {
  source_namespace: string;
  source_memory_id: string;
  target_namespace: string;
  actor_agent_id: string;
  actor_version?: string;
}

export interface MemoryDeprecateRequest {
  revision_id: string;
}

export interface MemoryDeprecateResponse {
  status: string;
  revision_id: string;
}

// ── Namespaces list types ───────────────────────────────────────────

export interface NamespaceListItem {
  namespace: string;
  owner_type: string;
  owner_id: string;
  policy?: { [k: string]: unknown };
  updated_at?: string;
}

export interface NamespaceListResponse {
  items: NamespaceListItem[];
  count: number;
  truncated: boolean;
}

// ── Conduit lookup (unified search) types ───────────────────────────

export interface ConduitLookupRequest {
  namespaces: string[];
  query?: string;
  ranking?: "activation" | "chronological" | "similarity" | "relevance";
  limit?: number;
  domains?: ("memory" | "knowledge")[];
  facet_kinds?: string[];
  facet_sources?: string[];
  origins?: string[];
  statuses?: MemoryStatus[];
  tags?: string[];
  confidence_min?: number;
  since?: string;
  until?: string;
}

export interface ConduitLookupResultItem {
  Revision: MemoryRevision;
  Score?: number;
  State?: unknown;
}

export interface ConduitLookupResponse {
  facets: LookupFacets;
  results: ConduitLookupResultItem[];
}

// ── Synthesis (LLM-backed answer) types ─────────────────────────────

export interface SynthesisAskRequest {
  question: string;
  namespaces: string[];
  tags?: string[];
  limit?: number;
  domains?: ("memory" | "knowledge")[];
  statuses?: MemoryStatus[];
  confidence_min?: number;
  // Pin a specific model for one call. Omit to use the server default.
  model?: string;
}

export interface SynthesisSource {
  n: number;
  revision_id: string;
  memory_id: string;
  domain: "memory" | "knowledge";
  namespace: string;
  memory_key?: string;
  summary: string;
  confidence: number;
  score?: number;
}

export interface SynthesisCost {
  input_usd: number;
  output_usd: number;
  total_usd: number;
}

export interface SynthesisUsage {
  provider: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  latency_ms: number;
  cost?: SynthesisCost;
}

export interface SynthesisAskResponse {
  answer: string;
  sources: SynthesisSource[];
  usage: SynthesisUsage;
}

// ── Broker types ────────────────────────────────────────────────────

export interface BrokerPlanRequest {
  intent: string;
  task_summary?: string;
  namespace_constraints?: string[];
  budget?: {
    max_items?: number;
    max_bytes?: number;
    max_tokens_estimate?: number;
  };
}

export interface BrokerPlanResponse {
  selector: Selector;
  assembly: PacketRequest;
  rationale: string;
  warnings: string[];
}
