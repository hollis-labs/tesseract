import type {
  Record,
  HealthStatus,
  ViewResponse,
  EstimateResponse,
  AuditEvent,
  AuditResponse,
  AuthToken,
  TokenCreateResponse,
  NamespacePolicy,
  WriteResponse,
  PacketResponse,
  ConsistencyScanResponse,
  ConsistencyRepairResponse,
  TrimResponse,
  CompactResponse,
  MetricsResponse,
  BrokerPlanResponse,
} from '../api/types';

// ── Demo mode detection ──────────────────────────────────────────────

export function isDemoMode(): boolean {
  if (typeof window === 'undefined') return false;
  const params = new URLSearchParams(window.location.search);
  return params.get('demo') === '1' || params.get('demo') === 'true';
}

// ── Timestamps ───────────────────────────────────────────────────────

const now = new Date();
function ago(hours: number): string {
  return new Date(now.getTime() - hours * 3600_000).toISOString();
}

// ── Mock records ─────────────────────────────────────────────────────

const MOCK_RECORDS: Record[] = [
  {
    record_id: 'rec_001', namespace: 'user/memory/project-alpha', key: 'status',
    revision: 3, actor: 'user:jane', checksum: 'sha256:abc123',
    created_at: ago(1),
    payload: { phase: 'implementation', sprint: 4, blockers: [], confidence: 0.85 },
  },
  {
    record_id: 'rec_002', namespace: 'user/memory/project-alpha', key: 'architecture',
    revision: 1, actor: 'app:claude-agent', checksum: 'sha256:def456',
    created_at: ago(12),
    payload: { stack: 'Go + React', db: 'SQLite', embedding: 'go:embed', api_style: 'REST' },
  },
  {
    record_id: 'rec_003', namespace: 'user/memory/preferences', key: 'editor',
    revision: 2, actor: 'user:jane', checksum: 'sha256:ghi789',
    created_at: ago(48),
    payload: { theme: 'dark', font: 'JetBrains Mono', tabSize: 2, formatOnSave: true },
  },
  {
    record_id: 'rec_004', namespace: 'app/test/session/sess-001', key: 'context',
    revision: 5, actor: 'app:test-runner', checksum: 'sha256:jkl012',
    created_at: ago(0.5),
    payload: { tests_run: 47, passed: 45, failed: 2, duration_ms: 3420 },
  },
  {
    record_id: 'rec_005', namespace: 'app/test/session/sess-001', key: 'coverage',
    revision: 1, actor: 'app:test-runner', checksum: 'sha256:mno345',
    created_at: ago(0.5),
    payload: { lines: 78.2, branches: 65.1, functions: 82.0 },
  },
  {
    record_id: 'rec_006', namespace: 'user/pins', key: 'active-project',
    revision: 1, actor: 'user:jane', checksum: 'sha256:pqr678',
    created_at: ago(24),
    payload: { project: 'conduit', branch: 'feature/gui-sprint' },
  },
  {
    record_id: 'rec_007', namespace: 'user/memory/debugging', key: 'known-issues',
    revision: 4, actor: 'app:claude-agent', checksum: 'sha256:stu901',
    created_at: ago(6),
    payload: { issues: ['SQLite WAL mode on NFS', 'Token expiry race condition'], resolved: ['CORS headers'] },
  },
  {
    record_id: 'rec_008', namespace: 'app/prod/conduit', key: 'config',
    revision: 2, actor: 'system:deploy', checksum: 'sha256:vwx234',
    created_at: ago(72),
    payload: { port: 8080, metrics: true, auth_mode: 'token', max_payload_kb: 512 },
  },
];

// ── Mock audit events ────────────────────────────────────────────────

