import { useCallback } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  BookOpen,
  Brain,
  Database,
  FileText,
  HeartPulse,
  Layers,
  PenTool,
  ScrollText,
  Search,
  Shield,
  Sparkles,
} from "lucide-react";
import {
  tesseractLookup,
  getAuditEvents,
  getMetrics,
  estimate,
  listNamespaces,
} from "../api/client";
import { usePoll } from "../hooks/usePoll";
import { Spinner } from "../components/ui/Spinner";
import { StatusBadge } from "../components/ui/StatusBadge";
import type {
  AuditResponse,
  EstimateResponse,
  HealthStatus,
  MetricsResponse,
  NamespaceListResponse,
} from "../api/types";
import type { NavPage } from "../components/layout/nav";

interface Props {
  health: HealthStatus | null;
  onNavigate: (
    page: NavPage,
    update?: {
      reviewPreset?: "lowConfidence" | "reviewed" | "pendingReview";
    },
  ) => void;
}

export function DashboardPage({ health, onNavigate }: Props) {
  const estimateFetcher = useCallback(() => estimate({ revision_scope: "head", limit: 1 }), []);
  const { data: estData } = usePoll<EstimateResponse>(estimateFetcher, 15_000);

  const auditFetcher = useCallback(() => getAuditEvents({ limit: 8 }), []);
  const { data: auditData, loading: auditLoading } = usePoll<AuditResponse>(auditFetcher, 10_000);

  const namespaceFetcher = useCallback(() => listNamespaces({ limit: 1000 }), []);
  const { data: namespaceData } = usePoll<NamespaceListResponse>(namespaceFetcher, 20_000);

  const metricsFetcher = useCallback(() => getMetrics(), []);
  const { data: metricsData, error: metricsError } = usePoll<MetricsResponse>(metricsFetcher, 20_000);

  const reviewCountsFetcher = useCallback(async () => {
    const namespaces = namespaceData?.items.map((item) => item.namespace) ?? [];
    if (namespaces.length === 0) {
      return { lowConfidence: 0, reviewed: 0, pendingReview: 0 };
    }
    const res = await tesseractLookup({
      namespaces,
      ranking: "activation",
      revision_scope: "current",
      statuses: ["draft", "reviewed", "canonical"],
      limit: 500,
    });
    let lowConfidence = 0;
    let reviewed = 0;
    let pendingReview = 0;
    // This call takes the server's default payload_mode. `summary` carries
    // status and confidence, but `keys` does not, so both are optional here.
    // Counting an unknown confidence as low would invent review work that
    // does not exist; a tile is better slightly under-counted than wrong.
    for (const item of res.results) {
      const { confidence, status } = item.revision;
      if (confidence !== undefined && confidence < 0.8) lowConfidence++;
      if (status === "reviewed") reviewed++;
      if (status === "draft" || status === "reviewed") pendingReview++;
    }
    return { lowConfidence, reviewed, pendingReview };
  }, [namespaceData]);
  const { data: reviewCounts } = usePoll(reviewCountsFetcher, 20_000);

  const recentEvents = auditData?.items ?? [];
  const latestEvent = recentEvents[0];
  const namespaceCount = namespaceData?.count ?? namespaceData?.items?.length ?? null;
  const totalRequests = metricsData?.totals.requests ?? null;
  const totalErrors = metricsData?.totals.errors ?? null;
  const issueCount = health?.consistency_issues ?? null;
  const metricsUnavailable =
    metricsError?.message?.includes("HTTP 404") || metricsError?.message?.toLowerCase().includes("not found");

  const primaryStatus =
    !health ? "loading" : issueCount && issueCount > 0 ? "attention needed" : health.status;

  const activitySummary = recentEvents[0]
    ? `${recentEvents[0].event_type} in ${recentEvents[0].namespace}`
    : "No recent events yet";

  return (
    <div>
      <div className="page-header" style={{ marginBottom: "0.75rem" }}>
        <h2 className="page-title">Dashboard</h2>
      </div>

      <div
        className="hud-panel"
        style={{
          padding: "1rem",
          marginBottom: "1rem",
          borderColor: "rgba(var(--primary) / 0.35)",
          background:
            "linear-gradient(135deg, rgba(var(--primary) / 0.08), rgba(var(--panel) / 0.96) 45%)",
        }}
      >
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "1.6fr 1fr",
            gap: "1rem",
            alignItems: "stretch",
          }}
        >
          <div style={{ minWidth: 0 }}>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "0.45rem",
                marginBottom: "0.45rem",
                color: "rgb(var(--primary))",
              }}
            >
              <Sparkles size={14} />
              <span
                style={{
                  fontSize: "0.72rem",
                  textTransform: "uppercase",
                  letterSpacing: "0.12em",
                }}
              >
                Ops Overview
              </span>
            </div>
            <div style={{ fontSize: "1.2rem", marginBottom: "0.4rem", color: "rgb(var(--text))" }}>
              Tesseract status: {primaryStatus}
            </div>
            <div style={{ fontSize: "0.82rem", color: "rgb(var(--muted))", lineHeight: 1.55 }}>
              Landing page for operator health, recent activity, and the fastest paths into review,
              write, search, and audit workflows.
            </div>

            <div style={{ display: "flex", gap: "0.4rem", flexWrap: "wrap", marginTop: "0.8rem" }}>
              <StatusBadge status={health?.status ?? "unknown"} />
              <StatusBadge
                status={
                  issueCount == null
                    ? "consistency unknown"
                    : issueCount === 0
                      ? "consistency ok"
                      : `${issueCount} consistency issue${issueCount === 1 ? "" : "s"}`
                }
                variant={issueCount && issueCount > 0 ? "warn" : "ok"}
              />
              <StatusBadge
                status={
                  namespaceCount == null
                    ? "namespaces —"
                    : `${namespaceCount} namespace${namespaceCount === 1 ? "" : "s"}`
                }
                variant="primary"
              />
              <StatusBadge
                status={latestEvent ? `activity ${timeAgo(latestEvent.created_at)}` : "activity idle"}
                variant={latestEvent ? "ok" : "muted"}
              />
            </div>
          </div>

          <div
            className="hud-panel2"
            style={{ padding: "0.85rem", display: "flex", flexDirection: "column", gap: "0.6rem" }}
          >
            <div>
              <div className="hud-label" style={{ marginBottom: "0.2rem" }}>
                Latest activity
              </div>
              <div style={{ fontSize: "0.84rem", color: "rgb(var(--text))" }}>{activitySummary}</div>
            </div>
            <div>
              <div className="hud-label" style={{ marginBottom: "0.2rem" }}>
                Database
              </div>
              <div
                style={{
                  fontSize: "0.74rem",
                  color: "rgb(var(--muted))",
                  fontFamily: "var(--font-mono)",
                  wordBreak: "break-all",
                }}
              >
                {health?.db_path ?? "Loading…"}
              </div>
            </div>
            <button
              type="button"
              className="hud-button-primary"
              onClick={() => onNavigate(issueCount && issueCount > 0 ? "consistency" : "audit")}
              style={{ marginTop: "auto", justifyContent: "center" }}
            >
              <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                {issueCount && issueCount > 0 ? <HeartPulse size={13} /> : <ScrollText size={13} />}
                {issueCount && issueCount > 0 ? "Review Health" : "Open Audit"}
              </span>
            </button>
          </div>
        </div>
      </div>

      <div className="stats-grid" style={{ marginBottom: "1rem" }}>
        <DashboardStat
          label="System Status"
          value={health?.status ?? "—"}
          icon={<Activity size={14} />}
          accent={health?.status === "ready" || health?.status === "ok" ? "ok" : "warn"}
          sub={health ? `schema v${health.schema_version}` : "Loading readiness"}
          onClick={() => onNavigate(issueCount && issueCount > 0 ? "consistency" : "audit")}
        />
        <DashboardStat
          label="Total Records"
          value={health?.record_count?.toLocaleString() ?? "—"}
          icon={<Database size={14} />}
          sub="All stored revisions"
          onClick={() => onNavigate("memoryKnowledgeBrowser")}
        />
        <DashboardStat
          label="Head Records"
          value={estData?.record_count?.toLocaleString() ?? "—"}
          icon={<Layers size={14} />}
          sub="Current revision scope"
          onClick={() => onNavigate("viewBuilder")}
        />
        <DashboardStat
          label="Est. Tokens"
          value={estData?.token_estimate?.toLocaleString() ?? "—"}
          icon={<FileText size={14} />}
          sub="Head-scope estimate"
          onClick={() => onNavigate("packetBuilder")}
        />
        <DashboardStat
          label="Namespaces"
          value={namespaceCount != null ? namespaceCount.toLocaleString() : "—"}
          icon={<Shield size={14} />}
          sub="Registered or observed"
          onClick={() => onNavigate("policyManager")}
        />
        <DashboardStat
          label="API Requests"
          value={
            metricsUnavailable
              ? "Off"
              : totalRequests != null
                ? totalRequests.toLocaleString()
                : "—"
          }
          icon={<Search size={14} />}
          sub={
            metricsUnavailable
              ? "Server metrics endpoint disabled"
              : totalErrors != null
                ? `${totalErrors} total error${totalErrors === 1 ? "" : "s"}`
                : "Metrics loading"
          }
          accent={metricsUnavailable ? "primary" : totalErrors && totalErrors > 0 ? "warn" : "ok"}
          onClick={() => onNavigate("audit")}
        />
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "1.2fr 0.8fr", gap: "0.9rem" }}>
        <div className="hud-panel" style={{ padding: "0.85rem" }}>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: "0.75rem",
              marginBottom: "0.7rem",
            }}
          >
            <div
              className="hud-label"
              style={{ color: "rgb(var(--primary))", display: "flex", alignItems: "center", gap: "0.35rem", marginBottom: 0 }}
            >
              <Activity size={13} /> Recent Activity
            </div>
            <span style={{ fontSize: "0.72rem", color: "rgb(var(--muted))" }}>
              Last {recentEvents.length} event{recentEvents.length === 1 ? "" : "s"}
            </span>
          </div>

          {auditLoading && !auditData && (
            <div style={{ padding: "1rem", textAlign: "center" }}>
              <Spinner size={16} />
            </div>
          )}

          {!auditLoading && recentEvents.length === 0 && (
            <div style={{ padding: "1rem", textAlign: "center", color: "rgb(var(--muted))", fontSize: "0.8rem" }}>
              No recent events
            </div>
          )}

          {recentEvents.length > 0 && (
            <div style={{ display: "flex", flexDirection: "column", gap: "0.15rem" }}>
              {recentEvents.map((evt) => (
                <div
                  key={evt.id}
                  style={{
                    display: "grid",
                    gridTemplateColumns: "auto minmax(0, 1fr) auto",
                    alignItems: "center",
                    gap: "0.75rem",
                    padding: "0.5rem 0",
                    borderBottom: "1px solid rgba(var(--border) / 0.35)",
                  }}
                >
                  <StatusBadge
                    status={evt.event_type}
                    variant={evt.event_type.includes("error") ? "danger" : evt.event_type.includes("promote") ? "primary" : "ok"}
                  />
                  <div style={{ minWidth: 0 }}>
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "space-between",
                        gap: "0.75rem",
                        marginBottom: "0.15rem",
                      }}
                    >
                      <span
                        style={{
                          fontSize: "0.82rem",
                          color: "rgb(var(--text))",
                          whiteSpace: "nowrap",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                        }}
                      >
                        {evt.key}
                      </span>
                      <span style={{ fontSize: "0.72rem", color: "rgb(var(--muted))", whiteSpace: "nowrap" }}>
                        {evt.actor}
                      </span>
                    </div>
                    <div
                      style={{
                        fontSize: "0.72rem",
                        color: "rgb(var(--muted))",
                        fontFamily: "var(--font-mono)",
                        whiteSpace: "nowrap",
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                      }}
                    >
                      {evt.namespace}
                    </div>
                  </div>
                  <div style={{ fontSize: "0.72rem", color: "rgb(var(--muted))", whiteSpace: "nowrap", textAlign: "right" }}>
                    {timeAgo(evt.created_at)}
                  </div>
                </div>
              ))}
            </div>
          )}

          <button
            className="hud-button-ghost"
            onClick={() => onNavigate("audit")}
            style={{ width: "100%", marginTop: "0.75rem", fontSize: "0.75rem" }}
          >
            View All Events
          </button>
        </div>

        <div style={{ display: "grid", gap: "0.9rem" }}>
          <div className="hud-panel" style={{ padding: "0.85rem" }}>
            <div
              className="hud-label"
              style={{ color: "rgb(var(--primary))", display: "flex", alignItems: "center", gap: "0.35rem", marginBottom: "0.65rem" }}
            >
              <ArrowRight size={13} /> Quick Actions
            </div>
            <div style={{ display: "flex", flexDirection: "column", gap: "0.45rem" }}>
              <QuickAction icon={<Brain size={14} />} label="Review Queue" sub="Triage memory needing curation" onClick={() => onNavigate("memoryReview")} />
              <QuickAction icon={<PenTool size={14} />} label="Memory Write" sub="Add or clarify memory" onClick={() => onNavigate("memoryWrite")} />
              <QuickAction icon={<BookOpen size={14} />} label="Knowledge Write" sub="Capture durable references" onClick={() => onNavigate("knowledgeWrite")} />
              <QuickAction icon={<Layers size={14} />} label="Packet Builder" sub="Assemble a bounded context payload" onClick={() => onNavigate("packetBuilder")} />
              <QuickAction icon={<Search size={14} />} label="Search & Research" sub="Ask across memory and knowledge" onClick={() => onNavigate("searchResearch")} />
              <QuickAction icon={<ScrollText size={14} />} label="Audit & Ops" sub="Inspect recent system events" onClick={() => onNavigate("audit")} />
            </div>
          </div>

          <div className="hud-panel" style={{ padding: "0.85rem" }}>
            <div
              className="hud-label"
              style={{ color: "rgb(var(--primary))", display: "flex", alignItems: "center", gap: "0.35rem", marginBottom: "0.65rem" }}
            >
              {issueCount && issueCount > 0 ? <AlertTriangle size={13} /> : <HeartPulse size={13} />}
              Attention
            </div>
            <div style={{ display: "flex", flexDirection: "column", gap: "0.55rem" }}>
              <AttentionRow
                label="Consistency"
                value={
                  issueCount == null
                    ? "Loading"
                    : issueCount === 0
                      ? "Healthy"
                      : `${issueCount} issue${issueCount === 1 ? "" : "s"} to inspect`
                }
                tone={issueCount && issueCount > 0 ? "warn" : "ok"}
              />
              <AttentionRow
                label="Latest Event"
                value={latestEvent ? `${latestEvent.event_type} ${timeAgo(latestEvent.created_at)}` : "No recent activity"}
                tone={latestEvent ? "primary" : "muted"}
              />
              <AttentionRow
                label="Metrics"
                value={
                  metricsUnavailable
                    ? "Disabled in server config"
                    : totalErrors == null
                      ? "Loading"
                      : totalErrors === 0
                        ? "No recorded API errors"
                        : `${totalErrors} API error${totalErrors === 1 ? "" : "s"} observed`
                }
                tone={
                  metricsUnavailable
                    ? "muted"
                    : totalErrors && totalErrors > 0
                      ? "warn"
                      : "ok"
                }
              />
            </div>
          </div>
        </div>
      </div>

      <div className="stats-grid" style={{ marginTop: "1rem", marginBottom: "1rem" }}>
        <DashboardStat
          label="Low Confidence"
          value={reviewCounts ? reviewCounts.lowConfidence.toLocaleString() : "—"}
          icon={<AlertTriangle size={14} />}
          accent={reviewCounts && reviewCounts.lowConfidence > 0 ? "warn" : "ok"}
          sub="Current revisions below 0.80 confidence"
          onClick={() => onNavigate("memoryReview", { reviewPreset: "lowConfidence" })}
        />
        <DashboardStat
          label="Reviewed"
          value={reviewCounts ? reviewCounts.reviewed.toLocaleString() : "—"}
          icon={<Brain size={14} />}
          accent="primary"
          sub="Not yet canonical"
          onClick={() => onNavigate("memoryReview", { reviewPreset: "reviewed" })}
        />
        <DashboardStat
          label="Pending Review"
          value={reviewCounts ? reviewCounts.pendingReview.toLocaleString() : "—"}
          icon={<ScrollText size={14} />}
          accent={reviewCounts && reviewCounts.pendingReview > 0 ? "warn" : "ok"}
          sub="Draft or reviewed current revisions"
          onClick={() => onNavigate("memoryReview", { reviewPreset: "pendingReview" })}
        />
      </div>
    </div>
  );
}

