import { Lightbulb, MessageSquare, Search, Tag, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { conduitLookup, listNamespaces } from "../api/client";
import type { ConduitLookupResponse, ConduitLookupResultItem, MemoryStatus } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { JsonViewer } from "../components/ui/JsonViewer";
import { Spinner } from "../components/ui/Spinner";
import { StatusBadge } from "../components/ui/StatusBadge";

// v1: curated, no LLM. The "Answer" tab groups results by domain and renders
// summary-first cards. Sources tab carries the full revision JSON for citation.
//
// v2 (high priority, separate slice): wire an LLM-backed synthesis step
// using the portfolio's go-modelsdev library so cost / token / latency
// telemetry comes through accurately. v2 fans the curated v1 results into
// a completion call with a fixed system prompt, returns the synthesized
// answer + per-source attribution markers, and persists the Q/A thread to
// memory so follow-ups can be recalled in future sessions.

type Tab = "answer" | "sources";

type DomainFilter = "both" | "memory" | "knowledge";

interface ThreadEntry {
  id: string;
  question: string;
  response: ConduitLookupResponse | null;
  error: string | null;
  asked_at: string;
}

interface Props {
  onOpenItem?: (domain: "memory" | "knowledge", namespace: string, key: string) => void;
}

const STATUS_FILTERS: MemoryStatus[] = ["draft", "reviewed", "canonical", "deprecated"];

export function SearchResearchPage({ onOpenItem }: Props) {
  const [question, setQuestion] = useState("");
  const [namespacesField, setNamespacesField] = useState("");
  const [tagsField, setTagsField] = useState("");
  const [domain, setDomain] = useState<DomainFilter>("both");
  const [statusSet, setStatusSet] = useState<Set<MemoryStatus>>(new Set(["canonical", "reviewed"]));
  const [confidenceMin, setConfidenceMin] = useState("0.5");
  const [limit, setLimit] = useState("20");
  const [thread, setThread] = useState<ThreadEntry[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("answer");
  const [loading, setLoading] = useState(false);

  // Default namespace seed: pull all registered namespaces once so the user
  // can blanket-search without typing. They can override with the field.
  const [defaultNamespaces, setDefaultNamespaces] = useState<string[]>([]);
  useEffect(() => {
    listNamespaces({ limit: 1000 })
      .then((res) => setDefaultNamespaces(res.items.map((n) => n.namespace)))
      .catch(() => setDefaultNamespaces([]));
  }, []);

  const active = thread.find((e) => e.id === activeId) ?? null;

  const handleAsk = async () => {
    const q = question.trim();
    if (!q) {
      toast.error("Question is required");
      return;
    }
    const explicitNs = namespacesField
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    const namespaces = explicitNs.length > 0 ? explicitNs : defaultNamespaces;
    if (namespaces.length === 0) {
      toast.error("No namespaces available; type a namespace or wait for the registry to load.");
      return;
    }

    const id = `q_${Date.now()}`;
    const entry: ThreadEntry = {
      id,
      question: q,
      response: null,
      error: null,
      asked_at: new Date().toISOString(),
    };
    setThread((prev) => [entry, ...prev]);
    setActiveId(id);
    setLoading(true);

    try {
      const tagList = tagsField
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      const parsedLimit = parseInt(limit, 10);
      const parsedConf = parseFloat(confidenceMin);
      const req: Parameters<typeof conduitLookup>[0] = {
        namespaces,
        query: q,
        ranking: "relevance",
      };
      if (Number.isFinite(parsedLimit) && parsedLimit > 0) req.limit = parsedLimit;
      if (domain !== "both") req.domains = [domain];
      if (tagList.length > 0) req.tags = tagList;
      if (statusSet.size > 0) req.statuses = Array.from(statusSet);
      if (Number.isFinite(parsedConf) && parsedConf > 0) req.confidence_min = parsedConf;

      const res = await conduitLookup(req);
      setThread((prev) => prev.map((e) => (e.id === id ? { ...e, response: res } : e)));
      toast.success(`Returned ${res.results.length} result${res.results.length === 1 ? "" : "s"}`);
      setTab("answer");
      setQuestion("");
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setThread((prev) => prev.map((e) => (e.id === id ? { ...e, error: msg } : e)));
      toast.error(`Lookup failed: ${msg}`);
    } finally {
      setLoading(false);
    }
  };

  const handleClearThread = () => {
    setThread([]);
    setActiveId(null);
  };

  const toggleStatus = (s: MemoryStatus) => {
    const next = new Set(statusSet);
    if (next.has(s)) next.delete(s);
    else next.add(s);
    setStatusSet(next);
  };

  const groupedByDomain = (results: ConduitLookupResultItem[]) => {
    const map = new Map<string, ConduitLookupResultItem[]>();
    for (const r of results) {
      const d = r.Revision.domain;
      const arr = map.get(d) ?? [];
      arr.push(r);
      map.set(d, arr);
    }
    return Array.from(map.entries()).sort(([a], [b]) => a.localeCompare(b));
  };

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Search &amp; Research</h2>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "370px 1fr", gap: "1rem" }}>
        {/* ── Left rail: thread + filters ─────────────────────── */}
        <div style={{ display: "flex", flexDirection: "column", gap: "0.75rem" }}>
          <div className="hud-panel" style={{ padding: "0.75rem" }}>
            <div className="form-field">
              <label className="hud-label" htmlFor="search-question">
                Question <span style={{ color: "rgb(var(--danger))" }}>*</span>
              </label>
              <textarea
                id="search-question"
                className="hud-textarea"
                placeholder="What does the store know about X? History of Y? Trace the concept Z..."
                value={question}
                onChange={(e) => setQuestion(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) handleAsk();
                }}
                rows={4}
                style={{ width: "100%", minHeight: "5.5rem" }}
              />
              <div style={{ fontSize: "0.65rem", color: "rgb(var(--muted))", marginTop: "0.2rem" }}>
                ⌘/Ctrl + Enter to ask. v1 returns curated cited results; v2 (planned) adds LLM
                synthesis via go-modelsdev.
              </div>
            </div>

            <div className="form-field" style={{ marginTop: "0.5rem" }}>
              <label className="hud-label" htmlFor="search-namespaces">
                Namespaces{" "}
                <span style={{ color: "rgb(var(--muted))" }}>
                  (comma-separated; blank = all {defaultNamespaces.length})
                </span>
              </label>
              <input
                id="search-namespaces"
                className="hud-input"
                placeholder="user/jane/memory, user/jane/knowledge/projects"
                value={namespacesField}
                onChange={(e) => setNamespacesField(e.target.value)}
                style={{ width: "100%" }}
              />
            </div>

            <div className="form-field" style={{ marginTop: "0.5rem" }}>
              <label className="hud-label" htmlFor="search-tags">
                Tags <span style={{ color: "rgb(var(--muted))" }}>(comma-separated)</span>
              </label>
              <input
                id="search-tags"
                className="hud-input"
                placeholder="decision, scope:agent-ops.steward.main"
                value={tagsField}
                onChange={(e) => setTagsField(e.target.value)}
                style={{ width: "100%" }}
              />
            </div>

            <div className="form-grid" style={{ marginTop: "0.5rem" }}>
              <div className="form-field">
                <span className="hud-label">Domain</span>
                <div style={{ display: "flex", gap: "0.25rem", paddingTop: "0.2rem" }}>
                  {(["both", "memory", "knowledge"] as const).map((d) => (
                    <button
                      key={d}
                      type="button"
                      onClick={() => setDomain(d)}
                      style={{
                        padding: "0.2rem 0.5rem",
                        background: domain === d ? "rgba(var(--primary) / 0.12)" : "transparent",
                        border: `1px solid ${domain === d ? "rgb(var(--primary))" : "rgb(var(--border))"}`,
                        color: domain === d ? "rgb(var(--primary))" : "rgb(var(--muted))",
                        cursor: "pointer",
                        fontSize: "0.65rem",
                        fontFamily: "var(--font-mono)",
                        textTransform: "uppercase",
                        borderRadius: "var(--radius-sm)",
                      }}
                    >
                      {d}
                    </button>
                  ))}
                </div>
              </div>

              <div className="form-field">
                <label className="hud-label" htmlFor="search-confidence">
                  Min confidence
                </label>
                <input
                  id="search-confidence"
                  className="hud-input"
                  type="number"
                  step="0.05"
                  min="0"
                  max="1"
                  value={confidenceMin}
                  onChange={(e) => setConfidenceMin(e.target.value)}
                  style={{ width: "100%" }}
                />
              </div>
            </div>

            <div className="form-field" style={{ marginTop: "0.5rem" }}>
              <span className="hud-label">Statuses</span>
              <div
                style={{ display: "flex", gap: "0.25rem", flexWrap: "wrap", paddingTop: "0.2rem" }}
              >
                {STATUS_FILTERS.map((s) => (
                  <button
                    key={s}
                    type="button"
                    onClick={() => toggleStatus(s)}
                    style={{
                      padding: "0.2rem 0.5rem",
                      background: statusSet.has(s) ? "rgba(var(--primary) / 0.12)" : "transparent",
                      border: `1px solid ${statusSet.has(s) ? "rgb(var(--primary))" : "rgb(var(--border))"}`,
                      color: statusSet.has(s) ? "rgb(var(--primary))" : "rgb(var(--muted))",
                      cursor: "pointer",
                      fontSize: "0.65rem",
                      fontFamily: "var(--font-mono)",
                      borderRadius: "var(--radius-sm)",
                    }}
                  >
                    {s}
                  </button>
                ))}
              </div>
            </div>

            <div className="form-field" style={{ marginTop: "0.5rem" }}>
              <label className="hud-label" htmlFor="search-limit">
                Limit
              </label>
              <input
                id="search-limit"
                className="hud-input"
                type="number"
                min="1"
                max="500"
                value={limit}
                onChange={(e) => setLimit(e.target.value)}
                style={{ width: "100%" }}
              />
            </div>

            <button
              type="button"
              className="hud-button-primary"
              onClick={handleAsk}
              disabled={loading || !question.trim()}
              style={{ marginTop: "0.75rem", width: "100%" }}
            >
              <span
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  gap: "0.3rem",
                }}
              >
                {loading ? <Spinner size={13} /> : <Search size={13} />}{" "}
                {thread.length > 0 ? "Ask follow-up" : "Ask"}
              </span>
            </button>
          </div>

          {thread.length > 0 && (
            <div className="hud-panel" style={{ padding: "0.5rem" }}>
              <div
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  marginBottom: "0.4rem",
                }}
              >
                <div
                  className="hud-label"
                  style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}
                >
                  <MessageSquare size={11} /> Thread ({thread.length})
                </div>
                <button
                  type="button"
                  className="hud-button-ghost"
                  onClick={handleClearThread}
                  style={{ padding: "0.1rem 0.3rem" }}
                  title="Clear thread"
                >
                  <Trash2 size={11} />
                </button>
              </div>
              {thread.map((e) => (
                <button
                  key={e.id}
                  type="button"
                  onClick={() => setActiveId(e.id)}
                  style={{
                    display: "block",
                    width: "100%",
                    textAlign: "left",
                    padding: "0.4rem 0.5rem",
                    background: e.id === activeId ? "rgba(var(--primary) / 0.08)" : "transparent",
                    border: "none",
                    borderLeft:
                      e.id === activeId ? "2px solid rgb(var(--primary))" : "2px solid transparent",
                    color: "rgb(var(--text))",
                    cursor: "pointer",
                    fontSize: "0.75rem",
                    marginBottom: "0.15rem",
                    borderRadius: "var(--radius-sm)",
                  }}
                >
                  <div
                    style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                  >
                    {e.question}
                  </div>
                  <div
                    style={{ fontSize: "0.6rem", color: "rgb(var(--muted))", marginTop: "0.1rem" }}
                  >
                    {e.error ? (
                      <span style={{ color: "rgb(var(--danger))" }}>error</span>
                    ) : e.response ? (
                      `${e.response.results.length} hits`
                    ) : (
                      "loading…"
                    )}
                  </div>
                </button>
              ))}
            </div>
          )}
        </div>

        {/* ── Right pane: result ─────────────────────────── */}
        <div>
          {!active && (
            <div className="hud-panel" style={{ padding: "2rem" }}>
              <EmptyState
                message="Ask a question to begin."
                sub="Use the form on the left. Each ask becomes a thread entry; click thread items to revisit."
                icon={<Lightbulb size={32} strokeWidth={1.5} />}
              />
            </div>
          )}

          {active && (
            <div>
              {/* Question header */}
              <div className="hud-panel" style={{ padding: "0.75rem", marginBottom: "0.5rem" }}>
                <div style={{ fontSize: "0.95rem", lineHeight: 1.5 }}>{active.question}</div>
                <div
                  style={{ fontSize: "0.65rem", color: "rgb(var(--muted))", marginTop: "0.3rem" }}
                >
                  asked {active.asked_at}
                </div>
              </div>

              {active.error && (
                <div
                  className="hud-panel"
                  style={{ padding: "0.75rem", color: "rgb(var(--danger))" }}
                >
                  {active.error}
                </div>
              )}

              {!active.response && !active.error && (
                <div style={{ padding: "2rem", textAlign: "center" }}>
                  <Spinner size={20} />
                </div>
              )}

              {active.response && (
                <>
                  {/* Tabs */}
                  <div
                    style={{
                      display: "flex",
                      gap: "0.25rem",
                      marginBottom: "0.5rem",
                      borderBottom: "1px solid rgb(var(--border))",
                    }}
                  >
                    {(["answer", "sources"] as const).map((t) => (
                      <button
                        key={t}
                        type="button"
                        onClick={() => setTab(t)}
                        style={{
                          padding: "0.4rem 0.75rem",
                          background: tab === t ? "rgba(var(--primary) / 0.08)" : "transparent",
                          border: "none",
                          borderBottom:
                            tab === t ? "2px solid rgb(var(--primary))" : "2px solid transparent",
                          color: tab === t ? "rgb(var(--primary))" : "rgb(var(--muted))",
                          cursor: "pointer",
                          fontSize: "0.8rem",
                          fontFamily: "var(--font-mono)",
                          textTransform: "uppercase",
                        }}
                      >
                        {t}
                      </button>
                    ))}
                    <div style={{ flex: 1 }} />
                    <span
                      style={{ fontSize: "0.7rem", color: "rgb(var(--muted))", padding: "0.5rem" }}
                    >
                      {active.response.results.length} result
                      {active.response.results.length === 1 ? "" : "s"}
                      {active.response.facets.domains &&
                        ` · ${Object.entries(active.response.facets.domains)
                          .map(([d, n]) => `${d}: ${n}`)
                          .join(", ")}`}
                    </span>
                  </div>

                  {/* Answer tab */}
                  {tab === "answer" && (
                    <div>
                      <div
                        className="hud-panel"
                        style={{
                          padding: "0.75rem",
                          marginBottom: "0.5rem",
                          borderColor: "rgba(var(--primary) / 0.4)",
                        }}
                      >
                        <div
                          className="hud-label"
                          style={{
                            display: "flex",
                            alignItems: "center",
                            gap: "0.3rem",
                            marginBottom: "0.4rem",
                          }}
                        >
                          <Lightbulb size={12} /> v1 curated answer
                        </div>
                        <div
                          style={{
                            fontSize: "0.8rem",
                            color: "rgb(var(--muted))",
                            lineHeight: 1.5,
                          }}
                        >
                          The store returned {active.response.results.length} relevance-ranked
                          revision{active.response.results.length === 1 ? "" : "s"} matching your
                          question. v1 surfaces them grouped by domain with summaries first; the
                          Sources tab carries the full citation payload. v2 will replace this card
                          with an LLM-synthesized answer that cites these same sources, using the
                          portfolio go-modelsdev library so cost, tokens, and latency telemetry come
                          through accurately.
                        </div>
                      </div>

                      {groupedByDomain(active.response.results).map(([d, items]) => (
                        <div key={d} style={{ marginBottom: "0.75rem" }}>
                          <div
                            className="hud-label"
                            style={{
                              padding: "0.3rem 0",
                              color: "rgb(var(--primary))",
                              borderBottom: "1px solid rgb(var(--border))",
                              marginBottom: "0.4rem",
                              textTransform: "uppercase",
                            }}
                          >
                            {d} · {items.length}
                          </div>
                          <div style={{ display: "flex", flexDirection: "column", gap: "0.4rem" }}>
                            {items.map((r) => (
                              <button
                                key={r.Revision.revision_id}
                                type="button"
                                className="hud-panel"
                                onClick={() =>
                                  r.Revision.memory_key &&
                                  onOpenItem?.(
                                    r.Revision.domain,
                                    r.Revision.namespace,
                                    r.Revision.memory_key,
                                  )
                                }
                                disabled={!r.Revision.memory_key || !onOpenItem}
                                style={{
                                  padding: "0.6rem 0.75rem",
                                  textAlign: "left",
                                  background: "transparent",
                                  color: "inherit",
                                  width: "100%",
                                  cursor:
                                    r.Revision.memory_key && onOpenItem ? "pointer" : "default",
                                }}
                              >
                                <div
                                  style={{
                                    display: "flex",
                                    justifyContent: "space-between",
                                    alignItems: "baseline",
                                    gap: "0.5rem",
                                  }}
                                >
                                  <div
                                    style={{
                                      fontFamily: "var(--font-mono)",
                                      fontSize: "0.8rem",
                                      color: "rgb(var(--primary))",
                                    }}
                                  >
                                    {r.Revision.memory_key ?? "(no key)"}
                                  </div>
                                  <div
                                    style={{
                                      display: "flex",
                                      gap: "0.3rem",
                                      alignItems: "center",
                                      fontSize: "0.65rem",
                                    }}
                                  >
                                    <StatusBadge status={r.Revision.status} />
                                    {r.Score !== undefined && (
                                      <span
                                        style={{
                                          fontFamily: "var(--font-mono)",
                                          color: "rgb(var(--muted))",
                                        }}
                                      >
                                        score {r.Score.toFixed(3)}
                                      </span>
                                    )}
                                    <span
                                      style={{
                                        fontFamily: "var(--font-mono)",
                                        color: "rgb(var(--muted))",
                                      }}
                                    >
                                      conf {r.Revision.confidence.toFixed(2)}
                                    </span>
                                  </div>
                                </div>
                                <div
                                  style={{
                                    fontSize: "0.8rem",
                                    marginTop: "0.3rem",
                                    lineHeight: 1.4,
                                  }}
                                >
                                  {r.Revision.payload.summary || "(no summary)"}
                                </div>
                                <div
                                  style={{
                                    display: "flex",
                                    justifyContent: "space-between",
                                    alignItems: "center",
                                    marginTop: "0.3rem",
                                    flexWrap: "wrap",
                                    gap: "0.3rem",
                                  }}
                                >
                                  <div style={{ display: "flex", gap: "0.2rem", flexWrap: "wrap" }}>
                                    {r.Revision.tags.slice(0, 5).map((t) => (
                                      <span
                                        key={t}
                                        style={{
                                          padding: "0.05rem 0.3rem",
                                          background: "rgba(var(--panel2) / 0.6)",
                                          border: "1px solid rgb(var(--border))",
                                          borderRadius: "var(--radius-sm)",
                                          fontSize: "0.6rem",
                                          fontFamily: "var(--font-mono)",
                                          color: "rgb(var(--muted))",
                                          display: "inline-flex",
                                          alignItems: "center",
                                          gap: "0.15rem",
                                        }}
                                      >
                                        <Tag size={8} /> {t}
                                      </span>
                                    ))}
                                  </div>
                                  <span
                                    style={{
                                      fontSize: "0.6rem",
                                      fontFamily: "var(--font-mono)",
                                      color: "rgb(var(--muted))",
                                    }}
                                  >
                                    {r.Revision.namespace}
                                  </span>
                                </div>
                              </button>
                            ))}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}

                  {/* Sources tab */}
                  {tab === "sources" && (
                    <div className="hud-panel" style={{ padding: "0.75rem" }}>
                      <div className="hud-label" style={{ marginBottom: "0.4rem" }}>
                        Raw cited revisions ({active.response.results.length})
                      </div>
                      <JsonViewer data={active.response.results} maxHeight="600px" />
                    </div>
                  )}
                </>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