const MOCK_AUDIT: AuditEvent[] = [
  { id: 101, event_type: 'write', actor: 'user:jane', namespace: 'user/memory/project-alpha', key: 'status', revision: 3, record_id: 'rec_001', created_at: ago(1) },
  { id: 100, event_type: 'write', actor: 'app:test-runner', namespace: 'app/test/session/sess-001', key: 'context', revision: 5, record_id: 'rec_004', created_at: ago(0.5) },
  { id: 99, event_type: 'promote.apply', actor: 'user:jane', namespace: 'user/memory/debugging', key: 'known-issues', revision: 4, record_id: 'rec_007', created_at: ago(6) },
  { id: 98, event_type: 'promote.approve', actor: 'user:jane', namespace: 'user/memory/debugging', key: 'known-issues', revision: 3, record_id: 'rec_007', created_at: ago(6.5) },
  { id: 97, event_type: 'promote.request', actor: 'app:claude-agent', namespace: 'app/test/session/sess-001', key: 'known-issues', revision: 3, record_id: 'rec_007', created_at: ago(7) },
  { id: 96, event_type: 'write', actor: 'user:jane', namespace: 'user/memory/preferences', key: 'editor', revision: 2, record_id: 'rec_003', created_at: ago(48) },
  { id: 95, event_type: 'write', actor: 'system:deploy', namespace: 'app/prod/conduit', key: 'config', revision: 2, record_id: 'rec_008', created_at: ago(72) },
];

// ── Mock tokens ──────────────────────────────────────────────────────

const MOCK_TOKENS: AuthToken[] = [
  {
    id: 'tok_001', name: 'claude-agent-token', client_id: 'app:claude-agent',
    scopes: ['read', 'write', 'promote.request'], namespace_globs: ['app/*', 'user/memory/*'],
    created_at: ago(168), expires_at: ago(-720), revoked: false,
  },
  {
    id: 'tok_002', name: 'test-runner', client_id: 'app:test-runner',
    scopes: ['read', 'write'], namespace_globs: ['app/test/*'],
    created_at: ago(336), expires_at: ago(-360), revoked: false,
  },
  {
    id: 'tok_003', name: 'old-deploy-key', client_id: 'system:deploy',
    scopes: ['read', 'write', 'admin'], namespace_globs: ['*'],
    created_at: ago(720), expires_at: ago(24), revoked: true,
  },
];

// ── Demo API implementations ─────────────────────────────────────────

