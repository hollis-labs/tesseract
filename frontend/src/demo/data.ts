import type {
  AdminQueueResponse,
  AdminSetupResponse,
  AdminStorageResponse,
  AuditEvent,
  AuditResponse,
  AuthToken,
  BrokerPlanResponse,
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
  PacketResponse,
  RecallBriefItem,
  RecallResponse,
  Record,
  SynthesisAskRequest,
  SynthesisAskResponse,
  TokenCreateResponse,
  TrimResponse,
  TTLCleanupResponse,
  ViewResponse,
  WriteResponse,
} from "../api/types";

// ── Demo mode detection ──────────────────────────────────────────────

export function isDemoMode(): boolean {
  if (typeof window === "undefined") return false;
  const params = new URLSearchParams(window.location.search);
  return params.get("demo") === "1" || params.get("demo") === "true";
}

// ── Timestamps ───────────────────────────────────────────────────────

const now = new Date();
function ago(hours: number): string {
  return new Date(now.getTime() - hours * 3600_000).toISOString();
}

// ── Mock records ─────────────────────────────────────────────────────

const MOCK_RECORDS: Record[] = [
  {
    record_id: "rec_001",
    namespace: "user/memory/project-alpha",
    key: "status",
    revision: 3,
    actor: "user:jane",
    checksum: "sha256:abc123",
    created_at: ago(1),
    payload: { phase: "implementation", sprint: 4, blockers: [], confidence: 0.85 },
  },
  {
    record_id: "rec_002",
    namespace: "user/memory/project-alpha",
    key: "architecture",
    revision: 1,
    actor: "app:claude-agent",
    checksum: "sha256:def456",
    created_at: ago(12),
    payload: { stack: "Go + React", db: "SQLite", embedding: "go:embed", api_style: "REST" },
  },
  {
    record_id: "rec_003",
    namespace: "user/memory/preferences",
    key: "editor",
    revision: 2,
    actor: "user:jane",
    checksum: "sha256:ghi789",
    created_at: ago(48),
    payload: { theme: "dark", font: "JetBrains Mono", tabSize: 2, formatOnSave: true },
  },
  {
    record_id: "rec_004",
    namespace: "app/test/session/sess-001",
    key: "context",
    revision: 5,
    actor: "app:test-runner",
    checksum: "sha256:jkl012",
    created_at: ago(0.5),
    payload: { tests_run: 47, passed: 45, failed: 2, duration_ms: 3420 },
  },
  {
    record_id: "rec_005",
    namespace: "app/test/session/sess-001",
    key: "coverage",
    revision: 1,
    actor: "app:test-runner",
    checksum: "sha256:mno345",
    created_at: ago(0.5),
    payload: { lines: 78.2, branches: 65.1, functions: 82.0 },
  },
  {
    record_id: "rec_006",
    namespace: "user/pins",
    key: "active-project",
    revision: 1,
    actor: "user:jane",
    checksum: "sha256:pqr678",
    created_at: ago(24),
    payload: { project: "tesseract", branch: "feature/gui-sprint" },
  },
  {
    record_id: "rec_007",
    namespace: "user/memory/debugging",
    key: "known-issues",
    revision: 4,
    actor: "app:claude-agent",
    checksum: "sha256:stu901",
    created_at: ago(6),
    payload: {
      issues: ["SQLite WAL mode on NFS", "Token expiry race condition"],
      resolved: ["CORS headers"],
    },
  },
  {
    record_id: "rec_008",
    namespace: "app/prod/tesseract",
    key: "config",
    revision: 2,
    actor: "system:deploy",
    checksum: "sha256:vwx234",
    created_at: ago(72),
    payload: { port: 8080, metrics: true, auth_mode: "token", max_payload_kb: 512 },
  },
];

