import { ChevronDown, ChevronRight, FileText, RefreshCw } from "lucide-react";
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { getAuditEvents } from "../api/client";
import type { AuditEvent } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { JsonViewer } from "../components/ui/JsonViewer";
import { Spinner } from "../components/ui/Spinner";
import { StatusBadge } from "../components/ui/StatusBadge";

// Canonical event-type identifiers grouped for the filter dropdown. Mirrors
// internal/contextstore/audittypes.go; keep in sync. The HTTP and MCP
// promote-stage names are intentionally distinct on the wire — both appear in
// persisted audit data, so the filter exposes both.
const EVENT_TYPE_GROUPS: { label: string; types: { value: string; label: string }[] }[] = [
  {
    label: "Memory",
    types: [
      { value: "memory.write", label: "memory.write" },
      { value: "memory.supersede", label: "memory.supersede" },
      { value: "memory.deprecate", label: "memory.deprecate" },
      { value: "memory.promote", label: "memory.promote" },
    ],
  },
  {
    label: "Knowledge",
    types: [
      { value: "knowledge.write", label: "knowledge.write" },
      { value: "knowledge.supersede", label: "knowledge.supersede" },
    ],
  },
  {
    label: "Context",
    types: [
      { value: "write", label: "write" },
      { value: "typed_write", label: "typed_write" },
      { value: "status_promote", label: "status_promote" },
      { value: "status_deprecate", label: "status_deprecate" },
      { value: "session_snapshot", label: "session_snapshot" },
      { value: "packet", label: "packet" },
      { value: "bulk_ingest", label: "bulk_ingest" },
      { value: "chunked_ingest", label: "chunked_ingest" },
    ],
  },
  {
    label: "Promote (MCP)",
    types: [
      { value: "promote", label: "promote" },
      { value: "promote.request", label: "promote.request" },
      { value: "promote.approve", label: "promote.approve" },
    ],
  },
  {
    label: "Promote (HTTP)",
    types: [
      { value: "promote.request.created", label: "promote.request.created" },
      { value: "promote.request.approved", label: "promote.request.approved" },
    ],
  },
  {
    label: "Maintenance",
    types: [
      { value: "maintenance.trim", label: "maintenance.trim" },
      { value: "maintenance.compact", label: "maintenance.compact" },
    ],
  },
];

// Pick a badge variant based on event-type semantics. Keeps the row scannable.
function badgeVariant(eventType: string): "ok" | "warn" | "danger" | "muted" | "primary" {
  if (eventType.includes("error") || eventType.includes("deprecate")) return "danger";
  if (eventType.startsWith("promote") || eventType.includes("supersede")) return "warn";
  if (eventType.startsWith("memory.") || eventType.startsWith("knowledge.")) return "primary";
  return "ok";
}

// Heuristic: route the click to the right detail page. Memory/knowledge events
// have a domain encoded in the event_type prefix; everything else is context.
function routeForEvent(eventType: string): "memory" | "knowledge" | "context" {
  if (eventType.startsWith("memory.")) return "memory";
  if (eventType.startsWith("knowledge.")) return "knowledge";
  return "context";
}

function dayBucket(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "(invalid date)";
  return d.toISOString().slice(0, 10);
}

interface Props {
  // Click-through to the affected record. Memory/knowledge events route to the
  // memory/knowledge detail page; context events route to the context-domain
  // record detail page. Optional — without this, rows expand inline only.
  onOpenItem?: (domain: "memory" | "knowledge" | "context", namespace: string, key: string) => void;
}