export const demo = {
  getHealth(): HealthStatus {
    return {
      status: 'ok',
      db_path: '/tmp/conduit-demo.db',
      schema_version: 4,
      record_count: MOCK_RECORDS.length,
      consistency_issues: 0,
    };
  },

  evaluateView(namespaces?: string[]): ViewResponse {
    let items = MOCK_RECORDS;
    if (namespaces && namespaces.length > 0) {
      items = items.filter(r =>
        namespaces.some(ns => {
          if (ns.endsWith('*')) return r.namespace.startsWith(ns.slice(0, -1));
          return r.namespace === ns;
        }),
      );
    }
    return {
      items,
      evaluation_meta: {
        sort_keys: ['namespace', 'key'],
        matched_count: items.length,
        truncated: false,
        normalized_scope: 'head',
      },
    };
  },

  estimate(): EstimateResponse {
    return {
      record_count: MOCK_RECORDS.length,
      total_bytes: 4096,
      token_estimate: 1250,
    };
  },

  getHead(namespace: string, key: string): { record: Record } {
    const rec = MOCK_RECORDS.find(r => r.namespace === namespace && r.key === key);
    if (!rec) throw new Error(`Record not found: ${namespace}/${key}`);
    return { record: rec };
  },

  getHistory(namespace: string, key: string): { items: Record[]; next_cursor: null } {
    const rec = MOCK_RECORDS.find(r => r.namespace === namespace && r.key === key);
    if (!rec) return { items: [], next_cursor: null };
    const items: Record[] = [];
    for (let i = rec.revision; i >= 1; i--) {
      items.push({
        ...rec,
        revision: i,
        created_at: ago((rec.revision - i) * 24 + 1),
        checksum: `sha256:rev${i}_${rec.key}`,
        payload: i === rec.revision ? rec.payload : { note: `Revision ${i} (older)` },
      });
    }
    return { items, next_cursor: null };
  },

  writeRecord(): WriteResponse {
    return {
      record_id: `rec_demo_${Date.now()}`,
      revision: 1,
      head_revision: 1,
      timestamp: new Date().toISOString(),
    };
  },

  getAuditEvents(limit = 50): AuditResponse {
    const items = MOCK_AUDIT.slice(0, limit);
    return { items, count: MOCK_AUDIT.length, next_cursor: null };
  },

  getMetrics(): MetricsResponse {
    return {
      enabled: true,
      routes: [
        { method: 'POST', path: '/v1/views/evaluate', requests: 142, errors: 1, latency_ns_avg: 3_200_000, status_counts: { '200': 141, '400': 1 } },
        { method: 'GET', path: '/v1/context/head', requests: 89, errors: 0, latency_ns_avg: 1_100_000, status_counts: { '200': 89 } },
        { method: 'POST', path: '/v1/context/write', requests: 34, errors: 2, latency_ns_avg: 5_800_000, status_counts: { '200': 32, '400': 2 } },
      ],
      totals: { requests: 265, errors: 3 },
    };
  },

  listTokens(): { tokens: AuthToken[] } {
    return { tokens: MOCK_TOKENS };
  },

  createToken(): TokenCreateResponse {
    return {
      token: 'ctx_demo_' + Math.random().toString(36).slice(2, 18),
      id: 'tok_demo_' + Date.now(),
      name: 'demo-token',
      client_id: 'demo:user',
      scopes: ['read', 'write'],
      namespace_globs: ['*'],
      created_at: new Date().toISOString(),
      expires_at: ago(-720),
    };
  },

  getNamespacePolicy(namespace: string): NamespacePolicy {
    return {
      namespace,
      owner_type: 'user',
      owner_id: 'jane',
      policy: {
        tier: 'standard',
        retention: '720h',
        max_revisions: 100,
        max_bytes_per_key: 1048576,
        allowed_ops: ['read', 'write', 'promote'],
      },
    };
  },

  scanConsistency(): ConsistencyScanResponse {
    return { issues: [], count: 0 };
  },

  repairConsistency(): ConsistencyRepairResponse {
    return { rebuilt_heads: 0, remaining_issues: 0, issues: [] };
  },

  trimRecords(dryRun: boolean): TrimResponse {
    return { trimmed: dryRun ? 12 : 12, namespace_pattern: '*', duration_ms: 45, dry_run: dryRun };
  },

  compactRecords(dryRun: boolean): CompactResponse {
    return { compacted: dryRun ? 8 : 8, namespace_pattern: '*', duration_ms: 32, dry_run: dryRun };
  },

  buildPacket(): PacketResponse {
    return {
      items: MOCK_RECORDS.slice(0, 4),
      manifest: {
        pins_included: 1,
        items_total: 4,
        bytes: 2048,
        tokens_estimate: 620,
        truncated: false,
      },
    };
  },

  brokerPlan(): BrokerPlanResponse {
    return {
      selector: { namespaces: ['user/memory/*'], revision_scope: 'head', limit: 50 },
      assembly: { include_pins: true, max_tokens_estimate: 8000, payload_mode: 'full' },
      rationale: 'Resuming project-alpha: loading user memory and pin context for continuity.',
      warnings: [],
    };
  },

  promoteRequest(): unknown {
    return { status: 'pending', request_id: 'promo_demo_' + Date.now() };
  },
  promoteApprove(): unknown {
    return { status: 'approved' };
  },
  promoteApply(): unknown {
    return { status: 'applied' };
  },
  registerNamespace(namespace: string): NamespacePolicy {
    return this.getNamespacePolicy(namespace);
  },
  revokeToken(): { id: string; revoked: boolean } {
    return { id: 'tok_demo', revoked: true };
  },
};