const DEMO_NAMESPACES: { namespace: string; owner_type: string; owner_id: string }[] = [
  { namespace: "user/jane/memory", owner_type: "user", owner_id: "jane" },
  { namespace: "user/jane/cache", owner_type: "user", owner_id: "jane" },
  { namespace: "user/jane/pins", owner_type: "user", owner_id: "jane" },
  { namespace: "user/jane/knowledge/projects", owner_type: "user", owner_id: "jane" },
  { namespace: "user/jane/knowledge/portfolio", owner_type: "user", owner_id: "jane" },
  { namespace: "app/editor/session", owner_type: "app", owner_id: "editor" },
  { namespace: "app/test/session/sess-001", owner_type: "app", owner_id: "test" },
  { namespace: "app/prod/tesseract", owner_type: "app", owner_id: "deploy" },
];

// ── Mock audit events ────────────────────────────────────────────────

const MOCK_AUDIT: AuditEvent[] = [
  {
    id: 101,
    event_type: "write",
    actor: "user:jane",
    namespace: "user/memory/project-alpha",
    key: "status",
    revision: 3,
    record_id: "rec_001",
    created_at: ago(1),
  },
  {
    id: 100,
    event_type: "write",
    actor: "app:test-runner",
    namespace: "app/test/session/sess-001",
    key: "context",
    revision: 5,
    record_id: "rec_004",
    created_at: ago(0.5),
  },
  {
    id: 99,
    event_type: "promote.apply",
    actor: "user:jane",
    namespace: "user/memory/debugging",
    key: "known-issues",
    revision: 4,
    record_id: "rec_007",
    created_at: ago(6),
  },
  {
    id: 98,
    event_type: "promote.approve",
    actor: "user:jane",
    namespace: "user/memory/debugging",
    key: "known-issues",
    revision: 3,
    record_id: "rec_007",
    created_at: ago(6.5),
  },
  {
    id: 97,
    event_type: "promote.request",
    actor: "app:claude-agent",
    namespace: "app/test/session/sess-001",
    key: "known-issues",
    revision: 3,
    record_id: "rec_007",
    created_at: ago(7),
  },
  {
    id: 96,
    event_type: "write",
    actor: "user:jane",
    namespace: "user/memory/preferences",
    key: "editor",
    revision: 2,
    record_id: "rec_003",
    created_at: ago(48),
  },
  {
    id: 95,
    event_type: "write",
    actor: "system:deploy",
    namespace: "app/prod/tesseract",
    key: "config",
    revision: 2,
    record_id: "rec_008",
    created_at: ago(72),
  },
];

// ── Mock tokens ──────────────────────────────────────────────────────

const MOCK_TOKENS: AuthToken[] = [
  {
    id: "tok_001",
    name: "claude-agent-token",
    client_id: "app:claude-agent",
    scopes: ["read", "write", "promote.request"],
    namespace_globs: ["app/*", "user/memory/*"],
    created_at: ago(168),
    expires_at: ago(-720),
    revoked: false,
  },
  {
    id: "tok_002",
    name: "test-runner",
    client_id: "app:test-runner",
    scopes: ["read", "write"],
    namespace_globs: ["app/test/*"],
    created_at: ago(336),
    expires_at: ago(-360),
    revoked: false,
  },
  {
    id: "tok_003",
    name: "old-deploy-key",
    client_id: "system:deploy",
    scopes: ["read", "write", "admin"],
    namespace_globs: ["*"],
    created_at: ago(720),
    expires_at: ago(24),
    revoked: true,
  },
];

// ── Demo API implementations ─────────────────────────────────────────