export function AuditPage({ onOpenItem }: Props) {
  const [eventType, setEventType] = useState("");
  const [nsFilter, setNsFilter] = useState("");
  const [actorFilter, setActorFilter] = useState("");
  // since/until are RFC3339; the form takes datetime-local (no timezone)
  // and we append "Z" before sending so the backend parses cleanly. Empty
  // means unbounded.
  const [since, setSince] = useState("");
  const [until, setUntil] = useState("");
  const [limit, setLimit] = useState("50");
  const [groupByDay, setGroupByDay] = useState(true);
  const [expandedRow, setExpandedRow] = useState<number | null>(null);

  // First-page events come from the auto-refresh poll. "Load more" appends
  // older events (cursor-based) into a separate buffer so the next poll
  // doesn't blow them away.
  const [olderEvents, setOlderEvents] = useState<AuditEvent[]>([]);
  const [loadingMore, setLoadingMore] = useState(false);
  const [nextCursor, setNextCursor] = useState<number | null>(null);

  // Convert form datetime-local strings (no tz) into RFC3339 by appending Z.
  // Empty means unbounded.
  const sinceISO = since ? `${since}:00Z` : "";
  const untilISO = until ? `${until}:59Z` : "";

  // When filters change, drop the older-events buffer — the cursor is no
  // longer meaningful against the new filter set. Actor + time bounds are
  // server-side now, so they belong in this key too.
  const lastFilterKey = useRef("");
  useEffect(() => {
    const key = `${eventType}|${nsFilter}|${actorFilter}|${sinceISO}|${untilISO}|${limit}`;
    if (lastFilterKey.current !== key) {
      lastFilterKey.current = key;
      setOlderEvents([]);
      setNextCursor(null);
    }
  }, [eventType, nsFilter, actorFilter, sinceISO, untilISO, limit]);

  const loadFirstPage = useCallback(async () => {
    const params: Parameters<typeof getAuditEvents>[0] = {
      limit: parseInt(limit, 10) || 50,
    };
    if (eventType) params.event_type = eventType;
    const ns = nsFilter.trim();
    if (ns) params.namespace = ns;
    const a = actorFilter.trim();
    if (a) params.actor = a;
    if (sinceISO) params.since = sinceISO;
    if (untilISO) params.until = untilISO;
    const res = await getAuditEvents(params);
    setNextCursor(res.next_cursor);
    return res;
  }, [eventType, nsFilter, actorFilter, sinceISO, untilISO, limit]);

  // Lightweight fetcher with manual refresh + 10s auto-refresh.
  const [firstPage, setFirstPage] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await loadFirstPage();
      setFirstPage(res.items);
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, [loadFirstPage]);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 10_000);
    return () => clearInterval(t);
  }, [refresh]);

  const loadMore = async () => {
    if (!nextCursor) return;
    setLoadingMore(true);
    try {
      const params: Parameters<typeof getAuditEvents>[0] = {
        limit: parseInt(limit, 10) || 50,
        cursor: nextCursor,
      };
      if (eventType) params.event_type = eventType;
      const ns = nsFilter.trim();
      if (ns) params.namespace = ns;
      const a = actorFilter.trim();
      if (a) params.actor = a;
      if (sinceISO) params.since = sinceISO;
      if (untilISO) params.until = untilISO;
      const res = await getAuditEvents(params);
      setOlderEvents((prev) => [...prev, ...res.items]);
      setNextCursor(res.next_cursor);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      toast.error(`Load more failed: ${msg}`);
    } finally {
      setLoadingMore(false);
    }
  };

  // First page + older buffer combined. Actor / since / until are now
  // server-side filters on /v1/context/audit, so no client-side trimming.
  const events = useMemo(() => [...firstPage, ...olderEvents], [firstPage, olderEvents]);

  // Group by day for the timeline view. When grouping is off, render flat.
  const grouped = useMemo(() => {
    if (!groupByDay) return [{ day: "", items: events }];
    const map = new Map<string, AuditEvent[]>();
    for (const e of events) {
      const d = dayBucket(e.created_at);
      const arr = map.get(d) ?? [];
      arr.push(e);
      map.set(d, arr);
    }
    return Array.from(map.entries())
      .sort(([a], [b]) => b.localeCompare(a))
      .map(([day, items]) => ({ day, items }));
  }, [events, groupByDay]);

  const handleRowClick = (evt: AuditEvent) => {
    const route = routeForEvent(evt.event_type);
    if (onOpenItem && evt.namespace && evt.key) {
      onOpenItem(route, evt.namespace, evt.key);
    } else {
      // No drill-through available — fall back to expanding metadata in place.
      setExpandedRow(expandedRow === evt.id ? null : evt.id);
    }
  };

  const totalShown = events.length;

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Audit & Ops</h2>
        <div className="page-actions">
          <button type="button" className="hud-button-ghost" onClick={refresh} disabled={loading}>
            <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
              {loading ? <Spinner size={11} /> : <RefreshCw size={11} />} Refresh
            </span>
          </button>
        </div>
      </div>

      {/* Filters */}
      <div className="hud-panel" style={{ padding: "0.75rem", marginBottom: "0.75rem" }}>
        <div className="form-grid" style={{ gridTemplateColumns: "1.2fr 1.5fr 1fr 0.6fr" }}>
          <div className="form-field">
            <label className="hud-label" htmlFor="audit-event-type">
              Event Type
            </label>
            <select
              id="audit-event-type"
              className="hud-input"
              value={eventType}
              onChange={(e) => setEventType(e.target.value)}
              style={{ width: "100%" }}
            >
              <option value="">All Events</option>
              {EVENT_TYPE_GROUPS.map((group) => (
                <optgroup key={group.label} label={group.label}>
                  {group.types.map((t) => (
                    <option key={t.value} value={t.value}>
                      {t.label}
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="audit-namespace">
              Namespace
            </label>
            <input
              id="audit-namespace"
              className="hud-input"
              placeholder="user/chrispian/memory"
              value={nsFilter}
              onChange={(e) => setNsFilter(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="audit-actor">
              Actor <span style={{ color: "rgb(var(--muted))" }}>(substring)</span>
            </label>
            <input
              id="audit-actor"
              className="hud-input"
              placeholder="agent_id substring..."
              value={actorFilter}
              onChange={(e) => setActorFilter(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="audit-limit">
              Page Limit
            </label>
            <input
              id="audit-limit"
              className="hud-input"
              type="number"
              min="1"
              max="500"
              value={limit}
              onChange={(e) => setLimit(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
        </div>
        <div className="form-grid" style={{ gridTemplateColumns: "1fr 1fr", marginTop: "0.5rem" }}>
          <div className="form-field">
            <label className="hud-label" htmlFor="audit-since">
              Since <span style={{ color: "rgb(var(--muted))" }}>(local time)</span>
            </label>
            <input
              id="audit-since"
              className="hud-input"
              type="datetime-local"
              value={since}
              onChange={(e) => setSince(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="audit-until">
              Until <span style={{ color: "rgb(var(--muted))" }}>(local time)</span>
            </label>
            <input
              id="audit-until"
              className="hud-input"
              type="datetime-local"
              value={until}
              onChange={(e) => setUntil(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
        </div>
        <div style={{ marginTop: "0.5rem", display: "flex", gap: "1rem", alignItems: "center" }}>
          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.3rem",
              fontSize: "0.75rem",
              cursor: "pointer",
            }}
          >
            <input
              type="checkbox"
              checked={groupByDay}
              onChange={(e) => setGroupByDay(e.target.checked)}
              style={{ accentColor: "rgb(var(--primary))" }}
            />
            Group by day
          </label>
          <span style={{ fontSize: "0.7rem", color: "rgb(var(--muted))" }}>
            {totalShown} shown
            {nextCursor && ` · more available`}
          </span>
        </div>
      </div>

      {error && (
        <div
          className="hud-panel"
          style={{ padding: "0.75rem", color: "rgb(var(--danger))", marginBottom: "0.75rem" }}
        >
          Error: {error.message}
        </div>
      )}

      {/* Timeline */}
      <div className="hud-panel">
        {loading && events.length === 0 && (
          <div style={{ padding: "2rem", textAlign: "center" }}>
            <Spinner size={20} />
          </div>
        )}

        {!loading && events.length === 0 && (
          <EmptyState
            message="No audit events"
            sub={
              eventType || nsFilter || actorFilter
                ? "Try a different filter combination"
                : "Events will appear as operations occur"
            }
          />
        )}

        {events.length > 0 && (
          <table className="hud-table">
            <thead>
              <tr>
                <th style={{ width: 30 }} aria-label="expand" />
                <th>Type</th>
                <th>Namespace</th>
                <th>Key</th>
                <th>Rev</th>
                <th>Actor</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {grouped.map((group) => (
                <Fragment key={group.day || "flat"}>
                  {group.day && (
                    <tr key={`day-${group.day}`}>
                      <td
                        colSpan={7}
                        style={{
                          padding: "0.4rem 0.75rem",
                          background: "rgba(var(--panel2) / 0.5)",
                          fontFamily: "var(--font-mono)",
                          fontSize: "0.7rem",
                          color: "rgb(var(--muted))",
                          textTransform: "uppercase",
                          letterSpacing: "0.05em",
                        }}
                      >
                        {group.day} · {group.items.length} event
                        {group.items.length === 1 ? "" : "s"}
                      </td>
                    </tr>
                  )}
                  {group.items.map((evt) => {
                    const isExpanded = expandedRow === evt.id;
                    const route = routeForEvent(evt.event_type);
                    const canDrillThrough = !!onOpenItem && !!evt.namespace && !!evt.key;
                    return (
                      <Fragment key={evt.id}>
                        <tr
                          onClick={() => handleRowClick(evt)}
                          style={{
                            cursor: canDrillThrough || evt.metadata != null ? "pointer" : "default",
                          }}
                          title={
                            canDrillThrough
                              ? `Open ${route} detail for ${evt.namespace}/${evt.key}`
                              : evt.metadata != null
                                ? "Toggle metadata"
                                : ""
                          }
                        >
                          <td>
                            {evt.metadata != null ? (
                              isExpanded ? (
                                <ChevronDown size={12} />
                              ) : (
                                <ChevronRight size={12} />
                              )
                            ) : (
                              <FileText size={12} style={{ color: "rgb(var(--muted))" }} />
                            )}
                          </td>
                          <td>
                            <StatusBadge
                              status={evt.event_type}
                              variant={badgeVariant(evt.event_type)}
                            />
                          </td>
                          <td style={{ fontSize: "0.8rem", fontFamily: "var(--font-mono)" }}>
                            {evt.namespace}
                          </td>
                          <td style={{ fontSize: "0.8rem", fontFamily: "var(--font-mono)" }}>
                            {evt.key}
                          </td>
                          <td style={{ color: "rgb(var(--muted))", fontSize: "0.8rem" }}>
                            r{evt.revision}
                          </td>
                          <td
                            style={{
                              color: "rgb(var(--muted))",
                              fontFamily: "var(--font-mono)",
                              fontSize: "0.75rem",
                            }}
                          >
                            {evt.actor}
                          </td>
                          <td style={{ color: "rgb(var(--muted))", fontSize: "0.75rem" }}>
                            {new Date(evt.created_at).toLocaleString()}
                          </td>
                        </tr>
                        {isExpanded && evt.metadata != null && (
                          <tr key={`${evt.id}-detail`}>
                            <td
                              colSpan={7}
                              style={{
                                padding: "0.5rem 0.75rem",
                                background: "rgba(var(--panel2) / 0.4)",
                              }}
                            >
                              <div className="hud-label" style={{ marginBottom: "0.3rem" }}>
                                Metadata
                              </div>
                              <JsonViewer data={evt.metadata} maxHeight="200px" />
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    );
                  })}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}

        {nextCursor && (
          <div
            style={{
              padding: "0.75rem",
              textAlign: "center",
              borderTop: "1px solid rgb(var(--border))",
            }}
          >
            <button
              type="button"
              className="hud-button-ghost"
              onClick={loadMore}
              disabled={loadingMore}
            >
              <span
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "0.3rem",
                  justifyContent: "center",
                }}
              >
                {loadingMore ? <Spinner size={11} /> : null}
                Load older events
              </span>
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
