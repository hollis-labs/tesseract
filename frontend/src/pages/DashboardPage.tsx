import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  LiveDot,
  Metric,
  Pill,
} from "@hollis-labs/sysop-ui";
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
} from "lucide-react";
import { useCallback } from "react";
import {
  estimate,
  getAuditEvents,
  getMetrics,
  listNamespaces,
  tesseractLookup,
} from "../api/client";
import type {
  AuditResponse,
  EstimateResponse,
  HealthStatus,
  MetricsResponse,
  NamespaceListResponse,
} from "../api/types";
import type { NavPage } from "../components/layout/nav";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

interface Props {
  health: HealthStatus | null;
  onNavigate: (
    page: NavPage,
    update?: {
      reviewPreset?: "lowConfidence" | "reviewed" | "pendingReview";
    },
  ) => void;
}

type Accent = "info" | "success" | "warning";

export function DashboardPage({ health, onNavigate }: Props) {
  const estimateFetcher = useCallback(() => estimate({ revision_scope: "head", limit: 1 }), []);
  const { data: estimateData } = usePoll<EstimateResponse>(estimateFetcher, 15_000);

  const auditFetcher = useCallback(() => getAuditEvents({ limit: 8 }), []);
  const { data: auditData, loading: auditLoading } = usePoll<AuditResponse>(auditFetcher, 10_000);

  const namespaceFetcher = useCallback(() => listNamespaces({ limit: 1000 }), []);
  const { data: namespaceData } = usePoll<NamespaceListResponse>(namespaceFetcher, 20_000);

  const metricsFetcher = useCallback(() => getMetrics(), []);
  const { data: metricsData, error: metricsError } = usePoll<MetricsResponse>(
    metricsFetcher,
    20_000,
  );

  const reviewCountsFetcher = useCallback(async () => {
    const namespaces = namespaceData?.items.map((item) => item.namespace) ?? [];
    if (namespaces.length === 0) {
      return { lowConfidence: 0, reviewed: 0, pendingReview: 0 };
    }
    const response = await tesseractLookup({
      namespaces,
      ranking: "activation",
      revision_scope: "current",
      statuses: ["draft", "reviewed", "canonical"],
      limit: 500,
    });
    let lowConfidence = 0;
    let reviewed = 0;
    let pendingReview = 0;
    for (const item of response.results) {
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
    metricsError?.message?.includes("HTTP 404") ||
    metricsError?.message?.toLowerCase().includes("not found");

  const primaryStatus = !health
    ? "loading"
    : issueCount && issueCount > 0
      ? "attention needed"
      : health.status;
  const healthy =
    health?.status === "ready" || health?.status === "ok" || health?.status === "healthy";

  return (
    <div className="min-h-full bg-bg text-text">
      <section className="grid border-b border-border-strong lg:grid-cols-[minmax(0,1.5fr)_minmax(18rem,0.5fr)]">
        <div className="px-4 py-5 lg:px-6">
          <div className="flex items-center gap-2 text-sm text-text-soft">
            <LiveDot
              tone={!health ? "neutral" : healthy ? "success" : "warning"}
              pulsing={!health || !healthy}
              label={primaryStatus}
            />
            Operational state
          </div>
          <h2 className="mt-2 text-2xl font-semibold tracking-tight">
            Tesseract is {primaryStatus}
          </h2>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-text-subtle">
            Health, current storage activity, and direct paths into the operator workflows that need
            attention.
          </p>
          <div className="mt-4 flex flex-wrap gap-2">
            <Pill tone={healthy ? "success" : health ? "warning" : "neutral"} dot>
              {health?.status ?? "health unknown"}
            </Pill>
            <Pill tone={issueCount && issueCount > 0 ? "warning" : "success"}>
              {issueCount == null
                ? "consistency unknown"
                : issueCount === 0
                  ? "consistency ok"
                  : `${issueCount} consistency issue${issueCount === 1 ? "" : "s"}`}
            </Pill>
            <Pill tone="info">
              {namespaceCount == null
                ? "namespaces —"
                : `${namespaceCount} namespace${namespaceCount === 1 ? "" : "s"}`}
            </Pill>
            <Pill tone={latestEvent ? "success" : "neutral"}>
              {latestEvent ? `activity ${timeAgo(latestEvent.created_at)}` : "activity idle"}
            </Pill>
          </div>
        </div>

        <aside className="flex flex-col gap-4 border-t border-border-strong bg-panel px-4 py-5 lg:border-t-0 lg:border-l">
          <div>
            <p className="text-xs text-text-subtle">Latest activity</p>
            <p className="mt-1 text-sm text-text">
              {latestEvent
                ? `${latestEvent.event_type} in ${latestEvent.namespace}`
                : "No recent events yet"}
            </p>
          </div>
          <div>
            <p className="text-xs text-text-subtle">Database</p>
            <p className="mt-1 break-all font-mono text-xs leading-5 text-text-soft">
              {health?.db_path ?? "Loading…"}
            </p>
          </div>
          <Button
            className="mt-auto self-start"
            onClick={() => onNavigate(issueCount && issueCount > 0 ? "consistency" : "audit")}
          >
            {issueCount && issueCount > 0 ? (
              <HeartPulse aria-hidden="true" />
            ) : (
              <ScrollText aria-hidden="true" />
            )}
            {issueCount && issueCount > 0 ? "Review health" : "Open audit"}
          </Button>
        </aside>
      </section>

      <section
        className="grid border-b border-border-strong sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6"
        aria-label="System metrics"
      >
        <DashboardMetric
          label="System status"
          value={health?.status ?? "—"}
          icon={<Activity />}
          accent={healthy ? "success" : "warning"}
          hint={health ? `schema v${health.schema_version}` : "Loading readiness"}
          onClick={() => onNavigate(issueCount && issueCount > 0 ? "consistency" : "audit")}
        />
        <DashboardMetric
          label="Total records"
          value={health?.record_count?.toLocaleString() ?? "—"}
          icon={<Database />}
          hint="All stored revisions"
          onClick={() => onNavigate("memoryKnowledgeBrowser")}
        />
        <DashboardMetric
          label="Head records"
          value={estimateData?.record_count?.toLocaleString() ?? "—"}
          icon={<Layers />}
          hint="Current revision scope"
          onClick={() => onNavigate("viewBuilder")}
        />
        <DashboardMetric
          label="Estimated tokens"
          value={estimateData?.token_estimate?.toLocaleString() ?? "—"}
          icon={<FileText />}
          hint="Head-scope estimate"
          onClick={() => onNavigate("packetBuilder")}
        />
        <DashboardMetric
          label="Namespaces"
          value={namespaceCount?.toLocaleString() ?? "—"}
          icon={<Shield />}
          hint="Registered or observed"
          onClick={() => onNavigate("policyManager")}
        />
        <DashboardMetric
          label="API requests"
          value={metricsUnavailable ? "Off" : (totalRequests?.toLocaleString() ?? "—")}
          icon={<Search />}
          accent={
            metricsUnavailable ? "info" : totalErrors && totalErrors > 0 ? "warning" : "success"
          }
          hint={
            metricsUnavailable
              ? "Metrics endpoint disabled"
              : totalErrors != null
                ? `${totalErrors} total error${totalErrors === 1 ? "" : "s"}`
                : "Metrics loading"
          }
          onClick={() => onNavigate("audit")}
        />
      </section>

      <div className="grid gap-4 p-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(20rem,0.65fr)]">
        <Card size="sm">
          <CardHeader className="border-b border-border-strong">
            <CardTitle className="flex items-center gap-2">
              <Activity className="size-4 text-status-doing" aria-hidden="true" />
              Recent activity
            </CardTitle>
            <span className="font-mono text-[11px] text-text-subtle">
              {recentEvents.length} event{recentEvents.length === 1 ? "" : "s"}
            </span>
          </CardHeader>
          <CardContent className="px-0">
            {auditLoading && !auditData ? (
              <div className="flex justify-center py-8 text-text-subtle">
                <Spinner size={16} />
              </div>
            ) : null}
            {!auditLoading && recentEvents.length === 0 ? (
              <p className="py-8 text-center text-sm text-text-subtle">No recent events</p>
            ) : null}
            {recentEvents.map((event) => (
              <div
                key={event.id}
                className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 border-b border-border-soft px-3 py-2.5 last:border-b-0"
              >
                <Pill tone={eventTone(event.event_type)}>{event.event_type}</Pill>
                <div className="min-w-0">
                  <div className="flex items-center justify-between gap-3">
                    <span className="truncate font-mono text-xs text-text">{event.key}</span>
                    <span className="shrink-0 text-xs text-text-subtle">{event.actor}</span>
                  </div>
                  <p className="mt-0.5 truncate font-mono text-[11px] text-text-subtle">
                    {event.namespace}
                  </p>
                </div>
                <time className="font-mono text-[11px] text-text-subtle">
                  {timeAgo(event.created_at)}
                </time>
              </div>
            ))}
          </CardContent>
          <div className="border-t border-border-strong p-3">
            <Button
              variant="outline"
              size="sm"
              className="w-full"
              onClick={() => onNavigate("audit")}
            >
              View all events
            </Button>
          </div>
        </Card>

        <div className="grid content-start gap-4">
          <Card size="sm">
            <CardHeader className="border-b border-border-strong">
              <CardTitle>Quick actions</CardTitle>
            </CardHeader>
            <CardContent className="space-y-1 px-1">
              <QuickAction
                icon={<Brain />}
                label="Review queue"
                sub="Triage memory needing curation"
                onClick={() => onNavigate("memoryReview")}
              />
              <QuickAction
                icon={<PenTool />}
                label="Memory write"
                sub="Add or clarify memory"
                onClick={() => onNavigate("memoryWrite")}
              />
              <QuickAction
                icon={<BookOpen />}
                label="Knowledge write"
                sub="Capture durable references"
                onClick={() => onNavigate("knowledgeWrite")}
              />
              <QuickAction
                icon={<Layers />}
                label="Packet builder"
                sub="Assemble bounded context"
                onClick={() => onNavigate("packetBuilder")}
              />
              <QuickAction
                icon={<Search />}
                label="Search and research"
                sub="Ask across stored knowledge"
                onClick={() => onNavigate("searchResearch")}
              />
              <QuickAction
                icon={<ScrollText />}
                label="Audit and ops"
                sub="Inspect recent system events"
                onClick={() => onNavigate("audit")}
              />
            </CardContent>
          </Card>

          <Card size="sm">
            <CardHeader className="border-b border-border-strong">
              <CardTitle className="flex items-center gap-2">
                {issueCount && issueCount > 0 ? (
                  <AlertTriangle className="size-4 text-status-paused" aria-hidden="true" />
                ) : (
                  <HeartPulse className="size-4 text-status-done" aria-hidden="true" />
                )}
                Attention
              </CardTitle>
            </CardHeader>
            <CardContent className="divide-y divide-border-soft px-3">
              <AttentionRow
                label="Consistency"
                value={
                  issueCount == null
                    ? "Loading"
                    : issueCount === 0
                      ? "Healthy"
                      : `${issueCount} issue${issueCount === 1 ? "" : "s"} to inspect`
                }
                tone={issueCount && issueCount > 0 ? "warning" : "success"}
              />
              <AttentionRow
                label="Latest event"
                value={
                  latestEvent
                    ? `${latestEvent.event_type} ${timeAgo(latestEvent.created_at)}`
                    : "No recent activity"
                }
                tone={latestEvent ? "info" : "neutral"}
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
                    ? "neutral"
                    : totalErrors && totalErrors > 0
                      ? "warning"
                      : "success"
                }
              />
            </CardContent>
          </Card>
        </div>
      </div>

      <section
        className="grid border-y border-border-strong sm:grid-cols-3"
        aria-label="Review queue metrics"
      >
        <DashboardMetric
          label="Low confidence"
          value={reviewCounts?.lowConfidence.toLocaleString() ?? "—"}
          icon={<AlertTriangle />}
          accent={reviewCounts && reviewCounts.lowConfidence > 0 ? "warning" : "success"}
          hint="Current revisions below 0.80"
          onClick={() => onNavigate("memoryReview", { reviewPreset: "lowConfidence" })}
        />
        <DashboardMetric
          label="Reviewed"
          value={reviewCounts?.reviewed.toLocaleString() ?? "—"}
          icon={<Brain />}
          hint="Not yet canonical"
          onClick={() => onNavigate("memoryReview", { reviewPreset: "reviewed" })}
        />
        <DashboardMetric
          label="Pending review"
          value={reviewCounts?.pendingReview.toLocaleString() ?? "—"}
          icon={<ScrollText />}
          accent={reviewCounts && reviewCounts.pendingReview > 0 ? "warning" : "success"}
          hint="Draft or reviewed revisions"
          onClick={() => onNavigate("memoryReview", { reviewPreset: "pendingReview" })}
        />
      </section>
    </div>
  );
}

function DashboardMetric({
  label,
  value,
  icon,
  hint,
  accent = "info",
  onClick,
}: {
  label: string;
  value: string;
  icon: React.ReactNode;
  hint: string;
  accent?: Accent;
  onClick: () => void;
}) {
  const accentColor = {
    info: "var(--theme-color-status-doing)",
    success: "var(--theme-color-status-done)",
    warning: "var(--theme-color-status-paused)",
  }[accent];

  return (
    <Button
      type="button"
      variant="ghost"
      className="h-auto min-h-28 justify-start rounded-none border-r border-b border-border-soft px-4 py-4 text-left last:border-r-0 sm:border-b-0"
      onClick={onClick}
    >
      <span className="flex w-full items-start gap-3">
        <span className="mt-0.5 shrink-0 text-text-subtle [&_svg]:size-4" aria-hidden="true">
          {icon}
        </span>
        <Metric label={label} value={value} hint={hint} accentColor={accentColor} />
      </span>
    </Button>
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
    <Button
      variant="ghost"
      className="h-auto w-full justify-start px-2 py-2 text-left font-normal"
      onClick={onClick}
    >
      <span className="shrink-0 text-status-doing [&_svg]:size-4" aria-hidden="true">
        {icon}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-sm text-text">{label}</span>
        <span className="block text-xs text-text-subtle">{sub}</span>
      </span>
      <ArrowRight className="text-text-subtle" aria-hidden="true" />
    </Button>
  );
}

function AttentionRow({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone: "neutral" | "info" | "success" | "warning";
}) {
  return (
    <div className="grid grid-cols-[7rem_minmax(0,1fr)] gap-3 py-2.5 text-xs">
      <span className="text-text-subtle">{label}</span>
      <span
        className={
          tone === "success"
            ? "text-status-done"
            : tone === "warning"
              ? "text-status-paused"
              : tone === "info"
                ? "text-status-doing"
                : "text-text-subtle"
        }
      >
        {value}
      </span>
    </div>
  );
}

function eventTone(eventType: string): "danger" | "info" | "success" {
  if (eventType.includes("error")) return "danger";
  if (eventType.includes("promote")) return "info";
  return "success";
}

function timeAgo(date: string): string {
  const difference = Date.now() - new Date(date).getTime();
  const seconds = Math.floor(difference / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}