export const demo = {
  getHealth(): HealthStatus {
    return {
      healthy: true,
      status: "healthy",
      db_path: "/tmp/tesseract-demo.db",
      records_dir: "/tmp/tesseract-demo-records",
      records_dir_exists: true,
      schema_version: 4,
      record_count: MOCK_RECORDS.length,
      consistency_issues: 0,
      generated_at: now.toISOString(),
    };
  },

  getAdminSetup(): AdminSetupResponse {
    return {
      app: "tesseract",
      paths: [
        { label: "data", path: "/demo/data", exists: true, kind: "dir", writable: true },
        { label: "state", path: "/demo/state", exists: true, kind: "dir", writable: true },
        { label: "cache", path: "/demo/cache", exists: true, kind: "dir", writable: true },
        { label: "config", path: "/demo/config", exists: true, kind: "dir", writable: true },
        {
          label: "config-file",
          path: "/demo/config/config.yaml",
          exists: false,
          kind: "missing",
          writable: true,
        },
        {
          label: "main-db",
          path: "/demo/state/context.db",
          exists: true,
          kind: "file",
          writable: true,
        },
        {
          label: "records",
          path: "/demo/state/records",
          exists: true,
          kind: "dir",
          writable: true,
        },
        {
          label: "queue-db",
          path: "/demo/state/queue.db",
          exists: true,
          kind: "file",
          writable: true,
        },
      ],
      auth: { mode: "open" },
      runtime: {
        metrics_enabled: true,
        request_logging_enabled: false,
        request_log_mode: "redacted",
        memory_store_enabled: true,
        knowledge_store_enabled: true,
        synthesis_enabled: false,
      },
      config: {
        embedding_provider: "openai",
        embedding_model: "text-embedding-3-large",
        dedup_similarity_threshold: 0.85,
        synthesis_provider: "",
        synthesis_model: "",
        synthesis_max_tokens: 0,
        synthesis_temperature: 0,
        synthesis_system_prompt_set: false,
      },
    };
  },

  getAdminQueue(): AdminQueueResponse {
    return {
      enabled: true,
      queue: "tesseract",
      path: "/demo/state/queue.db",
      worker: {
        configured: true,
        concurrency: 1,
        max_tries: 3,
        retry_after: "30s",
        poll_interval: "3s",
      },
      total: 3,
      available: 1,
      delayed: 1,
      reserved: 1,
      failed: 0,
      oldest_created_at: new Date(now.getTime() - 10 * 60 * 1000).toISOString(),
      next_available_at: new Date(now.getTime() + 2 * 60 * 1000).toISOString(),
      active_by_type: [{ type: "embed", count: 3 }],
      generated_at: now.toISOString(),
    };
  },

  getAdminStorage(): AdminStorageResponse {
    return {
      generated_at: now.toISOString(),
      total_bytes: 4_718_592,
      paths: [
        {
          label: "main-db",
          path: "/demo/state/context.db",
          exists: true,
          kind: "file",
          bytes: 2_097_152,
        },
        {
          label: "records",
          path: "/demo/state/records",
          exists: true,
          kind: "dir",
          bytes: 1_572_864,
        },
        {
          label: "queue-db",
          path: "/demo/state/queue.db",
          exists: true,
          kind: "file",
          bytes: 1_048_576,
        },
      ],
      records: {
        revisions: MOCK_RECORDS.length,
        heads: MOCK_RECORDS.length,
        expired: 0,
        oldest_created_at: MOCK_RECORDS[MOCK_RECORDS.length - 1]?.created_at ?? now.toISOString(),
        newest_created_at: MOCK_RECORDS[0]?.created_at ?? now.toISOString(),
      },
      namespace_policy: {
        namespaces: DEMO_NAMESPACES.length,
        with_retention: 2,
        with_max_revisions: 2,
        with_max_bytes_per_key: 0,
        without_policy_limits: Math.max(0, DEMO_NAMESPACES.length - 2),
      },
      top_namespaces: DEMO_NAMESPACES.slice(0, 5).map((item, index) => ({
        namespace: item.namespace,
        revisions: 8 - index,
        keys: 3,
        oldest_created_at: new Date(now.getTime() - (index + 2) * 60 * 60 * 1000).toISOString(),
        newest_created_at: new Date(now.getTime() - index * 10 * 60 * 1000).toISOString(),
      })),
    };
  },

  evaluateView(namespaces?: string[]): ViewResponse {
    let items = MOCK_RECORDS;
    if (namespaces && namespaces.length > 0) {
      items = items.filter((r) =>
        namespaces.some((ns) => {
          if (ns.endsWith("*")) return r.namespace.startsWith(ns.slice(0, -1));
          return r.namespace === ns;
        }),
      );
    }
    return {
      items,
      evaluation_meta: {
        sort_keys: ["namespace", "key"],
        matched_count: items.length,
        truncated: false,
        normalized_scope: "head",
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
    const rec = MOCK_RECORDS.find((r) => r.namespace === namespace && r.key === key);
    if (!rec) throw new Error(`Record not found: ${namespace}/${key}`);
    return { record: rec };
  },

  getHistory(namespace: string, key: string): { items: Record[]; next_cursor: null } {
    const rec = MOCK_RECORDS.find((r) => r.namespace === namespace && r.key === key);
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
        {
          method: "POST",
          path: "/v1/views/evaluate",
          requests: 142,
          errors: 1,
          latency_ns_avg: 3_200_000,
          status_counts: { "200": 141, "400": 1 },
        },
        {
          method: "GET",
          path: "/v1/context/head",
          requests: 89,
          errors: 0,
          latency_ns_avg: 1_100_000,
          status_counts: { "200": 89 },
        },
        {
          method: "POST",
          path: "/v1/context/write",
          requests: 34,
          errors: 2,
          latency_ns_avg: 5_800_000,
          status_counts: { "200": 32, "400": 2 },
        },
      ],
      totals: { requests: 265, errors: 3 },
    };
  },

  listTokens(): { tokens: AuthToken[] } {
    return { tokens: MOCK_TOKENS };
  },

  createToken(): TokenCreateResponse {
    return {
      token: "ctx_demo_" + Math.random().toString(36).slice(2, 18),
      id: "tok_demo_" + Date.now(),
      name: "demo-token",
      client_id: "demo:user",
      scopes: ["read", "write"],
      namespace_globs: ["*"],
      created_at: new Date().toISOString(),
      expires_at: ago(-720),
    };
  },

  getNamespacePolicy(namespace: string): NamespacePolicy {
    return {
      namespace,
      owner_type: "user",
      owner_id: "jane",
      policy: {
        tier: "standard",
        retention: "720h",
        max_revisions: 100,
        max_bytes_per_key: 1048576,
        allowed_ops: ["read", "write", "promote"],
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
    return { trimmed: dryRun ? 12 : 12, namespace_pattern: "*", duration_ms: 45, dry_run: dryRun };
  },

  compactRecords(dryRun: boolean): CompactResponse {
    return { compacted: dryRun ? 8 : 8, namespace_pattern: "*", duration_ms: 32, dry_run: dryRun };
  },

  cleanupExpiredTTL(): TTLCleanupResponse {
    return { cleaned: 3 };
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
      selector: { namespaces: ["user/memory/*"], revision_scope: "head", limit: 50 },
      assembly: { include_pins: true, max_tokens_estimate: 8000, payload_mode: "full" },
      rationale: "Resuming project-alpha: loading user memory and pin context for continuity.",
      warnings: [],
    };
  },

  recall(params: {
    namespace: string;
    tags?: string[];
    limit?: number;
    format?: "brief" | "full";
  }): RecallResponse {
    const matches = MOCK_RECORDS.filter((r) => {
      if (params.namespace.endsWith("*"))
        return r.namespace.startsWith(params.namespace.slice(0, -1));
      return r.namespace === params.namespace;
    });
    const limit = params.limit ?? 15;
    const limited = matches.slice(0, limit);
    const brief: RecallBriefItem[] = limited.map((r, i) => ({
      revision_id: `01KPRRZ332MDP106D0F8H057${i.toString(16).padStart(2, "0").toUpperCase()}`,
      memory_id: r.record_id,
      domain: r.namespace.includes("/knowledge") ? "knowledge" : "memory",
      namespace: r.namespace,
      memory_key: r.key,
      tags: ["demo", r.actor.split(":")[0] ?? "unknown"],
      confidence: 0.7 + 0.3 * (1 - i / Math.max(limited.length, 1)),
      summary:
        typeof r.payload === "object" && r.payload !== null
          ? `Demo record ${r.key} (rev ${r.revision})`
          : String(r.payload),
      created_at: r.created_at,
    }));
    const facets: { domains: { [k: string]: number } } = { domains: {} };
    for (const item of brief) {
      facets.domains[item.domain] = (facets.domains[item.domain] ?? 0) + 1;
    }
    return {
      results: brief,
      facets,
      meta: {
        namespace: params.namespace,
        limit,
        returned: brief.length,
        format: params.format ?? "brief",
      },
    };
  },

  listNamespaces(prefix?: string): NamespaceListResponse {
    const filtered = prefix
      ? DEMO_NAMESPACES.filter((n) => n.namespace.startsWith(prefix))
      : DEMO_NAMESPACES;
    return {
      items: filtered.map((n) => ({ ...n, updated_at: ago(48) })),
      count: filtered.length,
      truncated: false,
    };
  },

  getMemoryCurrent(namespace: string, memoryKey: string): MemoryRevision {
    return {
      revision_id: "01KPRRZ332MDP106D0F8H057ZQ",
      memory_id: "01KPRRZ331PJYGHSWX5FCB65XM",
      domain: "memory",
      namespace,
      memory_key: memoryKey,
      status: "canonical",
      created_at: ago(24),
      author: { agent_id: "demo-agent", agent_version: "0.1.0" },
      trigger: "explicit",
      session_id: "session-demo",
      origin: "project",
      confidence: 0.92,
      tags: ["demo", "memory", memoryKey.split("_")[0] ?? "general"],
      payload: {
        summary: `Demo memory revision for ${memoryKey}.`,
        body: `Body content for the demo revision keyed at \`${memoryKey}\` in namespace \`${namespace}\`. In production this would carry the canonical body of the memory.`,
      },
    };
  },

  getMemoryHistory(namespace: string, memoryKey: string): MemoryRevision[] {
    return [3, 2, 1].map((n, i) => ({
      revision_id: `01KPRRZ332MDP106D0F8H057Z${n}`,
      memory_id: "01KPRRZ331PJYGHSWX5FCB65XM",
      domain: "memory",
      namespace,
      memory_key: memoryKey,
      status: i === 0 ? "canonical" : "deprecated",
      created_at: ago(i * 24 + 1),
      author: { agent_id: "demo-agent", agent_version: "0.1.0" },
      confidence: 0.95 - i * 0.05,
      tags: ["demo"],
      payload: { summary: `Revision ${n} of ${memoryKey}` },
    }));
  },

  getKnowledgeCurrent(namespace: string, memoryKey: string): KnowledgeRevision {
    const rev = this.getMemoryCurrent(namespace, memoryKey);
    return { ...rev, domain: "knowledge", facets: { kind: "doc", source: "filesystem" } };
  },

  getKnowledgeHistory(namespace: string, memoryKey: string): KnowledgeRevision[] {
    return this.getMemoryHistory(namespace, memoryKey).map((r) => ({
      ...r,
      domain: "knowledge",
      facets: { kind: "doc", source: "filesystem" },
    }));
  },

  knowledgeWrite(req: KnowledgeWriteRequest): KnowledgeRevision {
    const payload: KnowledgeRevision["payload"] = { summary: req.summary };
    if (req.body) payload.body = req.body;
    return {
      revision_id: `01DEMO_K${Date.now()}`,
      memory_id: `01DEMOKNOW${Date.now()}`,
      domain: "knowledge",
      namespace: req.namespace,
      ...(req.key ? { memory_key: req.key } : {}),
      status: "canonical",
      created_at: new Date().toISOString(),
      author: req.author,
      confidence: req.confidence ?? 0.9,
      tags: req.tags ?? [],
      payload,
      facets: {
        kind: req.kind,
        source: req.source,
        pointer: req.pointer,
      },
    };
  },

  memoryWrite(req: MemoryWriteRequest): MemoryRevision {
    return {
      revision_id: `01DEMO_W${Date.now()}`,
      memory_id: `01DEMOMEM${Date.now()}`,
      domain: "memory",
      namespace: req.namespace,
      memory_key: req.memory_key ?? "",
      status: req.status ?? "canonical",
      created_at: new Date().toISOString(),
      author: req.author,
      confidence: req.confidence ?? 0.9,
      tags: req.tags ?? [],
      payload: req.payload,
    };
  },

  memoryPromote(req: MemoryPromoteRequest): MemoryRevision {
    return {
      revision_id: `01DEMO_P${Date.now()}`,
      memory_id: req.source_memory_id,
      domain: "memory",
      namespace: req.target_namespace,
      memory_key: "promoted_demo",
      status: "canonical",
      created_at: new Date().toISOString(),
      author: { agent_id: req.actor_agent_id, agent_version: req.actor_version ?? "" },
      confidence: 0.95,
      tags: ["demo", "promoted"],
      payload: { summary: `Promoted from ${req.source_namespace}` },
    };
  },

  memoryDeprecate(req: MemoryDeprecateRequest): MemoryDeprecateResponse {
    return { status: "deprecated", revision_id: req.revision_id };
  },

  synthesisAsk(req: SynthesisAskRequest): SynthesisAskResponse {
    return {
      answer: `Demo synthesis for: "${req.question}". This is a stub answer that would normally cite [1] and [2] from the sources.`,
      sources: MOCK_RECORDS.slice(0, 2).map((r, i) => ({
        n: i + 1,
        revision_id: `01DEMOSYN${i}`,
        memory_id: r.record_id,
        domain: "memory" as const,
        namespace: r.namespace,
        memory_key: r.key,
        summary: `Demo source ${i + 1} for synthesis`,
        confidence: 0.9 - i * 0.05,
        score: 0.9 - i * 0.05,
      })),
      usage: {
        provider: "demo",
        model: "demo-model-v1",
        input_tokens: 256,
        output_tokens: 64,
        latency_ms: 850,
        cost: { input_usd: 0.001, output_usd: 0.0008, total_usd: 0.0018 },
      },
    };
  },

  tesseractLookup(req: TesseractLookupRequest): TesseractLookupResponse {
    const limit = req.limit ?? 15;
    const items = MOCK_RECORDS.slice(0, limit).map((r, i) => ({
      Revision: {
        revision_id: `01DEMO${i.toString().padStart(2, "0")}`,
        memory_id: r.record_id,
        domain: r.namespace.includes("/knowledge") ? ("knowledge" as const) : ("memory" as const),
        namespace: r.namespace,
        memory_key: r.key,
        status: "canonical" as const,
        created_at: r.created_at,
        author: { agent_id: r.actor.replace(":", "_") },
        confidence: 0.9 - i * 0.05,
        tags: ["demo", req.query ?? "lookup"],
        payload: {
          summary: `Demo lookup hit ${i + 1} for "${req.query ?? "(no query)"}"`,
          body:
            typeof r.payload === "object" ? JSON.stringify(r.payload, null, 2) : String(r.payload),
        },
      },
      Score: 0.9 - i * 0.05,
    }));
    const facets: { domains: { [k: string]: number } } = { domains: {} };
    for (const it of items) {
      facets.domains[it.Revision.domain] = (facets.domains[it.Revision.domain] ?? 0) + 1;
    }
    return { facets, results: items };
  },

  promoteRequest(): unknown {
    return { status: "pending", request_id: "promo_demo_" + Date.now() };
  },
  promoteApprove(): unknown {
    return { status: "approved" };
  },
  promoteApply(): unknown {
    return { status: "applied" };
  },
  registerNamespace(namespace: string): NamespacePolicy {
    return this.getNamespacePolicy(namespace);
  },
  revokeToken(): { id: string; revoked: boolean } {
    return { id: "tok_demo", revoked: true };
  },
};
