import {
  Button,
  Callout,
  Card,
  CardContent,
  Checkbox,
  Input,
  Label,
  Pill,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
} from "@hollis-labs/sysop-ui";
import {
  AlertTriangle,
  ArrowRight,
  CheckSquare,
  Edit3,
  Eye,
  Filter,
  RefreshCw,
  ShieldAlert,
  Square,
  Trash2,
  X,
} from "lucide-react";
import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  listAllNamespaces,
  memoryDeprecate,
  memoryPromote,
  memoryWrite,
  tesseractLookup,
} from "../api/client";
import type { FullLookupResultItem, MemoryRevision, MemoryStatus } from "../api/types";
import { isFullLookupResult } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { Spinner } from "../components/ui/Spinner";
import { StatusBadge } from "../components/ui/StatusBadge";

type DomainFilter = "both" | "memory" | "knowledge";
type QueueMode = "actionable" | "all";
type ReviewPreset = "lowConfidence" | "reviewed" | "pendingReview";

interface Props {
  onOpenItem?: (domain: "memory" | "knowledge", namespace: string, key: string) => void;
  onOpenWrite?: () => void;
  initialPreset?: ReviewPreset | undefined;
}

// Extends the FULL result shape, not the union. Everything this page renders
// — author, status, confidence, payload.body — exists only on a full result,
// and loadQueue narrows to that shape before building any QueueItem. Widening
// this to TesseractLookupResultItem would let a projected result reach the
// render path, where it throws on the first missing field.
interface QueueItem extends FullLookupResultItem {
  reviewReasons: string[];
  reviewPriority: number;
}

const DISMISSED_STORAGE_KEY = "tesseract.memoryReview.dismissed";
const ALL_STATUSES: MemoryStatus[] = ["draft", "reviewed", "canonical", "deprecated"];

function isSessionNamespace(namespace: string): boolean {
  return namespace.includes("/session/");
}

function loadDismissed(): string[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(DISMISSED_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((v) => typeof v === "string") : [];
  } catch {
    return [];
  }
}

function persistDismissed(ids: string[]): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(DISMISSED_STORAGE_KEY, JSON.stringify(ids));
  } catch {
    // Ignore storage issues; review queue still works for the current session.
  }
}

function getReviewReasons(revision: MemoryRevision, threshold: number): string[] {
  const reasons: string[] = [];
  if (revision.status === "draft") reasons.push("Draft");
  if (revision.status === "reviewed") reasons.push("Reviewed, not canonical");
  if (revision.status === "deprecated") reasons.push("Deprecated / superseded");
  if (revision.confidence < threshold)
    reasons.push(`Low confidence (${revision.confidence.toFixed(2)})`);
  if (revision.supersedes) reasons.push("Supersedes an older revision");
  if (revision.dedup_match) reasons.push("Potential duplicate");
  if (isSessionNamespace(revision.namespace) && revision.status !== "deprecated") {
    reasons.push("Session-scoped candidate for promotion");
  }
  return reasons;
}

function getReviewPriority(revision: MemoryRevision, threshold: number, score?: number): number {
  let priority = 0;
  if (revision.status === "draft") priority += 120;
  if (revision.status === "reviewed") priority += 100;
  if (revision.status === "deprecated") priority += 90;
  if (revision.status === "canonical") priority += 40;
  if (revision.confidence < threshold) {
    priority += Math.round((threshold - revision.confidence) * 120);
  }
  if (revision.supersedes) priority += 20;
  if (revision.dedup_match) priority += 20;
  if (isSessionNamespace(revision.namespace) && revision.status !== "deprecated") priority += 15;
  if (typeof score === "number") priority += Math.round(score * 10);
  return priority;
}

function isActionable(item: FullLookupResultItem, threshold: number): boolean {
  return getReviewReasons(item.revision, threshold).length > 0;
}

function isPromotable(item: FullLookupResultItem): boolean {
  return isSessionNamespace(item.revision.namespace) && item.revision.status !== "deprecated";
}

function canClarify(item: FullLookupResultItem): boolean {
  return Boolean(item.revision.memory_key);
}

function formatTimestamp(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString();
}

function summarizeSelection(
  items: QueueItem[],
  selected: Set<string>,
): { total: number; promotable: number; deprecatable: number } {
  let promotable = 0;
  let deprecatable = 0;
  for (const item of items) {
    if (!selected.has(item.revision.revision_id)) continue;
    if (isPromotable(item)) promotable++;
    if (item.revision.status !== "deprecated") deprecatable++;
  }
  return { total: selected.size, promotable, deprecatable };
}

