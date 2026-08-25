import {
  AlertTriangle,
  ArrowRight,
  CheckSquare,
  Edit3,
  Eye,
  Filter,
  Inbox,
  RefreshCw,
  ShieldAlert,
  Square,
  Trash2,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  tesseractLookup,
  listNamespaces,
  memoryDeprecate,
  memoryPromote,
  memoryWrite,
} from "../api/client";
import type { TesseractLookupResultItem, MemoryRevision, MemoryStatus } from "../api/types";
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

interface QueueItem extends TesseractLookupResultItem {
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
  if (revision.confidence < threshold) reasons.push(`Low confidence (${revision.confidence.toFixed(2)})`);
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

function isActionable(item: TesseractLookupResultItem, threshold: number): boolean {
  return getReviewReasons(item.revision, threshold).length > 0;
}

function isPromotable(item: TesseractLookupResultItem): boolean {
  return isSessionNamespace(item.revision.namespace) && item.revision.status !== "deprecated";
}

function canClarify(item: TesseractLookupResultItem): boolean {
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
      const nsResponse = await listNamespaces({ limit: 1000 });
      const allNamespaces = nsResponse.items.map((item) => item.namespace);
      setNamespaces(allNamespaces);

      if (allNamespaces.length === 0) {
        setItems([]);
        setFocusedId(null);
        return;
      }

      const parsedLimit = parseInt(resultLimit, 10);
      const baseLimit = Number.isFinite(parsedLimit) && parsedLimit > 0 ? parsedLimit : 180;
      const fetchLimit = Math.min(Math.max(baseLimit * 2, 100), 500);
      const domainFilter = domain === "both" ? undefined : [domain];

      const headReq: Parameters<typeof tesseractLookup>[0] = {
        namespaces: allNamespaces,
        ranking: "activation",
        revision_scope: "current",
        statuses: ["draft", "reviewed", "canonical"],
        limit: fetchLimit,
      };
      const deprecatedReq: Parameters<typeof tesseractLookup>[0] = {
        namespaces: allNamespaces,
        ranking: "chronological",
        revision_scope: "timeline",
        statuses: ["deprecated"],
        limit: Math.min(Math.max(Math.floor(baseLimit / 2), 40), 150),
      };
      if (domainFilter) {
        headReq.domains = domainFilter;
        deprecatedReq.domains = domainFilter;
      }

      const [heads, deprecated] = await Promise.all([
        tesseractLookup(headReq),
        tesseractLookup(deprecatedReq),
      ]);

      const latestDeprecatedByMemory = new Map<string, TesseractLookupResultItem>();
      for (const item of deprecated.results) {
        const existing = latestDeprecatedByMemory.get(item.revision.memory_id);
        if (!existing || item.revision.created_at > existing.revision.created_at) {
          latestDeprecatedByMemory.set(item.revision.memory_id, item);
        }
      }

      const merged = [...heads.results, ...latestDeprecatedByMemory.values()];
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
        const haystack = `${item.revision.namespace} ${item.revision.memory_key ?? ""} ${item.revision.payload.summary}`.toLowerCase();
        if (!haystack.includes(q)) return false;
      }
      return true;
    });
  }, [dismissedIds, includeDismissed, items, mode, namespaceFilter, threshold]);

  const focusedItem = useMemo(
    () => visibleItems.find((item) => item.revision.revision_id === focusedId) ?? visibleItems[0] ?? null,
    [focusedId, visibleItems],
  );

  useEffect(() => {
    if (!focusedItem) return;
    setFocusedId(focusedItem.revision.revision_id);
  }, [focusedItem]);

  useEffect(() => {
    if (!focusedItem) return;
    setClarifyStatus(focusedItem.revision.status === "deprecated" ? "reviewed" : focusedItem.revision.status);
    setClarifyConfidence(String(focusedItem.revision.confidence.toFixed(2)));
    setClarifySummary(focusedItem.revision.payload.summary);
    setClarifyBody(focusedItem.revision.payload.body ?? "");
  }, [focusedItem?.revision.revision_id]);

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
        visibleIds.forEach((id) => next.delete(id));
      } else {
        visibleIds.forEach((id) => next.add(id));
      }
      return next;
    });
  };

  const dismissSelected = () => {
    if (selectedIds.size === 0) return;
    const next = new Set(dismissedIds);
    selectedIds.forEach((id) => next.add(id));
    setDismissedIds(next);
    setSelectedIds(new Set());
    toast.success(`Dismissed ${selectionSummary.total} item${selectionSummary.total === 1 ? "" : "s"} from the review queue`);
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
    if (!window.confirm(`Deprecate ${targets.length} selected item${targets.length === 1 ? "" : "s"}?`)) {
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
    const targets = visibleItems.filter((item) => selectedIds.has(item.revision.revision_id) && isPromotable(item));
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
        confidence: Number.isFinite(parsedConfidence) ? parsedConfidence : focusedItem.revision.confidence,
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
    <div>
      <div className="page-header">
        <h2 className="page-title">Memory Review</h2>
        <div className="page-actions" style={{ display: "flex", gap: "0.4rem" }}>
          {onOpenWrite && (
            <button type="button" className="hud-button-ghost" onClick={onOpenWrite}>
              Manual Write
            </button>
          )}
          <button type="button" className="hud-button-ghost" onClick={restoreDismissed} disabled={dismissedIds.size === 0}>
            Restore Dismissed
          </button>
          <button type="button" className="hud-button-primary" onClick={() => void loadQueue()} disabled={loading || acting}>
            <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
              {loading ? <Spinner size={13} /> : <RefreshCw size={13} />} Refresh Queue
            </span>
          </button>
        </div>
      </div>

      {activePreset && (
        <div
          className="hud-panel"
          style={{
            padding: "0.75rem 0.9rem",
            marginBottom: "0.9rem",
            borderColor: "rgba(var(--primary) / 0.4)",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: "0.75rem",
          }}
        >
          <div style={{ fontSize: "0.8rem", color: "rgb(var(--text))" }}>
            Preset active:{" "}
            <span style={{ color: "rgb(var(--primary))" }}>
              {activePreset === "lowConfidence"
                ? "Low Confidence"
                : activePreset === "reviewed"
                  ? "Reviewed"
                  : "Pending Review"}
            </span>
          </div>
          <button type="button" className="hud-button-ghost" onClick={() => setActivePreset(null)}>
            Clear Preset
          </button>
        </div>
      )}

      <div className="hud-panel" style={{ padding: "0.9rem", marginBottom: "0.9rem" }}>
        <div style={{ display: "grid", gap: "0.75rem", gridTemplateColumns: "2fr 1fr 1fr 1fr 1fr" }}>
          <div className="form-field">
            <label className="hud-label" htmlFor="review-filter">
              Filter
            </label>
            <input
              id="review-filter"
              className="hud-input"
              placeholder="Namespace, key, summary"
              value={namespaceFilter}
              onChange={(e) => setNamespaceFilter(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="review-limit">
              Queue Size
            </label>
            <input
              id="review-limit"
              className="hud-input"
              type="number"
              min="25"
              max="500"
              value={resultLimit}
              onChange={(e) => setResultLimit(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="review-threshold">
              Low Confidence
            </label>
            <input
              id="review-threshold"
              className="hud-input"
              type="number"
              min="0"
              max="1"
              step="0.05"
              value={confidenceThreshold}
              onChange={(e) => setConfidenceThreshold(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label">Domain</label>
            <div style={{ display: "flex", gap: "0.25rem" }}>
              {(["both", "memory", "knowledge"] as const).map((value) => (
                <button
                  key={value}
                  type="button"
                  className={domain === value ? "hud-button-primary" : "hud-button-ghost"}
                  onClick={() => setDomain(value)}
                >
                  {value}
                </button>
              ))}
            </div>
          </div>
          <div className="form-field">
            <label className="hud-label">Mode</label>
            <div style={{ display: "flex", gap: "0.25rem", flexWrap: "wrap" }}>
              {(["actionable", "all"] as const).map((value) => (
                <button
                  key={value}
                  type="button"
                  className={mode === value ? "hud-button-primary" : "hud-button-ghost"}
                  onClick={() => setMode(value)}
                >
                  {value}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div style={{ display: "flex", justifyContent: "space-between", gap: "1rem", marginTop: "0.8rem", flexWrap: "wrap" }}>
          <div style={{ display: "flex", gap: "0.4rem", alignItems: "center", flexWrap: "wrap" }}>
            <StatusBadge status={`draft ${counts.draft}`} variant="warn" />
            <StatusBadge status={`reviewed ${counts.reviewed}`} variant="primary" />
            <StatusBadge status={`canonical ${counts.canonical}`} variant="ok" />
            <StatusBadge status={`deprecated ${counts.deprecated}`} variant="muted" />
            <span style={{ color: "rgb(var(--muted))", fontSize: "0.75rem" }}>
              {visibleItems.length} shown across {namespaces.length} namespaces
            </span>
          </div>

          <label style={{ display: "flex", alignItems: "center", gap: "0.4rem", fontSize: "0.8rem", color: "rgb(var(--muted))" }}>
            <input
              type="checkbox"
              checked={includeDismissed}
              onChange={(e) => setIncludeDismissed(e.target.checked)}
              style={{ accentColor: "rgb(var(--primary))" }}
            />
            Include dismissed
          </label>
        </div>
      </div>

      {selectedIds.size > 0 && (
        <div className="hud-panel" style={{ padding: "0.9rem", marginBottom: "0.9rem", borderColor: "rgba(var(--primary) / 0.45)" }}>
          <div style={{ display: "flex", justifyContent: "space-between", gap: "1rem", alignItems: "center", flexWrap: "wrap", marginBottom: "0.8rem" }}>
            <div>
              <div style={{ fontSize: "0.9rem", color: "rgb(var(--text))" }}>
                {selectionSummary.total} selected
              </div>
              <div style={{ fontSize: "0.75rem", color: "rgb(var(--muted))" }}>
                {selectionSummary.promotable} promotable · {selectionSummary.deprecatable} deprecatable
              </div>
            </div>
            <div style={{ display: "flex", gap: "0.4rem", flexWrap: "wrap" }}>
              <button type="button" className="hud-button-ghost" onClick={() => setSelectedIds(new Set())}>
                Clear Selection
              </button>
              <button type="button" className="hud-button-ghost" onClick={dismissSelected}>
                Dismiss
              </button>
              <button type="button" className="hud-button-danger" onClick={() => void handleBulkDeprecate()} disabled={acting || selectionSummary.deprecatable === 0}>
                <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                  {acting ? <Spinner size={13} /> : <Trash2 size={13} />} Deprecate Selected
                </span>
              </button>
            </div>
          </div>

          <div style={{ display: "grid", gap: "0.75rem", gridTemplateColumns: "2fr 1fr 1fr auto" }}>
            <div className="form-field">
              <label className="hud-label" htmlFor="bulk-promote-target">
                Promote To Namespace
              </label>
              <input
                id="bulk-promote-target"
                className="hud-input"
                placeholder="user/<actor>/memory or user/<actor>/project/<id>/memory"
                value={promoteTargetNamespace}
                onChange={(e) => setPromoteTargetNamespace(e.target.value)}
                style={{ width: "100%" }}
              />
            </div>
            <div className="form-field">
              <label className="hud-label" htmlFor="bulk-promote-actor">
                Actor
              </label>
              <input
                id="bulk-promote-actor"
                className="hud-input"
                value={promoteActorId}
                onChange={(e) => setPromoteActorId(e.target.value)}
                style={{ width: "100%" }}
              />
            </div>
            <div className="form-field">
              <label className="hud-label" htmlFor="bulk-promote-version">
                Version
              </label>
              <input
                id="bulk-promote-version"
                className="hud-input"
                placeholder="optional"
                value={promoteActorVersion}
                onChange={(e) => setPromoteActorVersion(e.target.value)}
                style={{ width: "100%" }}
              />
            </div>
            <div className="form-field" style={{ alignSelf: "end" }}>
              <button type="button" className="hud-button-primary" onClick={() => void handleBulkPromote()} disabled={acting || selectionSummary.promotable === 0}>
                <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                  {acting ? <Spinner size={13} /> : <ArrowRight size={13} />} Promote Selected
                </span>
              </button>
            </div>
          </div>
        </div>
      )}

      {error && (
        <div className="hud-panel" style={{ padding: "0.75rem", marginBottom: "0.9rem", borderColor: "rgba(var(--danger) / 0.4)", color: "rgb(var(--danger))" }}>
          {error}
        </div>
      )}

      {loading ? (
        <div className="hud-panel" style={{ padding: "2rem", display: "flex", justifyContent: "center" }}>
          <Spinner size={18} />
        </div>
      ) : visibleItems.length === 0 ? (
        <EmptyState
          icon={<Inbox size={18} />}
          message="No items match the current filters. Try widening the queue or restoring dismissed items."
          sub="Try widening the queue or restoring dismissed items."
        />
      ) : (
        <div style={{ display: "grid", gap: "1rem", gridTemplateColumns: "1.3fr 1fr" }}>
          <div className="hud-panel" style={{ overflow: "hidden" }}>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", padding: "0.75rem 0.9rem", borderBottom: "1px solid rgb(var(--border))" }}>
              <div style={{ display: "flex", alignItems: "center", gap: "0.45rem", fontSize: "0.85rem" }}>
                <Filter size={13} />
                Review Queue
              </div>
              <button type="button" className="hud-button-ghost" onClick={toggleSelectAllVisible}>
                <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                  {visibleItems.length > 0 && visibleItems.every((item) => selectedIds.has(item.revision.revision_id)) ? <CheckSquare size={12} /> : <Square size={12} />}
                  Toggle All
                </span>
              </button>
            </div>

            <div style={{ maxHeight: "calc(100vh - 320px)", overflow: "auto" }}>
              {visibleItems.map((item) => {
                const active = focusedItem?.revision.revision_id === item.revision.revision_id;
                const selected = selectedIds.has(item.revision.revision_id);
                const dismissed = dismissedIds.has(item.revision.revision_id);
                return (
                  <div
                    key={item.revision.revision_id}
                    style={{
                      padding: "0.85rem 0.9rem",
                      borderBottom: "1px solid rgba(var(--border) / 0.6)",
                      background: active ? "rgba(var(--primary) / 0.07)" : "transparent",
                      opacity: dismissed && includeDismissed ? 0.65 : 1,
                    }}
                  >
                    <div style={{ display: "flex", gap: "0.75rem", alignItems: "flex-start" }}>
                      <button
                        type="button"
                        onClick={() => toggleSelection(item.revision.revision_id)}
                        style={{
                          marginTop: "0.05rem",
                          border: "none",
                          background: "none",
                          color: selected ? "rgb(var(--primary))" : "rgb(var(--muted))",
                          cursor: "pointer",
                        }}
                        aria-label={selected ? "Deselect item" : "Select item"}
                      >
                        {selected ? <CheckSquare size={15} /> : <Square size={15} />}
                      </button>

                      <button
                        type="button"
                        onClick={() => setFocusedId(item.revision.revision_id)}
                        style={{ flex: 1, background: "none", border: "none", color: "inherit", textAlign: "left", cursor: "pointer" }}
                      >
                        <div style={{ display: "flex", gap: "0.45rem", alignItems: "center", flexWrap: "wrap", marginBottom: "0.35rem" }}>
                          <StatusBadge status={item.revision.status} />
                          {item.revision.confidence < threshold ? (
                            <StatusBadge status={`conf ${item.revision.confidence.toFixed(2)}`} variant="warn" />
                          ) : (
                            <StatusBadge status={`conf ${item.revision.confidence.toFixed(2)}`} variant="ok" />
                          )}
                          {dismissed && includeDismissed && <StatusBadge status="dismissed" variant="muted" />}
                        </div>

                        <div style={{ fontSize: "0.9rem", color: "rgb(var(--text))", marginBottom: "0.2rem" }}>
                          {item.revision.payload.summary}
                        </div>
                        <div style={{ fontSize: "0.72rem", color: "rgb(var(--muted))", fontFamily: "var(--font-mono)" }}>
                          {item.revision.namespace} / {item.revision.memory_key ?? item.revision.memory_id}
                        </div>

                        <div style={{ display: "flex", flexWrap: "wrap", gap: "0.3rem", marginTop: "0.45rem" }}>
                          {item.reviewReasons.slice(0, 3).map((reason) => (
                            <span
                              key={reason}
                              style={{
                                padding: "0.15rem 0.35rem",
                                borderRadius: "999px",
                                border: "1px solid rgba(var(--border) / 0.9)",
                                fontSize: "0.68rem",
                                color: "rgb(var(--muted))",
                              }}
                            >
                              {reason}
                            </span>
                          ))}
                        </div>
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>

          <div className="hud-panel" style={{ padding: "0.95rem", minHeight: 500 }}>
            {focusedItem ? (
              <>
                <div style={{ display: "flex", justifyContent: "space-between", gap: "0.75rem", alignItems: "flex-start", marginBottom: "0.8rem" }}>
                  <div>
                    <div style={{ display: "flex", gap: "0.4rem", alignItems: "center", flexWrap: "wrap", marginBottom: "0.35rem" }}>
                      <StatusBadge status={focusedItem.revision.status} />
                      {focusedItem.revision.confidence < threshold ? (
                        <span style={{ display: "flex", alignItems: "center", gap: "0.25rem", color: "rgb(var(--warn))", fontSize: "0.78rem" }}>
                          <AlertTriangle size={12} /> Low confidence
                        </span>
                      ) : (
                        <span style={{ display: "flex", alignItems: "center", gap: "0.25rem", color: "rgb(var(--ok))", fontSize: "0.78rem" }}>
                          <ShieldAlert size={12} /> Stable
                        </span>
                      )}
                    </div>
                    <div style={{ fontSize: "1rem", marginBottom: "0.25rem" }}>
                      {focusedItem.revision.payload.summary}
                    </div>
                    <div style={{ fontSize: "0.72rem", color: "rgb(var(--muted))", fontFamily: "var(--font-mono)" }}>
                      {focusedItem.revision.namespace} / {focusedItem.revision.memory_key ?? focusedItem.revision.memory_id}
                    </div>
                  </div>

                  <div style={{ display: "flex", gap: "0.4rem", flexWrap: "wrap", justifyContent: "flex-end" }}>
                    {onOpenItem && focusedItem.revision.memory_key && (
                      <button
                        type="button"
                        className="hud-button-ghost"
                        onClick={() =>
                          onOpenItem(
                            focusedItem.revision.domain,
                            focusedItem.revision.namespace,
                            focusedItem.revision.memory_key!,
                          )
                        }
                      >
                        <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                          <Eye size={12} /> Open Detail
                        </span>
                      </button>
                    )}
                    <button
                      type="button"
                      className="hud-button-ghost"
                      onClick={() => {
                        setDismissedIds((prev) => {
                          const next = new Set(prev);
                          next.add(focusedItem.revision.revision_id);
                          return next;
                        });
                        toast.success("Item dismissed from review queue");
                      }}
                    >
                      <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                        <X size={12} /> Dismiss
                      </span>
                    </button>
                  </div>
                </div>

                <div className="hud-panel2" style={{ padding: "0.75rem", marginBottom: "0.8rem" }}>
                  <div style={{ fontSize: "0.72rem", color: "rgb(var(--muted))", textTransform: "uppercase", letterSpacing: "0.08em", marginBottom: "0.35rem" }}>
                    Why This Is Surfaced
                  </div>
                  <div style={{ display: "flex", flexWrap: "wrap", gap: "0.35rem" }}>
                    {focusedItem.reviewReasons.map((reason) => (
                      <span
                        key={reason}
                        style={{
                          padding: "0.2rem 0.4rem",
                          borderRadius: "999px",
                          background: "rgba(var(--panel) / 0.9)",
                          border: "1px solid rgba(var(--border) / 0.9)",
                          fontSize: "0.7rem",
                          color: "rgb(var(--text))",
                        }}
                      >
                        {reason}
                      </span>
                    ))}
                  </div>
                </div>

                <div style={{ display: "grid", gap: "0.75rem", gridTemplateColumns: "1fr 1fr" }}>
                  <div className="hud-panel2" style={{ padding: "0.75rem" }}>
                    <div className="hud-label">Details</div>
                    <div style={{ display: "grid", gap: "0.35rem", fontSize: "0.78rem", color: "rgb(var(--muted))" }}>
                      <div>Revision: <span style={{ color: "rgb(var(--text))", fontFamily: "var(--font-mono)" }}>{focusedItem.revision.revision_id}</span></div>
                      <div>Memory: <span style={{ color: "rgb(var(--text))", fontFamily: "var(--font-mono)" }}>{focusedItem.revision.memory_id}</span></div>
                      <div>Author: <span style={{ color: "rgb(var(--text))" }}>{focusedItem.revision.author.agent_id}</span></div>
                      <div>Origin: <span style={{ color: "rgb(var(--text))" }}>{focusedItem.revision.origin ?? "unknown"}</span></div>
                      <div>Created: <span style={{ color: "rgb(var(--text))" }}>{formatTimestamp(focusedItem.revision.created_at)}</span></div>
                      {focusedItem.revision.supersedes && (
                        <div>Supersedes: <span style={{ color: "rgb(var(--text))", fontFamily: "var(--font-mono)" }}>{focusedItem.revision.supersedes}</span></div>
                      )}
                    </div>
                  </div>

                  <div className="hud-panel2" style={{ padding: "0.75rem" }}>
                    <div className="hud-label">Tags</div>
                    {focusedItem.revision.tags.length > 0 ? (
                      <div style={{ display: "flex", flexWrap: "wrap", gap: "0.35rem" }}>
                        {focusedItem.revision.tags.map((tag) => (
                          <span
                            key={tag}
                            style={{
                              padding: "0.2rem 0.4rem",
                              borderRadius: "999px",
                              border: "1px solid rgba(var(--border) / 0.9)",
                              fontSize: "0.68rem",
                              color: "rgb(var(--muted))",
                            }}
                          >
                            {tag}
                          </span>
                        ))}
                      </div>
                    ) : (
                      <div style={{ fontSize: "0.78rem", color: "rgb(var(--muted))" }}>No tags</div>
                    )}
                  </div>
                </div>

                <div style={{ marginTop: "0.8rem" }}>
                  <div className="hud-label">Body</div>
                  <div className="hud-panel2" style={{ padding: "0.75rem", minHeight: 90, whiteSpace: "pre-wrap", fontSize: "0.82rem", color: focusedItem.revision.payload.body ? "rgb(var(--text))" : "rgb(var(--muted))" }}>
                    {focusedItem.revision.payload.body || "No body content on this revision."}
                  </div>
                </div>

                <div style={{ marginTop: "0.9rem" }}>
                  <div style={{ display: "flex", alignItems: "center", gap: "0.35rem", marginBottom: "0.6rem" }}>
                    <Edit3 size={14} />
                    <span style={{ fontSize: "0.85rem" }}>Clarify / Update</span>
                  </div>
                  {!canClarify(focusedItem) ? (
                    <div className="hud-panel2" style={{ padding: "0.75rem", color: "rgb(var(--muted))", fontSize: "0.78rem" }}>
                      Quick clarify currently requires a keyed memory. This item has no `memory_key`, so use a manual write flow instead.
                    </div>
                  ) : (
                    <div className="hud-panel2" style={{ padding: "0.85rem" }}>
                      <div className="form-grid" style={{ gridTemplateColumns: "1fr 1fr 1fr 1fr" }}>
                        <div className="form-field">
                          <label className="hud-label" htmlFor="clarify-author">
                            Author
                          </label>
                          <input
                            id="clarify-author"
                            className="hud-input"
                            value={clarifyAuthor}
                            onChange={(e) => setClarifyAuthor(e.target.value)}
                            style={{ width: "100%" }}
                          />
                        </div>
                        <div className="form-field">
                          <label className="hud-label" htmlFor="clarify-version">
                            Version
                          </label>
                          <input
                            id="clarify-version"
                            className="hud-input"
                            value={clarifyVersion}
                            onChange={(e) => setClarifyVersion(e.target.value)}
                            style={{ width: "100%" }}
                          />
                        </div>
                        <div className="form-field">
                          <label className="hud-label" htmlFor="clarify-status">
                            New Status
                          </label>
                          <select
                            id="clarify-status"
                            className="hud-input"
                            value={clarifyStatus}
                            onChange={(e) => setClarifyStatus(e.target.value as MemoryStatus)}
                            style={{ width: "100%" }}
                          >
                            {ALL_STATUSES.filter((status) => status !== "deprecated").map((status) => (
                              <option key={status} value={status}>
                                {status}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className="form-field">
                          <label className="hud-label" htmlFor="clarify-confidence">
                            Confidence
                          </label>
                          <input
                            id="clarify-confidence"
                            className="hud-input"
                            type="number"
                            min="0"
                            max="1"
                            step="0.05"
                            value={clarifyConfidence}
                            onChange={(e) => setClarifyConfidence(e.target.value)}
                            style={{ width: "100%" }}
                          />
                        </div>
                      </div>

                      <div className="form-field" style={{ marginTop: "0.6rem" }}>
                        <label className="hud-label" htmlFor="clarify-summary">
                          Summary
                        </label>
                        <textarea
                          id="clarify-summary"
                          className="hud-textarea"
                          rows={2}
                          value={clarifySummary}
                          onChange={(e) => setClarifySummary(e.target.value)}
                          style={{ width: "100%" }}
                        />
                      </div>

                      <div className="form-field" style={{ marginTop: "0.6rem" }}>
                        <label className="hud-label" htmlFor="clarify-body">
                          Body
                        </label>
                        <textarea
                          id="clarify-body"
                          className="hud-textarea"
                          rows={6}
                          value={clarifyBody}
                          onChange={(e) => setClarifyBody(e.target.value)}
                          style={{ width: "100%" }}
                        />
                      </div>

                      <div style={{ display: "flex", justifyContent: "flex-end", marginTop: "0.75rem" }}>
                        <button
                          type="button"
                          className="hud-button-primary"
                          onClick={() => void handleClarify()}
                          disabled={clarifySubmitting || !clarifyAuthor.trim() || !clarifySummary.trim()}
                        >
                          <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                            {clarifySubmitting ? <Spinner size={13} /> : <Edit3 size={13} />} Save Clarification
                          </span>
                        </button>
                      </div>
                    </div>
                  )}
                </div>
              </>
            ) : (
              <EmptyState
                icon={<Inbox size={18} />}
                message="Choose a memory from the queue to inspect, update, promote, deprecate, or dismiss."
                sub="Pick an item from the left to review it."
              />
            )}
          </div>
        </div>
      )}
    </div>
  );
}