function DashboardStat({
  label,
  value,
  icon,
  sub,
  accent = "primary",
  onClick,
}: {
  label: string;
  value: string;
  icon: React.ReactNode;
  sub?: string;
  accent?: "primary" | "ok" | "warn";
  onClick?: () => void;
}) {
  const color =
    accent === "ok" ? "rgb(var(--ok))" : accent === "warn" ? "rgb(var(--warn))" : "rgb(var(--primary))";

  return (
    <button
      type="button"
      className="stat-card"
      onClick={onClick}
      style={{
        minHeight: 112,
        width: "100%",
        textAlign: "left",
        background: "rgb(var(--panel))",
        cursor: onClick ? "pointer" : "default",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: "0.75rem", marginBottom: "0.55rem" }}>
        <div className="stat-label" style={{ marginBottom: 0 }}>
          {label}
        </div>
        <span style={{ color, display: "flex", alignItems: "center" }}>{icon}</span>
      </div>
      <div className="stat-value" style={{ fontSize: "1.35rem", lineHeight: 1.15 }}>
        {value}
      </div>
      {sub && <div style={{ fontSize: "0.72rem", color: "rgb(var(--muted))", marginTop: "0.45rem", lineHeight: 1.45 }}>{sub}</div>}
    </button>
  );
}