export function MemoryReviewPage({ onOpenItem, onOpenWrite, initialPreset }: Props) {
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [acting, setActing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [items, setItems] = useState<QueueItem[]>([]);
  const [namespaceFilter, setNamespaceFilter] = useState("");
  const [domain, setDomain] = useState<DomainFilter>("both");
  const [mode, setMode] = useState<QueueMode>("actionable");
  const [includeDismissed, setIncludeDismissed] = useState(false);
  const [resultLimit, setResultLimit] = useState("180");
  const [confidenceThreshold, setConfidenceThreshold] = useState("0.8");
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [focusedId, setFocusedId] = useState<string | null>(null);
  const [dismissedIds, setDismissedIds] = useState<Set<string>>(() => new Set(loadDismissed()));
  const [activePreset, setActivePreset] = useState<ReviewPreset | null>(initialPreset ?? null);

  const [promoteTargetNamespace, setPromoteTargetNamespace] = useState("");
  const [promoteActorId, setPromoteActorId] = useState("ui-user");
  const [promoteActorVersion, setPromoteActorVersion] = useState("");

  const [clarifyAuthor, setClarifyAuthor] = useState("ui-user");
  const [clarifyVersion, setClarifyVersion] = useState("");
  const [clarifyStatus, setClarifyStatus] = useState<MemoryStatus>("reviewed");
  const [clarifyConfidence, setClarifyConfidence] = useState("0.9");
  const [clarifySummary, setClarifySummary] = useState("");
  const [clarifyBody, setClarifyBody] = useState("");
  const [clarifySubmitting, setClarifySubmitting] = useState(false);

  const threshold = useMemo(() => {
    const parsed = parseFloat(confidenceThreshold);
    return Number.isFinite(parsed) ? parsed : 0.8;
  }, [confidenceThreshold]);

  const loadQueue = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // The queue is built over EVERY registered namespace, so this pages
      // until the server says there is no more. A single capped request
      // returned 1000 of 1125 registered namespaces and the queue looked
      // complete while silently omitting the rest (CW-20260909-0003).
      const nsResponse = await listAllNamespaces();
      const allNamespaces = nsResponse.items.map((item) => item.namespace);
      setNamespaces(allNamespaces);
      if (!nsResponse.complete) {
        toast.warning(
          `Namespace registry did not finish loading: reviewing ${allNamespaces.length} of ${nsResponse.count}. The queue below is incomplete.`,
        );
      }

      if (allNamespaces.length === 0) {
        setItems([]);
        setFocusedId(null);
        return;
      }

      const parsedLimit = parseInt(resultLimit, 10);
      const baseLimit = Number.isFinite(parsedLimit) && parsedLimit > 0 ? parsedLimit : 180;
      const fetchLimit = Math.min(Math.max(baseLimit * 2, 100), 500);
      const domainFilter = domain === "both" ? undefined : [domain];

      // payload_mode: "full" is required here, not an optimization.
      //
      // This page is an EDITOR: the clarify panel prefills its body field
      // from revision.payload.body and writes the result back as a new
      // revision. Under the server's default projection the body is
      // withheld, the editor would prefill empty, and saving would publish
      // a head revision with the body dropped — silently, since the write
      // path is literal by design and simply records what it was given.
      // A component that edits what it reads asks for the whole thing.
      const headReq: Parameters<typeof tesseractLookup>[0] = {
        namespaces: allNamespaces,
        ranking: "activation",
        revision_scope: "current",
        statuses: ["draft", "reviewed", "canonical"],
        limit: fetchLimit,
        payload_mode: "full",
      };
      const deprecatedReq: Parameters<typeof tesseractLookup>[0] = {
        namespaces: allNamespaces,
        ranking: "chronological",
        revision_scope: "timeline",
        statuses: ["deprecated"],
        limit: Math.min(Math.max(Math.floor(baseLimit / 2), 40), 150),
        payload_mode: "full",
      };
      if (domainFilter) {
        headReq.domains = domainFilter;
        deprecatedReq.domains = domainFilter;
      }

      const [heads, deprecated] = await Promise.all([
        tesseractLookup(headReq),
        tesseractLookup(deprecatedReq),
      ]);

      // Narrow to full results at the boundary, before anything downstream
      // can touch a field a projection would have withheld.
      //
      // Both requests pin payload_mode: "full", so nothing should be dropped
      // here. The filter is what makes that a checked fact rather than an
      // assumption: if the pin is ever lost, the queue comes back short with
      // a visible error instead of throwing at render, and the editor is
      // never handed a revision whose body it did not receive.
      const rawResults = [...heads.results, ...deprecated.results];
      const fullResults = rawResults.filter(isFullLookupResult);
      if (fullResults.length !== rawResults.length) {
        toast.error(
          `${rawResults.length - fullResults.length} result(s) arrived without their payload and were skipped. Review requires payload_mode="full".`,
        );
      }
      const fullHeads = fullResults.filter((item) => item.revision.status !== "deprecated");
      const fullDeprecated = fullResults.filter((item) => item.revision.status === "deprecated");

      const latestDeprecatedByMemory = new Map<string, FullLookupResultItem>();
      for (const item of fullDeprecated) {
        const existing = latestDeprecatedByMemory.get(item.revision.memory_id);
        if (!existing || item.revision.created_at > existing.revision.created_at) {
          latestDeprecatedByMemory.set(item.revision.memory_id, item);
        }
      }

      const merged = [...fullHeads, ...latestDeprecatedByMemory.values()];
      const unique = new Map<string, QueueItem>();
      for (const item of merged) {
        unique.set(item.revision.revision_id, {
          ...item,
          reviewReasons: getReviewReasons(item.revision, threshold),
          reviewPriority: getReviewPriority(item.revision, threshold, item.score),
        });
      }

      const nextItems = Array.from(unique.values()).sort((a, b) => {
        if (b.reviewPriority !== a.reviewPriority) return b.reviewPriority - a.reviewPriority;
        if ((b.score ?? 0) !== (a.score ?? 0)) return (b.score ?? 0) - (a.score ?? 0);
        return b.revision.created_at.localeCompare(a.revision.created_at);
      });
      setItems(nextItems);
      setSelectedIds((prev) => {
        const next = new Set<string>();
        const valid = new Set(nextItems.map((item) => item.revision.revision_id));
        prev.forEach((id) => {
          if (valid.has(id)) next.add(id);
        });
        return next;
      });
      setFocusedId((prev) => {
        if (prev && nextItems.some((item) => item.revision.revision_id === prev)) return prev;
        return nextItems[0]?.revision.revision_id ?? null;
      });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Review queue failed: ${msg}`);
    } finally {
      setLoading(false);
    }
  }, [domain, resultLimit, threshold]);

  useEffect(() => {
    void loadQueue();
  }, [loadQueue]);

  useEffect(() => {
    persistDismissed(Array.from(dismissedIds));
  }, [dismissedIds]);

  useEffect(() => {
    setActivePreset(initialPreset ?? null);
  }, [initialPreset]);

  const visibleItems = useMemo(() => {
    const q = namespaceFilter.trim().toLowerCase();
    return items.filter((item) => {
      if (mode === "actionable" && !isActionable(item, threshold)) return false;
      if (!includeDismissed && dismissedIds.has(item.revision.revision_id)) return false;
      if (activePreset === "lowConfidence" && item.revision.confidence >= threshold) return false;
      if (activePreset === "reviewed" && item.revision.status !== "reviewed") return false;
      if (
        activePreset === "pendingReview" &&
        item.revision.status !== "draft" &&
        item.revision.status !== "reviewed"
      ) {
        return false;
      }
      if (q) {
        const haystack =
          `${item.revision.namespace} ${item.revision.memory_key ?? ""} ${item.revision.payload.summary}`.toLowerCase();
        if (!haystack.includes(q)) return false;
      }
      return true;
    });
  }, [activePreset, dismissedIds, includeDismissed, items, mode, namespaceFilter, threshold]);

  const focusedItem = useMemo(
    () =>
      visibleItems.find((item) => item.revision.revision_id === focusedId) ??
      visibleItems[0] ??
      null,
    [focusedId, visibleItems],
  );

  useEffect(() => {
    if (!focusedItem) return;
    setFocusedId(focusedItem.revision.revision_id);
  }, [focusedItem]);

  useEffect(() => {
    if (!focusedItem) return;
    setClarifyStatus(
      focusedItem.revision.status === "deprecated" ? "reviewed" : focusedItem.revision.status,
    );
    setClarifyConfidence(String(focusedItem.revision.confidence.toFixed(2)));
    setClarifySummary(focusedItem.revision.payload.summary);
    setClarifyBody(focusedItem.revision.payload.body ?? "");
  }, [focusedItem]);

  const counts = useMemo(() => {
    let draft = 0;
    let reviewed = 0;
    let canonical = 0;
    let deprecated = 0;
    for (const item of visibleItems) {
      if (item.revision.status === "draft") draft++;
      if (item.revision.status === "reviewed") reviewed++;
      if (item.revision.status === "canonical") canonical++;
      if (item.revision.status === "deprecated") deprecated++;
    }
    return { draft, reviewed, canonical, deprecated };
  }, [visibleItems]);

  const selectionSummary = useMemo(
    () => summarizeSelection(visibleItems, selectedIds),
    [selectedIds, visibleItems],
  );

  const toggleSelection = (revisionId: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(revisionId)) next.delete(revisionId);
      else next.add(revisionId);
      return next;
    });
  };

  const toggleSelectAllVisible = () => {
    const visibleIds = visibleItems.map((item) => item.revision.revision_id);
    const everySelected = visibleIds.length > 0 && visibleIds.every((id) => selectedIds.has(id));
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (everySelected) {
        visibleIds.forEach((id) => {
          next.delete(id);
        });
      } else {
        visibleIds.forEach((id) => {
          next.add(id);
        });
      }
      return next;
    });
  };

  const dismissSelected = () => {
    if (selectedIds.size === 0) return;
    const next = new Set(dismissedIds);
    selectedIds.forEach((id) => {
      next.add(id);
    });
    setDismissedIds(next);
    setSelectedIds(new Set());
    toast.success(
      `Dismissed ${selectionSummary.total} item${selectionSummary.total === 1 ? "" : "s"} from the review queue`,
    );
  };

  const restoreDismissed = () => {
    setDismissedIds(new Set());
    toast.success("Dismissed review items restored");
  };

  const handleBulkDeprecate = async () => {
    const targets = visibleItems.filter(
      (item) => selectedIds.has(item.revision.revision_id) && item.revision.status !== "deprecated",
    );
    if (targets.length === 0) {
      toast.error("Select at least one non-deprecated item to deprecate");
      return;
    }
    if (
      !window.confirm(
        `Deprecate ${targets.length} selected item${targets.length === 1 ? "" : "s"}?`,
      )
    ) {
      return;
    }
    setActing(true);
    try {
      let ok = 0;
      const failures: string[] = [];
      for (const item of targets) {
        try {
          await memoryDeprecate({ revision_id: item.revision.revision_id });
          ok++;
        } catch (err) {
          failures.push(
            `${item.revision.memory_key ?? item.revision.revision_id}: ${err instanceof Error ? err.message : String(err)}`,
          );
        }
      }
      if (ok > 0) toast.success(`Deprecated ${ok} item${ok === 1 ? "" : "s"}`);
      if (failures.length > 0) toast.error(`${failures.length} deprecations failed`);
      setSelectedIds(new Set());
      await loadQueue();
    } finally {
      setActing(false);
    }
  };

  const handleBulkPromote = async () => {
    const targetNamespace = promoteTargetNamespace.trim();
    const actorAgentId = promoteActorId.trim();
    if (!targetNamespace || !actorAgentId) {
      toast.error("Target namespace and actor are required for promotion");
      return;
    }
    const targets = visibleItems.filter(
      (item) => selectedIds.has(item.revision.revision_id) && isPromotable(item),
    );
    if (targets.length === 0) {
      toast.error("Select at least one session-scoped item to promote");
      return;
    }
    setActing(true);
    try {
      let ok = 0;
      const failures: string[] = [];
      for (const item of targets) {
        try {
          const req: Parameters<typeof memoryPromote>[0] = {
            source_namespace: item.revision.namespace,
            source_memory_id: item.revision.memory_id,
            target_namespace: targetNamespace,
            actor_agent_id: actorAgentId,
          };
          if (promoteActorVersion.trim()) req.actor_version = promoteActorVersion.trim();
          await memoryPromote(req);
          ok++;
        } catch (err) {
          failures.push(
            `${item.revision.memory_key ?? item.revision.revision_id}: ${err instanceof Error ? err.message : String(err)}`,
          );
        }
      }
      if (ok > 0) toast.success(`Promoted ${ok} item${ok === 1 ? "" : "s"}`);
      if (failures.length > 0) toast.error(`${failures.length} promotions failed`);
      setSelectedIds(new Set());
      await loadQueue();
    } finally {
      setActing(false);
    }
  };

  const handleClarify = async () => {
    if (!focusedItem) return;
    if (!canClarify(focusedItem)) {
      toast.error("Clarify/update currently requires a keyed memory");
      return;
    }
    // Refuse to write back a body this page never received.
    //
    // loadQueue pins payload_mode: "full", so this should be unreachable —
    // it is here so that losing that pin (a refactor, a new call site, a
    // changed default) fails loudly instead of silently publishing a head
    // revision with the body stripped. `payload.body` cannot carry this
    // check itself: it is omitted both when withheld and when genuinely
    // empty, so only the result's payload_mode marker distinguishes them.
    if (focusedItem.payload_mode !== undefined && focusedItem.payload_mode !== "full") {
      toast.error(
        `This revision was loaded with payload_mode="${focusedItem.payload_mode}", so its body is not present. Refresh the queue before editing.`,
      );
      return;
    }
    const author = clarifyAuthor.trim();
    const summary = clarifySummary.trim();
    if (!author || !summary) {
      toast.error("Author and summary are required");
      return;
    }
    setClarifySubmitting(true);
    try {
      const parsedConfidence = parseFloat(clarifyConfidence);
      const req: Parameters<typeof memoryWrite>[0] = {
        namespace: focusedItem.revision.namespace,
        supersedes: focusedItem.revision.revision_id,
        status: clarifyStatus,
        author: {
          agent_id: author,
        },
        trigger: "manual",
        confidence: Number.isFinite(parsedConfidence)
          ? parsedConfidence
          : focusedItem.revision.confidence,
        tags: focusedItem.revision.tags,
        payload: {
          summary,
        },
      };
      if (focusedItem.revision.memory_key) req.memory_key = focusedItem.revision.memory_key;
      if (focusedItem.revision.origin) req.origin = focusedItem.revision.origin;
      if (focusedItem.revision.facets) req.facets = focusedItem.revision.facets;
      if (clarifyVersion.trim()) req.author.agent_version = clarifyVersion.trim();
      if (clarifyBody.trim()) req.payload.body = clarifyBody.trim();
      await memoryWrite(req);
      toast.success("Clarification written as a new revision");
      await loadQueue();
    } catch (err) {
      toast.error(`Clarify failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setClarifySubmitting(false);
    }
  };

  return (
    <div className="min-h-full bg-bg text-text">
      <section className="border-b border-border-strong px-4 py-4">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold tracking-tight">Memory review</h2>
            <p className="mt-1 max-w-2xl text-sm leading-6 text-text-subtle">
              Triage low-confidence, draft, reviewed, duplicate, and session-scoped memory.
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            {onOpenWrite ? (
              <Button type="button" variant="outline" size="sm" onClick={onOpenWrite}>
                Manual write
              </Button>
            ) : null}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={restoreDismissed}
              disabled={dismissedIds.size === 0}
            >
              Restore dismissed
            </Button>
            <Button
              type="button"
              size="sm"
              onClick={() => void loadQueue()}
              disabled={loading || acting}
            >
              {loading ? <Spinner size={13} /> : <RefreshCw aria-hidden="true" />} Refresh queue
            </Button>
          </div>
        </div>
      </section>

      <div className="space-y-4 p-4">
        {activePreset ? (
          <Callout tone="info" title="Preset active">
            <div className="flex items-center justify-between gap-3">
              <span>
                {activePreset === "lowConfidence"
                  ? "Low confidence"
                  : activePreset === "reviewed"
                    ? "Reviewed"
                    : "Pending review"}
              </span>
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={() => setActivePreset(null)}
              >
                Clear preset
              </Button>
            </div>
          </Callout>
        ) : null}

        <Card size="sm">
          <CardContent className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-[2fr_1fr_1fr_1.4fr_1fr]">
              <ReviewField id="review-filter" label="Filter">
                <Input
                  id="review-filter"
                  className="font-mono"
                  placeholder="Namespace, key, summary"
                  value={namespaceFilter}
                  onChange={(event) => setNamespaceFilter(event.target.value)}
                />
              </ReviewField>
              <ReviewField id="review-limit" label="Queue size">
                <Input
                  id="review-limit"
                  className="font-mono"
                  type="number"
                  min="25"
                  max="500"
                  value={resultLimit}
                  onChange={(event) => setResultLimit(event.target.value)}
                />
              </ReviewField>
              <ReviewField id="review-threshold" label="Low confidence">
                <Input
                  id="review-threshold"
                  className="font-mono"
                  type="number"
                  min="0"
                  max="1"
                  step="0.05"
                  value={confidenceThreshold}
                  onChange={(event) => setConfidenceThreshold(event.target.value)}
                />
              </ReviewField>
              <fieldset className="space-y-2">
                <legend className="text-sm font-medium">Domain</legend>
                <div className="flex flex-wrap gap-1">
                  {(["both", "memory", "knowledge"] as const).map((value) => (
                    <Button
                      key={value}
                      type="button"
                      variant={domain === value ? "default" : "outline"}
                      size="xs"
                      onClick={() => setDomain(value)}
                      aria-pressed={domain === value}
                    >
                      {value}
                    </Button>
                  ))}
                </div>
              </fieldset>
              <fieldset className="space-y-2">
                <legend className="text-sm font-medium">Mode</legend>
                <div className="flex flex-wrap gap-1">
                  {(["actionable", "all"] as const).map((value) => (
                    <Button
                      key={value}
                      type="button"
                      variant={mode === value ? "default" : "outline"}
                      size="xs"
                      onClick={() => setMode(value)}
                      aria-pressed={mode === value}
                    >
                      {value}
                    </Button>
                  ))}
                </div>
              </fieldset>
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border-soft pt-3">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge status={`draft ${counts.draft}`} variant="warn" />
                <StatusBadge status={`reviewed ${counts.reviewed}`} variant="primary" />
                <StatusBadge status={`canonical ${counts.canonical}`} variant="ok" />
                <StatusBadge status={`deprecated ${counts.deprecated}`} variant="muted" />
                <span className="text-xs text-text-subtle">
                  {visibleItems.length} shown across {namespaces.length} namespaces
                </span>
              </div>
              <Label className="flex items-center gap-2 text-text-subtle">
                <Checkbox checked={includeDismissed} onCheckedChange={setIncludeDismissed} />{" "}
                Include dismissed
              </Label>
            </div>
          </CardContent>
        </Card>

        {selectedIds.size > 0 ? (
          <Card size="sm" className="border-status-doing">
            <CardContent className="space-y-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p className="text-sm font-medium">{selectionSummary.total} selected</p>
                  <p className="mt-1 text-xs text-text-subtle">
                    {selectionSummary.promotable} promotable · {selectionSummary.deprecatable}{" "}
                    deprecatable
                  </p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setSelectedIds(new Set())}
                  >
                    Clear selection
                  </Button>
                  <Button type="button" variant="outline" size="sm" onClick={dismissSelected}>
                    Dismiss
                  </Button>
                  <Button
                    type="button"
                    variant="destructive"
                    size="sm"
                    onClick={() => void handleBulkDeprecate()}
                    disabled={acting || selectionSummary.deprecatable === 0}
                  >
                    {acting ? <Spinner size={13} /> : <Trash2 aria-hidden="true" />} Deprecate
                    selected
                  </Button>
                </div>
              </div>
              <div className="grid items-end gap-4 sm:grid-cols-2 xl:grid-cols-[2fr_1fr_1fr_auto]">
                <ReviewField id="bulk-promote-target" label="Promote to namespace">
                  <Input
                    id="bulk-promote-target"
                    className="font-mono"
                    placeholder="user/<actor>/memory or user/<actor>/project/<id>/memory"
                    value={promoteTargetNamespace}
                    onChange={(event) => setPromoteTargetNamespace(event.target.value)}
                  />
                </ReviewField>
                <ReviewField id="bulk-promote-actor" label="Actor">
                  <Input
                    id="bulk-promote-actor"
                    className="font-mono"
                    value={promoteActorId}
                    onChange={(event) => setPromoteActorId(event.target.value)}
                  />
                </ReviewField>
                <ReviewField id="bulk-promote-version" label="Version">
                  <Input
                    id="bulk-promote-version"
                    className="font-mono"
                    placeholder="optional"
                    value={promoteActorVersion}
                    onChange={(event) => setPromoteActorVersion(event.target.value)}
                  />
                </ReviewField>
                <Button
                  type="button"
                  onClick={() => void handleBulkPromote()}
                  disabled={acting || selectionSummary.promotable === 0}
                >
                  {acting ? <Spinner size={13} /> : <ArrowRight aria-hidden="true" />} Promote
                  selected
                </Button>
              </div>
            </CardContent>
          </Card>
        ) : null}

        {error ? (
          <Callout tone="danger" title="Review queue unavailable">
            {error}
          </Callout>
        ) : null}
        {loading ? (
          <Card size="sm">
            <div className="flex justify-center py-8 text-text-subtle">
              <Spinner size={18} />
            </div>
          </Card>
        ) : visibleItems.length === 0 ? (
          <EmptyState
            message="No items match the current filters."
            sub="Try widening the queue or restoring dismissed items."
          />
        ) : (
          <div className="grid gap-4 xl:grid-cols-[1.3fr_1fr]">
            <Card size="sm" className="overflow-hidden">
              <div className="flex items-center justify-between border-b border-border-strong px-4 py-3">
                <span className="flex items-center gap-2 text-sm font-medium">
                  <Filter className="size-4" aria-hidden="true" />
                  Review queue
                </span>
                <Button type="button" variant="outline" size="xs" onClick={toggleSelectAllVisible}>
                  {visibleItems.every((item) => selectedIds.has(item.revision.revision_id)) ? (
                    <CheckSquare aria-hidden="true" />
                  ) : (
                    <Square aria-hidden="true" />
                  )}{" "}
                  Toggle all
                </Button>
              </div>
              <div className="max-h-[calc(100vh-20rem)] overflow-auto">
                {visibleItems.map((item) => {
                  const active = focusedItem?.revision.revision_id === item.revision.revision_id;
                  const selected = selectedIds.has(item.revision.revision_id);
                  const dismissed = dismissedIds.has(item.revision.revision_id);
                  return (
                    <div
                      key={item.revision.revision_id}
                      className={`flex items-start gap-2 border-b border-border-soft p-3 last:border-b-0 ${active ? "bg-panel-hover-soft" : ""} ${dismissed && includeDismissed ? "opacity-60" : ""}`}
                    >
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        className={selected ? "text-status-doing" : "text-text-subtle"}
                        onClick={() => toggleSelection(item.revision.revision_id)}
                        aria-label={selected ? "Deselect item" : "Select item"}
                      >
                        {selected ? (
                          <CheckSquare aria-hidden="true" />
                        ) : (
                          <Square aria-hidden="true" />
                        )}
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        className="h-auto min-w-0 flex-1 flex-col items-start rounded-none p-0 text-left font-normal"
                        onClick={() => setFocusedId(item.revision.revision_id)}
                      >
                        <span className="flex flex-wrap gap-1.5">
                          <StatusBadge status={item.revision.status} />
                          <StatusBadge
                            status={`conf ${item.revision.confidence.toFixed(2)}`}
                            variant={item.revision.confidence < threshold ? "warn" : "ok"}
                          />
                          {dismissed && includeDismissed ? (
                            <StatusBadge status="dismissed" variant="muted" />
                          ) : null}
                        </span>
                        <span className="mt-2 whitespace-normal text-sm text-text">
                          {item.revision.payload.summary}
                        </span>
                        <span className="mt-1 max-w-full truncate font-mono text-xs text-text-subtle">
                          {item.revision.namespace} /{" "}
                          {item.revision.memory_key ?? item.revision.memory_id}
                        </span>
                        <span className="mt-2 flex flex-wrap gap-1.5">
                          {item.reviewReasons.slice(0, 3).map((reason) => (
                            <Pill key={reason} tone="neutral">
                              {reason}
                            </Pill>
                          ))}
                        </span>
                      </Button>
                    </div>
                  );
                })}
              </div>
            </Card>

            <Card size="sm" className="min-h-[31rem]">
              <CardContent>
                {focusedItem ? (
                  <div className="space-y-4">
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <StatusBadge status={focusedItem.revision.status} />
                          {focusedItem.revision.confidence < threshold ? (
                            <span className="flex items-center gap-1 text-xs text-status-paused">
                              <AlertTriangle className="size-3" aria-hidden="true" /> Low confidence
                            </span>
                          ) : (
                            <span className="flex items-center gap-1 text-xs text-status-done">
                              <ShieldAlert className="size-3" aria-hidden="true" /> Stable
                            </span>
                          )}
                        </div>
                        <p className="mt-2 text-base">{focusedItem.revision.payload.summary}</p>
                        <p className="mt-1 break-all font-mono text-xs text-text-subtle">
                          {focusedItem.revision.namespace} /{" "}
                          {focusedItem.revision.memory_key ?? focusedItem.revision.memory_id}
                        </p>
                      </div>
                      <div className="flex gap-2">
                        {onOpenItem && focusedItem.revision.memory_key ? (
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            onClick={() =>
                              onOpenItem(
                                focusedItem.revision.domain,
                                focusedItem.revision.namespace,
                                focusedItem.revision.memory_key ?? "",
                              )
                            }
                          >
                            <Eye aria-hidden="true" /> Open detail
                          </Button>
                        ) : null}
                        <Button
                          type="button"
                          variant="outline"
                          size="xs"
                          onClick={() => {
                            setDismissedIds((previous) =>
                              new Set(previous).add(focusedItem.revision.revision_id),
                            );
                            toast.success("Item dismissed from review queue");
                          }}
                        >
                          <X aria-hidden="true" /> Dismiss
                        </Button>
                      </div>
                    </div>
                    <section className="rounded-md border border-border-soft bg-panel-hover-soft p-3">
                      <h3 className="text-xs font-medium text-text-subtle">Why this is surfaced</h3>
                      <div className="mt-2 flex flex-wrap gap-1.5">
                        {focusedItem.reviewReasons.map((reason) => (
                          <Pill key={reason} tone="neutral">
                            {reason}
                          </Pill>
                        ))}
                      </div>
                    </section>
                    <div className="grid gap-3 sm:grid-cols-2">
                      <section className="rounded-md border border-border-soft bg-panel-hover-soft p-3">
                        <h3 className="text-xs font-medium text-text-subtle">Details</h3>
                        <dl className="mt-2 grid gap-1.5 text-xs text-text-subtle">
                          <Detail label="Revision" value={focusedItem.revision.revision_id} mono />
                          <Detail label="Memory" value={focusedItem.revision.memory_id} mono />
                          <Detail label="Author" value={focusedItem.revision.author.agent_id} />
                          <Detail label="Origin" value={focusedItem.revision.origin ?? "unknown"} />
                          <Detail
                            label="Created"
                            value={formatTimestamp(focusedItem.revision.created_at)}
                          />
                          {focusedItem.revision.supersedes ? (
                            <Detail
                              label="Supersedes"
                              value={focusedItem.revision.supersedes}
                              mono
                            />
                          ) : null}
                        </dl>
                      </section>
                      <section className="rounded-md border border-border-soft bg-panel-hover-soft p-3">
                        <h3 className="text-xs font-medium text-text-subtle">Tags</h3>
                        <div className="mt-2 flex flex-wrap gap-1.5">
                          {focusedItem.revision.tags.length > 0 ? (
                            focusedItem.revision.tags.map((tag) => (
                              <Pill key={tag} tone="neutral">
                                {tag}
                              </Pill>
                            ))
                          ) : (
                            <span className="text-xs text-text-subtle">No tags</span>
                          )}
                        </div>
                      </section>
                    </div>
                    <section>
                      <h3 className="text-xs font-medium text-text-subtle">Body</h3>
                      <pre
                        className={`mt-2 min-h-24 whitespace-pre-wrap rounded-md border border-border-soft bg-panel-hover-soft p-3 font-mono text-xs leading-5 ${focusedItem.revision.payload.body ? "text-text" : "text-text-subtle"}`}
                      >
                        {focusedItem.revision.payload.body || "No body content on this revision."}
                      </pre>
                    </section>
                    <section className="border-t border-border-strong pt-4">
                      <h3 className="flex items-center gap-2 text-sm font-medium">
                        <Edit3 className="size-4" aria-hidden="true" />
                        Clarify or update
                      </h3>
                      {!canClarify(focusedItem) ? (
                        <Callout tone="warning" title="Quick clarify unavailable">
                          This item has no memory key. Use the manual write flow instead.
                        </Callout>
                      ) : (
                        <div className="mt-3 space-y-3 rounded-md border border-border-soft bg-panel-hover-soft p-3">
                          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                            <ReviewField id="clarify-author" label="Author">
                              <Input
                                id="clarify-author"
                                className="font-mono"
                                value={clarifyAuthor}
                                onChange={(event) => setClarifyAuthor(event.target.value)}
                              />
                            </ReviewField>
                            <ReviewField id="clarify-version" label="Version">
                              <Input
                                id="clarify-version"
                                className="font-mono"
                                value={clarifyVersion}
                                onChange={(event) => setClarifyVersion(event.target.value)}
                              />
                            </ReviewField>
                            <ReviewField id="clarify-status" label="New status">
                              <Select
                                value={clarifyStatus}
                                onValueChange={(value) => {
                                  if (value) setClarifyStatus(value as MemoryStatus);
                                }}
                              >
                                <SelectTrigger id="clarify-status" className="w-full">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {ALL_STATUSES.filter((status) => status !== "deprecated").map(
                                    (status) => (
                                      <SelectItem key={status} value={status}>
                                        {status}
                                      </SelectItem>
                                    ),
                                  )}
                                </SelectContent>
                              </Select>
                            </ReviewField>
                            <ReviewField id="clarify-confidence" label="Confidence">
                              <Input
                                id="clarify-confidence"
                                className="font-mono"
                                type="number"
                                min="0"
                                max="1"
                                step="0.05"
                                value={clarifyConfidence}
                                onChange={(event) => setClarifyConfidence(event.target.value)}
                              />
                            </ReviewField>
                          </div>
                          <ReviewField id="clarify-summary" label="Summary">
                            <Textarea
                              id="clarify-summary"
                              rows={2}
                              value={clarifySummary}
                              onChange={(event) => setClarifySummary(event.target.value)}
                            />
                          </ReviewField>
                          <ReviewField id="clarify-body" label="Body">
                            <Textarea
                              id="clarify-body"
                              className="font-mono"
                              rows={6}
                              value={clarifyBody}
                              onChange={(event) => setClarifyBody(event.target.value)}
                            />
                          </ReviewField>
                          <div className="flex justify-end">
                            <Button
                              type="button"
                              onClick={() => void handleClarify()}
                              disabled={
                                clarifySubmitting || !clarifyAuthor.trim() || !clarifySummary.trim()
                              }
                            >
                              {clarifySubmitting ? (
                                <Spinner size={13} />
                              ) : (
                                <Edit3 aria-hidden="true" />
                              )}{" "}
                              Save clarification
                            </Button>
                          </div>
                        </div>
                      )}
                    </section>
                  </div>
                ) : (
                  <EmptyState
                    message="Choose a memory from the queue to inspect."
                    sub="Pick an item from the left to review it."
                  />
                )}
              </CardContent>
            </Card>
          </div>
        )}
      </div>
    </div>
  );
}

function ReviewField({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  );
}

function Detail({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="inline">{label}: </dt>
      <dd className={`inline text-text ${mono ? "font-mono" : ""}`}>{value}</dd>
    </div>
  );
}
