import { Tag, Telescope } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { recall } from "../api/client";
import type { RecallBriefItem, RecallResponse } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { JsonViewer } from "../components/ui/JsonViewer";
import { Spinner } from "../components/ui/Spinner";

// Common namespace patterns surfaced in the suggestion dropdown.
// Picked from the placeholders that already appear across PacketBuilder /
// ViewBuilder / Broker / Promote so operators see the same shapes.
const NAMESPACE_SUGGESTIONS = [
  "user/<actor>/memory",
  "user/<actor>/knowledge",
  "user/<actor>/cache",
  "user/<actor>/pins",
  "user/<actor>/session",
  "app/<id>/cache",
  "app/<id>/draft",
];

const RECENT_NAMESPACES_KEY = "tesseract.recall.recentNamespaces";
const RECENT_NAMESPACES_MAX = 8;

function loadRecentNamespaces(): string[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(RECENT_NAMESPACES_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((s) => typeof s === "string") : [];
  } catch {
    return [];
  }
}

function pushRecentNamespace(ns: string): string[] {
  const current = loadRecentNamespaces();
  const next = [ns, ...current.filter((n) => n !== ns)].slice(0, RECENT_NAMESPACES_MAX);
  try {
    window.localStorage.setItem(RECENT_NAMESPACES_KEY, JSON.stringify(next));
  } catch {
    // localStorage may be unavailable; ignore.
  }
  return next;
}

interface Props {
  // Routes a recall result to the correct detail page based on its domain.
  // Memory results need MemoryDetailPage (uses /v1/memory/current); knowledge
  // results need KnowledgeDetailPage (uses /v1/knowledge/current).
  onOpenItem?: (domain: "memory" | "knowledge", namespace: string, key: string) => void;
}

// Read recall parameters out of the URL hash so a recall page is shareable.
// Format: #recall?namespace=…&tags=…&limit=…&format=…&domain=…
function readHashParams(): {
  namespace?: string;
  tags?: string;
  limit?: string;
  format?: "brief" | "full";
  domain?: "memory" | "knowledge";
} {
  if (typeof window === "undefined") return {};
  const hash = window.location.hash;
  const idx = hash.indexOf("?");
  if (idx < 0) return {};
  const params = new URLSearchParams(hash.slice(idx + 1));
  const out: ReturnType<typeof readHashParams> = {};
  const ns = params.get("namespace");
  if (ns) out.namespace = ns;
  const t = params.get("tags");
  if (t) out.tags = t;
  const l = params.get("limit");
  if (l) out.limit = l;
  const f = params.get("format");
  if (f === "brief" || f === "full") out.format = f;
  const d = params.get("domain");
  if (d === "memory" || d === "knowledge") out.domain = d;
  return out;
}

function writeHashParams(p: {
  namespace: string;
  tags: string;
  limit: string;
  format: string;
  domain: string;
}): void {
  if (typeof window === "undefined") return;
  const params = new URLSearchParams();
  if (p.namespace) params.set("namespace", p.namespace);
  if (p.tags) params.set("tags", p.tags);
  if (p.limit && p.limit !== "15") params.set("limit", p.limit);
  if (p.format && p.format !== "brief") params.set("format", p.format);
  if (p.domain) params.set("domain", p.domain);
  const qs = params.toString();
  const newHash = qs ? `#recall?${qs}` : "#recall";
  if (window.location.hash !== newHash) {
    history.replaceState(null, "", newHash);
  }
}

