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
  revision_scope?: 'head' | 'all';
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
  payload_mode?: 'full' | 'head_only';
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
