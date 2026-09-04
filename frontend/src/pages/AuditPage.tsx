import {
  Button,
  Callout,
  Checkbox,
  EmptyState,
  Input,
  JsonViewer,
  Label,
  PageHeader,
  Pill,
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@hollis-labs/sysop-ui";
import { ListPageLayout } from "@hollis-labs/sysop-ui/layout";
import { ChevronDown, ChevronRight, FileText, RefreshCw } from "lucide-react";
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { getAuditEvents } from "../api/client";
import type { AuditEvent } from "../api/types";
import { Spinner } from "../components/ui/Spinner";

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
    // One group, not one per surface: HTTP, MCP and CLI emit the same
    // event_type per stage, so a single filter covers every initiator.
    label: "Promote",
    types: [
      { value: "promote.request", label: "promote.request" },
      { value: "promote.approve", label: "promote.approve" },
      { value: "promote", label: "promote" },
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

function pillTone(eventType: string): "success" | "warning" | "danger" | "info" {
  if (eventType.includes("error") || eventType.includes("deprecate")) return "danger";
  if (eventType.startsWith("promote") || eventType.includes("supersede")) return "warning";
  if (eventType.startsWith("memory.") || eventType.startsWith("knowledge.")) return "info";
  return "success";
}

function routeForEvent(eventType: string): "memory" | "knowledge" | "context" {
  if (eventType.startsWith("memory.")) return "memory";
  if (eventType.startsWith("knowledge.")) return "knowledge";
  return "context";
}

function dayBucket(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "(invalid date)";
  return date.toISOString().slice(0, 10);
}

interface Props {
  onOpenItem?: (domain: "memory" | "knowledge" | "context", namespace: string, key: string) => void;
}

export function AuditPage({ onOpenItem }: Props) {
  const [eventType, setEventType] = useState("");
  const [nsFilter, setNsFilter] = useState("");
  const [actorFilter, setActorFilter] = useState("");
  const [since, setSince] = useState("");
  const [until, setUntil] = useState("");
  const [limit, setLimit] = useState("50");
  const [groupByDay, setGroupByDay] = useState(true);
  const [expandedRow, setExpandedRow] = useState<number | null>(null);
  const [olderEvents, setOlderEvents] = useState<AuditEvent[]>([]);
  const [loadingMore, setLoadingMore] = useState(false);
  const [nextCursor, setNextCursor] = useState<number | null>(null);

  const sinceISO = since ? `${since}:00Z` : "";
  const untilISO = until ? `${until}:59Z` : "";

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
    const namespace = nsFilter.trim();
    if (namespace) params.namespace = namespace;
    const actor = actorFilter.trim();
    if (actor) params.actor = actor;
    if (sinceISO) params.since = sinceISO;
    if (untilISO) params.until = untilISO;
    const response = await getAuditEvents(params);
    setNextCursor(response.next_cursor);
    return response;
  }, [eventType, nsFilter, actorFilter, sinceISO, untilISO, limit]);

  const [firstPage, setFirstPage] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await loadFirstPage();
      setFirstPage(response.items);
    } catch (reason) {
      setError(reason instanceof Error ? reason : new Error(String(reason)));
    } finally {
      setLoading(false);
    }
  }, [loadFirstPage]);

  useEffect(() => {
    void refresh();
    const timer = setInterval(refresh, 10_000);
    return () => clearInterval(timer);
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
      const namespace = nsFilter.trim();
      if (namespace) params.namespace = namespace;
      const actor = actorFilter.trim();
      if (actor) params.actor = actor;
      if (sinceISO) params.since = sinceISO;
      if (untilISO) params.until = untilISO;
      const response = await getAuditEvents(params);
      setOlderEvents((previous) => [...previous, ...response.items]);
      setNextCursor(response.next_cursor);
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      toast.error(`Load more failed: ${message}`);
    } finally {
      setLoadingMore(false);
    }
  };

  const events = useMemo(() => [...firstPage, ...olderEvents], [firstPage, olderEvents]);
  const grouped = useMemo(() => {
    if (!groupByDay) return [{ day: "", items: events }];
    const groups = new Map<string, AuditEvent[]>();
    for (const event of events) {
      const day = dayBucket(event.created_at);
      groups.set(day, [...(groups.get(day) ?? []), event]);
    }
    return Array.from(groups.entries())
      .sort(([left], [right]) => right.localeCompare(left))
      .map(([day, items]) => ({ day, items }));
  }, [events, groupByDay]);

  const handleRowAction = (event: AuditEvent) => {
    const route = routeForEvent(event.event_type);
    if (onOpenItem && event.namespace && event.key) {
      onOpenItem(route, event.namespace, event.key);
      return;
    }
    if (event.metadata != null) {
      setExpandedRow(expandedRow === event.id ? null : event.id);
    }
  };

  const filters = (
    <section
      className="border-b border-border-strong bg-panel px-4 py-4 lg:px-6"
      aria-label="Audit filters"
    >
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-[1.2fr_1.5fr_1fr_.65fr]">
        <div className="space-y-1.5">
          <Label id="audit-event-type-label">Event type</Label>
          <Select
            value={eventType || "all"}
            onValueChange={(value) => setEventType(value === "all" ? "" : (value ?? ""))}
          >
            <SelectTrigger className="w-full font-mono" aria-labelledby="audit-event-type-label">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All events</SelectItem>
              {EVENT_TYPE_GROUPS.map((group) => (
                <SelectGroup key={group.label}>
                  <SelectLabel>{group.label}</SelectLabel>
                  {group.types.map((type) => (
                    <SelectItem key={type.value} value={type.value}>
                      {type.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="audit-namespace">Namespace</Label>
          <Input
            id="audit-namespace"
            className="font-mono"
            placeholder="user/chrispian/memory"
            value={nsFilter}
            onChange={(event) => setNsFilter(event.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="audit-actor">
            Actor <span className="font-normal text-text-subtle">(substring)</span>
          </Label>
          <Input
            id="audit-actor"
            className="font-mono"
            placeholder="agent_id substring..."
            value={actorFilter}
            onChange={(event) => setActorFilter(event.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="audit-limit">Page limit</Label>
          <Input
            id="audit-limit"
            className="font-mono tabular-nums"
            type="number"
            min={1}
            max={500}
            value={limit}
            onChange={(event) => setLimit(event.target.value)}
          />
        </div>
      </div>
      <div className="mt-4 grid gap-4 md:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="audit-since">
            Since <span className="font-normal text-text-subtle">(local time)</span>
          </Label>
          <Input
            id="audit-since"
            type="datetime-local"
            value={since}
            onChange={(event) => setSince(event.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="audit-until">
            Until <span className="font-normal text-text-subtle">(local time)</span>
          </Label>
          <Input
            id="audit-until"
            type="datetime-local"
            value={until}
            onChange={(event) => setUntil(event.target.value)}
          />
        </div>
      </div>
      <div className="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2">
        <Label
          htmlFor="audit-group-by-day"
          className="flex cursor-pointer items-center gap-2 text-xs font-normal text-text-soft"
        >
          <Checkbox
            id="audit-group-by-day"
            checked={groupByDay}
            onCheckedChange={(checked) => setGroupByDay(checked === true)}
          />
          Group by day
        </Label>
        <span className="text-xs tabular-nums text-text-subtle" aria-live="polite">
          {events.length} shown{nextCursor ? " · more available" : ""}
        </span>
      </div>
    </section>
  );

  return (
    <ListPageLayout
      header={
        <PageHeader title="Audit & Ops">
          <Button type="button" variant="outline" size="sm" onClick={refresh} disabled={loading}>
            {loading ? <Spinner size={13} /> : <RefreshCw aria-hidden="true" />}
            Refresh
          </Button>
        </PageHeader>
      }
      filters={filters}
    >
      {error ? (
        <div className="border-b border-border-strong px-4 py-4 lg:px-6">
          <Callout tone="danger" title="Audit events unavailable">
            {error.message}
          </Callout>
        </div>
      ) : null}

      {loading && events.length === 0 ? (
        <div
          className="flex min-h-48 items-center justify-center"
          role="status"
          aria-label="Loading audit events"
        >
          <Spinner size={20} />
        </div>
      ) : null}

      {!loading && events.length === 0 ? (
        <EmptyState
          variant={eventType || nsFilter || actorFilter ? "no-results" : "empty"}
          title="No audit events"
          description={
            eventType || nsFilter || actorFilter
              ? "Try a different filter combination."
              : "Events will appear as operations occur."
          }
        />
      ) : null}

      {events.length > 0 ? (
        <div className="min-w-[62rem]">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <span className="sr-only">Open</span>
                </TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Namespace</TableHead>
                <TableHead>Key</TableHead>
                <TableHead>Rev</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Time</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {grouped.map((group) => (
                <Fragment key={group.day || "flat"}>
                  {group.day ? (
                    <TableRow>
                      <TableCell
                        colSpan={7}
                        className="bg-panel-2 px-4 py-2 font-mono text-[11px] font-medium tracking-wide text-text-subtle"
                      >
                        {group.day} · {group.items.length} event
                        {group.items.length === 1 ? "" : "s"}
                      </TableCell>
                    </TableRow>
                  ) : null}
                  {group.items.map((event) => {
                    const isExpanded = expandedRow === event.id;
                    const route = routeForEvent(event.event_type);
                    const canDrillThrough = Boolean(onOpenItem && event.namespace && event.key);
                    const canOpen = canDrillThrough || event.metadata != null;
                    const actionLabel = canDrillThrough
                      ? `Open ${route} detail for ${event.namespace}/${event.key}`
                      : isExpanded
                        ? "Collapse event metadata"
                        : "Expand event metadata";
                    return (
                      <Fragment key={event.id}>
                        <TableRow
                          className={
                            canOpen ? "cursor-pointer hover:bg-panel-hover-soft" : undefined
                          }
                          title={canOpen ? actionLabel : undefined}
                          onClick={canOpen ? () => handleRowAction(event) : undefined}
                        >
                          <TableCell>
                            {canOpen ? (
                              <Button
                                type="button"
                                variant="ghost"
                                size="icon-xs"
                                aria-label={actionLabel}
                                title={actionLabel}
                                onClick={(clickEvent) => {
                                  clickEvent.stopPropagation();
                                  handleRowAction(event);
                                }}
                              >
                                {event.metadata != null && !canDrillThrough ? (
                                  isExpanded ? (
                                    <ChevronDown aria-hidden="true" />
                                  ) : (
                                    <ChevronRight aria-hidden="true" />
                                  )
                                ) : (
                                  <FileText aria-hidden="true" />
                                )}
                              </Button>
                            ) : (
                              <FileText className="size-3.5 text-text-subtle" aria-hidden="true" />
                            )}
                          </TableCell>
                          <TableCell>
                            <Pill tone={pillTone(event.event_type)}>{event.event_type}</Pill>
                          </TableCell>
                          <TableCell className="font-mono text-xs text-text-soft">
                            {event.namespace}
                          </TableCell>
                          <TableCell className="font-mono text-xs text-text-soft">
                            {event.key}
                          </TableCell>
                          <TableCell className="text-xs tabular-nums text-text-subtle">
                            r{event.revision}
                          </TableCell>
                          <TableCell className="font-mono text-xs text-text-subtle">
                            {event.actor}
                          </TableCell>
                          <TableCell className="whitespace-nowrap text-xs text-text-subtle">
                            {new Date(event.created_at).toLocaleString()}
                          </TableCell>
                        </TableRow>
                        {isExpanded && event.metadata != null ? (
                          <TableRow>
                            <TableCell colSpan={7} className="bg-panel-2 px-4 py-4">
                              <h3 className="mb-2 text-xs font-semibold text-text-muted">
                                Metadata
                              </h3>
                              <JsonViewer value={event.metadata} className="max-h-52" />
                            </TableCell>
                          </TableRow>
                        ) : null}
                      </Fragment>
                    );
                  })}
                </Fragment>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}

      {nextCursor ? (
        <div className="flex justify-center border-t border-border-strong px-4 py-4">
          <Button type="button" variant="outline" onClick={loadMore} disabled={loadingMore}>
            {loadingMore ? <Spinner size={13} /> : null}
            Load older events
          </Button>
        </div>
      ) : null}
    </ListPageLayout>
  );
}