export function RecallPage({ onOpenItem }: Props) {
  const initial = readHashParams();
  const [namespace, setNamespace] = useState(initial.namespace ?? "");
  const [tags, setTags] = useState(initial.tags ?? "");
  const [limit, setLimit] = useState(initial.limit ?? "15");
  const [format, setFormat] = useState<"brief" | "full">(initial.format ?? "brief");
  const [domainFilter, setDomainFilter] = useState<"memory" | "knowledge" | "">(
    initial.domain ?? "",
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [response, setResponse] = useState<RecallResponse | null>(null);
  const [recentNamespaces, setRecentNamespaces] = useState<string[]>([]);

  useEffect(() => {
    setRecentNamespaces(loadRecentNamespaces());
  }, []);

  // Mirror current form state into the URL hash so the page is bookmarkable
  // and shareable. Skipped while loading to avoid intermediate-state churn.
  useEffect(() => {
    writeHashParams({ namespace, tags, limit, format, domain: domainFilter });
  }, [namespace, tags, limit, format, domainFilter]);

  const handleRecall = async () => {
    const ns = namespace.trim();
    if (!ns) {
      setError("Namespace is required.");
      toast.error("Namespace is required");
      return;
    }
    setLoading(true);
    setError(null);
    setResponse(null);
    try {
      const tagList = tags.trim()
        ? tags
            .split(",")
            .map((t) => t.trim())
            .filter(Boolean)
        : undefined;
      const parsedLimit = parseInt(limit, 10);
      const params: Parameters<typeof recall>[0] = {
        namespace: ns,
        format,
      };
      if (tagList) params.tags = tagList;
      if (Number.isFinite(parsedLimit) && parsedLimit > 0) params.limit = parsedLimit;
      const res = await recall(params);
      setResponse(res);
      setRecentNamespaces(pushRecentNamespace(ns));
      toast.success(`Returned ${res.meta.returned} result${res.meta.returned === 1 ? "" : "s"}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Recall failed: ${msg}`);
    } finally {
      setLoading(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") handleRecall();
  };

  // When the user clicks a domain facet chip we filter the brief result list
  // client-side. The /v1/recall API's domains arg is a tag-style filter on the
  // memory ranker; doing it client-side keeps the round-trip out of the loop
  // and lets the user toggle freely without re-fetching.
  const allBriefItems: RecallBriefItem[] =
    format === "brief" && response ? (response.results as RecallBriefItem[]) : [];
  const briefItems: RecallBriefItem[] = domainFilter
    ? allBriefItems.filter((item) => item.domain === domainFilter)
    : allBriefItems;

  const facetEntries = response ? Object.entries(response.facets.domains ?? {}) : [];

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Recall</h2>
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: response ? "1fr 1.4fr" : "1fr",
          gap: "1rem",
        }}
      >
        {/* ── Form ───────────────────────────────────────── */}
        <div className="hud-panel" style={{ padding: "1rem" }}>
          <div className="form-field">
            <label className="hud-label" htmlFor="recall-namespace">
              Namespace <span style={{ color: "rgb(var(--danger))" }}>*</span>
            </label>
            <input
              id="recall-namespace"
              className="hud-input"
              list="recall-namespace-suggestions"
              placeholder="user/chrispian/memory"
              value={namespace}
              onChange={(e) => setNamespace(e.target.value)}
              onKeyDown={handleKeyDown}
              style={{ width: "100%" }}
            />
            <datalist id="recall-namespace-suggestions">
              {recentNamespaces.map((ns) => (
                <option key={`recent-${ns}`} value={ns} label="recent" />
              ))}
              {NAMESPACE_SUGGESTIONS.map((ns) => (
                <option key={ns} value={ns} label="template" />
              ))}
            </datalist>
            <div style={{ fontSize: "0.7rem", color: "rgb(var(--muted))", marginTop: "0.2rem" }}>
              Single namespace only. Recent picks appear first; templates use `&lt;actor&gt;` /
              `&lt;id&gt;` placeholders to replace.
            </div>
          </div>

          <div className="form-field" style={{ marginTop: "0.75rem" }}>
            <label className="hud-label" htmlFor="recall-tags">
              Tags <span style={{ color: "rgb(var(--muted))" }}>(optional, comma-separated)</span>
            </label>
            <input
              id="recall-tags"
              className="hud-input"
              placeholder="decision, scope:agent-ops.steward.main"
              value={tags}
              onChange={(e) => setTags(e.target.value)}
              onKeyDown={handleKeyDown}
              style={{ width: "100%" }}
            />
          </div>

          <div className="form-grid" style={{ marginTop: "0.75rem" }}>
            <div className="form-field">
              <label className="hud-label" htmlFor="recall-limit">
                Limit
              </label>
              <input
                id="recall-limit"
                className="hud-input"
                type="number"
                min="1"
                max="500"
                value={limit}
                onChange={(e) => setLimit(e.target.value)}
                onKeyDown={handleKeyDown}
                style={{ width: "100%" }}
              />
            </div>
            <div className="form-field">
              <span className="hud-label">Format</span>
              <div
                style={{
                  display: "flex",
                  gap: "0.5rem",
                  alignItems: "center",
                  paddingTop: "0.3rem",
                }}
              >
                {(["brief", "full"] as const).map((f) => (
                  <label
                    key={f}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: "0.3rem",
                      cursor: "pointer",
                      fontSize: "0.85rem",
                    }}
                  >
                    <input
                      type="radio"
                      name="recall-format"
                      value={f}
                      checked={format === f}
                      onChange={() => setFormat(f)}
                      style={{ accentColor: "rgb(var(--primary))" }}
                    />
                    {f}
                  </label>
                ))}
              </div>
            </div>
          </div>

          <div style={{ display: "flex", gap: "0.5rem", marginTop: "0.75rem" }}>
            <button
              type="button"
              className="hud-button-primary"
              onClick={handleRecall}
              disabled={loading || !namespace.trim()}
            >
              <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                {loading ? <Spinner size={13} /> : <Telescope size={13} />} Recall
              </span>
            </button>
          </div>
        </div>

        {/* ── Results ───────────────────────────────────── */}
        {response && (
          <div>
            {/* Meta header */}
            <div className="hud-panel" style={{ padding: "0.75rem", marginBottom: "0.75rem" }}>
              <div
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  flexWrap: "wrap",
                  gap: "0.5rem",
                }}
              >
                <div style={{ fontSize: "0.85rem" }}>
                  <span style={{ color: "rgb(var(--muted))" }}>namespace </span>
                  <span style={{ fontFamily: "var(--font-mono)" }}>{response.meta.namespace}</span>
                </div>
                <div style={{ fontSize: "0.75rem", color: "rgb(var(--muted))" }}>
                  returned {response.meta.returned} / limit {response.meta.limit} · format{" "}
                  {response.meta.format}
                </div>
              </div>
              {facetEntries.length > 0 && (
                <div
                  style={{
                    display: "flex",
                    gap: "0.4rem",
                    marginTop: "0.5rem",
                    flexWrap: "wrap",
                    alignItems: "center",
                  }}
                >
                  {facetEntries.map(([domain, count]) => {
                    const isActive = domainFilter === domain;
                    const isFilterable = domain === "memory" || domain === "knowledge";
                    return (
                      <button
                        type="button"
                        key={domain}
                        onClick={() => {
                          if (!isFilterable) return;
                          setDomainFilter(isActive ? "" : (domain as "memory" | "knowledge"));
                        }}
                        disabled={!isFilterable}
                        title={
                          isFilterable
                            ? isActive
                              ? `Clear ${domain} filter`
                              : `Filter to ${domain} only`
                            : undefined
                        }
                        style={{
                          padding: "0.15rem 0.5rem",
                          background: isActive
                            ? "rgb(var(--primary))"
                            : "rgba(var(--primary) / 0.08)",
                          border: "1px solid rgba(var(--primary) / 0.3)",
                          borderRadius: "var(--radius-sm)",
                          fontSize: "0.7rem",
                          fontFamily: "var(--font-mono)",
                          color: isActive ? "rgb(var(--bg))" : "inherit",
                          cursor: isFilterable ? "pointer" : "default",
                        }}
                      >
                        {domain} · {count}
                      </button>
                    );
                  })}
                  {domainFilter && (
                    <span style={{ fontSize: "0.65rem", color: "rgb(var(--muted))" }}>
                      showing {briefItems.length} / {allBriefItems.length} (filtered)
                    </span>
                  )}
                </div>
              )}
            </div>

            {/* Brief list */}
            {format === "brief" && briefItems.length === 0 && (
              <EmptyState message="No results for this namespace + tag filter." />
            )}
            {format === "brief" && briefItems.length > 0 && (
              <div style={{ display: "flex", flexDirection: "column", gap: "0.5rem" }}>
                {briefItems.map((item) => {
                  const domain = (item.domain === "knowledge" ? "knowledge" : "memory") as
                    | "memory"
                    | "knowledge";
                  const canOpen = !!item.memory_key && !!onOpenItem;
                  return (
                    <button
                      type="button"
                      key={item.revision_id}
                      className="hud-panel"
                      onClick={() =>
                        item.memory_key && onOpenItem?.(domain, item.namespace, item.memory_key)
                      }
                      disabled={!canOpen}
                      style={{
                        padding: "0.6rem 0.75rem",
                        textAlign: "left",
                        cursor: canOpen ? "pointer" : "default",
                        background: "transparent",
                        color: "inherit",
                        width: "100%",
                      }}
                    >
                      <div
                        style={{
                          display: "flex",
                          justifyContent: "space-between",
                          gap: "0.5rem",
                          alignItems: "baseline",
                        }}
                      >
                        <div
                          style={{
                            fontSize: "0.85rem",
                            fontFamily: "var(--font-mono)",
                            color: "rgb(var(--primary))",
                          }}
                        >
                          {item.memory_key ?? (
                            <span style={{ color: "rgb(var(--muted))" }}>(no key)</span>
                          )}
                        </div>
                        <div style={{ fontSize: "0.7rem", color: "rgb(var(--muted))" }}>
                          {item.domain} · conf {item.confidence.toFixed(2)}
                        </div>
                      </div>
                      <div style={{ fontSize: "0.8rem", marginTop: "0.3rem", lineHeight: 1.4 }}>
                        {item.summary || (
                          <span style={{ color: "rgb(var(--muted))" }}>(no summary)</span>
                        )}
                      </div>
                      <div
                        style={{
                          display: "flex",
                          justifyContent: "space-between",
                          alignItems: "center",
                          marginTop: "0.4rem",
                          gap: "0.5rem",
                          flexWrap: "wrap",
                        }}
                      >
                        <div style={{ display: "flex", gap: "0.3rem", flexWrap: "wrap" }}>
                          {item.tags.slice(0, 6).map((t) => (
                            <button
                              type="button"
                              key={t}
                              onClick={(e) => {
                                e.stopPropagation();
                                const existing = tags
                                  .split(",")
                                  .map((x) => x.trim())
                                  .filter(Boolean);
                                if (existing.includes(t)) return;
                                setTags([...existing, t].join(", "));
                                toast.success(`Added tag: ${t}`);
                              }}
                              style={{
                                padding: "0.1rem 0.4rem",
                                background: "rgba(var(--panel2) / 0.6)",
                                border: "1px solid rgb(var(--border))",
                                borderRadius: "var(--radius-sm)",
                                fontSize: "0.65rem",
                                fontFamily: "var(--font-mono)",
                                color: "rgb(var(--muted))",
                                display: "inline-flex",
                                alignItems: "center",
                                gap: "0.2rem",
                                cursor: "pointer",
                              }}
                              title={`Add tag "${t}" to filter`}
                            >
                              <Tag size={9} /> {t}
                            </button>
                          ))}
                          {item.tags.length > 6 && (
                            <span style={{ fontSize: "0.65rem", color: "rgb(var(--muted))" }}>
                              +{item.tags.length - 6}
                            </span>
                          )}
                        </div>
                        <div
                          style={{
                            fontSize: "0.65rem",
                            color: "rgb(var(--muted))",
                            fontFamily: "var(--font-mono)",
                          }}
                        >
                          {item.created_at}
                        </div>
                      </div>
                    </button>
                  );
                })}
              </div>
            )}

            {/* Full format: raw RecallResult JSON for now */}
            {format === "full" && (
              <div className="hud-panel" style={{ padding: "0.75rem" }}>
                <div className="hud-label" style={{ marginBottom: "0.3rem" }}>
                  Raw RecallResult[]
                </div>
                <JsonViewer data={response.results} maxHeight="500px" />
              </div>
            )}
          </div>
        )}
      </div>

      {error && (
        <div
          className="hud-panel"
          style={{ padding: "0.75rem", color: "rgb(var(--danger))", marginTop: "0.75rem" }}
        >
          {error}
        </div>
      )}
    </div>
  );
}