function QuickAction({
  icon,
  label,
  sub,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  sub: string;
  onClick: () => void;
}) {
  return (
    <button
      className="hud-button-ghost"
      onClick={onClick}
      style={{
        display: "flex",
        alignItems: "center",
        gap: "0.6rem",
        padding: "0.6rem 0.65rem",
        textAlign: "left",
        width: "100%",
      }}
    >
      <span style={{ color: "rgb(var(--primary))", flexShrink: 0 }}>{icon}</span>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: "0.8rem", color: "rgb(var(--text))" }}>{label}</div>
        <div style={{ fontSize: "0.7rem", color: "rgb(var(--muted))", lineHeight: 1.45 }}>{sub}</div>
      </div>
      <ArrowRight size={13} style={{ marginLeft: "auto", color: "rgb(var(--muted))", flexShrink: 0 }} />
    </button>
  );
}

function AttentionRow({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone: "primary" | "ok" | "warn" | "muted";
}) {
  const color =
    tone === "ok"
      ? "rgb(var(--ok))"
      : tone === "warn"
        ? "rgb(var(--warn))"
        : tone === "primary"
          ? "rgb(var(--primary))"
          : "rgb(var(--muted))";

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "auto 1fr",
        gap: "0.6rem",
        alignItems: "start",
        paddingBottom: "0.55rem",
        borderBottom: "1px solid rgba(var(--border) / 0.35)",
      }}
    >
      <div className="hud-label" style={{ marginBottom: 0 }}>
        {label}
      </div>
      <div style={{ fontSize: "0.78rem", color, lineHeight: 1.45 }}>{value}</div>
    </div>
  );
}

function timeAgo(dateStr: string): string {
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diff = now - then;
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}
