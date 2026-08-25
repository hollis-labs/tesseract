import { Lightbulb, MessageSquare, Search, Sparkles, Tag, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { tesseractLookup, listNamespaces, synthesisAsk } from "../api/client";
import type {
  TesseractLookupResponse,
  TesseractLookupResultItem,
  MemoryStatus,
  SynthesisAskResponse,
} from "../api/types";
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

type Tab = "answer" | "synthesis" | "sources";

type DomainFilter = "both" | "memory" | "knowledge";

interface ThreadEntry {
  id: string;
  question: string;
  response: TesseractLookupResponse | null;
  // synthesis is loaded lazily when the operator opens the Synthesis tab and
  // clicks "Synthesize". Cached on the entry so re-clicking the entry doesn't
  // re-spend tokens.
  synthesis?: SynthesisAskResponse | null;
  synthesisError?: string | null;
  error: string | null;
  asked_at: string;
}

interface Props {
  onOpenItem?: (domain: "memory" | "knowledge", namespace: string, key: string) => void;
}

const STATUS_FILTERS: MemoryStatus[] = ["draft", "reviewed", "canonical", "deprecated"];

const THREAD_STORAGE_KEY = "tesseract.searchResearch.thread";
const PRESETS_STORAGE_KEY = "tesseract.searchResearch.presets";
const RECENT_NS_STORAGE_KEY = "tesseract.searchResearch.recentNamespaces";
const THREAD_MAX = 20;
const RECENT_NS_MAX = 8;

interface SavedPreset {
  name: string;
  question: string;
  namespacesField: string;
  tagsField: string;
  domain: DomainFilter;
  statuses: MemoryStatus[];
  confidenceMin: string;
  limit: string;
}

function safeReadJSON<T>(key: string, fallback: T): T {
  if (typeof window === "undefined") return fallback;
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return fallback;
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

function safeWriteJSON(key: string, value: unknown): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // localStorage unavailable / quota — ignore.
  }
}

export function SearchResearchPage({ onOpenItem }: Props) {
  const [question, setQuestion] = useState("");
  const [namespacesField, setNamespacesField] = useState("");
  const [tagsField, setTagsField] = useState("");
  const [domain, setDomain] = useState<DomainFilter>("both");
  const [statusSet, setStatusSet] = useState<Set<MemoryStatus>>(new Set(["canonical", "reviewed"]));
  const [confidenceMin, setConfidenceMin] = useState("0.5");
  const [limit, setLimit] = useState("20");
  const [thread, setThread] = useState<ThreadEntry[]>(() =>
    safeReadJSON<ThreadEntry[]>(THREAD_STORAGE_KEY, []),
  );
  const [activeId, setActiveId] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("answer");
  const [loading, setLoading] = useState(false);
  const [synthLoading, setSynthLoading] = useState(false);
  const [presets, setPresets] = useState<SavedPreset[]>(() =>
    safeReadJSON<SavedPreset[]>(PRESETS_STORAGE_KEY, []),
  );
  const [recentNamespaces, setRecentNamespaces] = useState<string[]>(() =>
    safeReadJSON<string[]>(RECENT_NS_STORAGE_KEY, []),
  );
  const [presetName, setPresetName] = useState("");

  // Default namespace seed: pull all registered namespaces once so the user
  // can blanket-search without typing. They can override with the field.
  const [defaultNamespaces, setDefaultNamespaces] = useState<string[]>([]);
  useEffect(() => {
    listNamespaces({ limit: 1000 })
      .then((res) => setDefaultNamespaces(res.items.map((n) => n.namespace)))
      .catch(() => setDefaultNamespaces([]));
  }, []);

  // Persist thread on change so reloads don't lose context. Trimmed to
  // THREAD_MAX entries (newest first) to keep storage bounded.
  useEffect(() => {
    safeWriteJSON(THREAD_STORAGE_KEY, thread.slice(0, THREAD_MAX));
  }, [thread]);

  // Persist presets + recent namespaces.
  useEffect(() => {
    safeWriteJSON(PRESETS_STORAGE_KEY, presets);
  }, [presets]);
  useEffect(() => {
    safeWriteJSON(RECENT_NS_STORAGE_KEY, recentNamespaces);
  }, [recentNamespaces]);

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
      const req: Parameters<typeof tesseractLookup>[0] = {
        namespaces,
        query: q,
        ranking: "relevance",
      };
      if (Number.isFinite(parsedLimit) && parsedLimit > 0) req.limit = parsedLimit;
      if (domain !== "both") req.domains = [domain];
      if (tagList.length > 0) req.tags = tagList;
      if (statusSet.size > 0) req.statuses = Array.from(statusSet);
      if (Number.isFinite(parsedConf) && parsedConf > 0) req.confidence_min = parsedConf;

      const res = await tesseractLookup(req);
      setThread((prev) => prev.map((e) => (e.id === id ? { ...e, response: res } : e)));
      // Update recent namespaces from the actual queried set (explicit > default).
      const used = explicitNs.length > 0 ? explicitNs : namespaces.slice(0, 3);
      setRecentNamespaces((prev) => {
        const next = [...used.filter((n) => !prev.includes(n)), ...prev].slice(0, RECENT_NS_MAX);
        return next;
      });
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

  // Run the LLM synthesis for the active thread entry. Cached on the entry
  // so jumping back to a prior question doesn't re-spend tokens.
  const handleSynthesize = async () => {
    if (!active || !active.response) return;
    if (active.synthesis) return;
    const explicitNs = namespacesField
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    const namespaces = explicitNs.length > 0 ? explicitNs : defaultNamespaces;
    if (namespaces.length === 0) {
      toast.error("No namespaces available for synthesis.");
      return;
    }
    setSynthLoading(true);
    try {
      const tagList = tagsField
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      const parsedLimit = parseInt(limit, 10);
      const parsedConf = parseFloat(confidenceMin);
      const req: Parameters<typeof synthesisAsk>[0] = {
        question: active.question,
        namespaces,
      };
      if (Number.isFinite(parsedLimit) && parsedLimit > 0) req.limit = parsedLimit;
      if (domain !== "both") req.domains = [domain];
      if (tagList.length > 0) req.tags = tagList;
      if (statusSet.size > 0) req.statuses = Array.from(statusSet);
      if (Number.isFinite(parsedConf) && parsedConf > 0) req.confidence_min = parsedConf;
      const synth = await synthesisAsk(req);
      setThread((prev) =>
        prev.map((e) => (e.id === active.id ? { ...e, synthesis: synth, synthesisError: null } : e)),
      );
      toast.success(`Synthesized via ${synth.usage.provider} / ${synth.usage.model}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setThread((prev) =>
        prev.map((e) => (e.id === active.id ? { ...e, synthesisError: msg } : e)),
      );
      toast.error(`Synthesis failed: ${msg}`);
    } finally {
      setSynthLoading(false);
    }
  };

  const toggleStatus = (s: MemoryStatus) => {
    const next = new Set(statusSet);
    if (next.has(s)) next.delete(s);
    else next.add(s);
    setStatusSet(next);
  };

  const groupedByDomain = (results: TesseractLookupResultItem[]) => {
    const map = new Map<string, TesseractLookupResultItem[]>();
    for (const r of results) {
      const d = r.revision.domain;
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
                list="search-namespaces-recents"
                placeholder="user/jane/memory, user/jane/knowledge/projects"
                value={namespacesField}
                onChange={(e) => setNamespacesField(e.target.value)}
                style={{ width: "100%" }}
              />
              <datalist id="search-namespaces-recents">
                {recentNamespaces.map((ns) => (
                  <option key={ns} value={ns} label="recent" />
                ))}
              </datalist>
              {recentNamespaces.length > 0 && (
                <div
                  style={{ display: "flex", gap: "0.25rem", flexWrap: "wrap", marginTop: "0.3rem" }}
                >
                  {recentNamespaces.slice(0, 5).map((ns) => (
                    <button
                      key={ns}
                      type="button"
                      onClick={() => {
                        const existing = namespacesField
                          .split(",")
                          .map((s) => s.trim())
                          .filter(Boolean);
                        if (existing.includes(ns)) return;
                        setNamespacesField([...existing, ns].join(", "));
                      }}
                      title={`Add ${ns} to namespaces`}
                      style={{
                        padding: "0.1rem 0.4rem",
                        background: "rgba(var(--panel2) / 0.6)",
                        border: "1px solid rgb(var(--border))",
                        borderRadius: "var(--radius-sm)",
                        fontSize: "0.6rem",
                        fontFamily: "var(--font-mono)",
                        color: "rgb(var(--muted))",
                        cursor: "pointer",
                      }}
                    >
                      {ns}
                    </button>
                  ))}
                </div>
              )}
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

            {/* Presets — save current filter set, load by name. */}
            <div
              style={{
                marginTop: "0.75rem",
                borderTop: "1px solid rgb(var(--border))",
                paddingTop: "0.5rem",
              }}
            >
              <div className="hud-label" style={{ marginBottom: "0.3rem", fontSize: "0.7rem" }}>
                Presets
              </div>
              <div style={{ display: "flex", gap: "0.3rem", marginBottom: "0.4rem" }}>
                <input
                  className="hud-input"
                  placeholder="preset name..."
                  value={presetName}
                  onChange={(e) => setPresetName(e.target.value)}
                  style={{ flex: 1, fontSize: "0.75rem" }}
                />
                <button
                  type="button"
                  className="hud-button-ghost"
                  disabled={!presetName.trim() || !question.trim()}
                  onClick={() => {
                    const name = presetName.trim();
                    if (!name) return;
                    const preset: SavedPreset = {
                      name,
                      question: question.trim(),
                      namespacesField,
                      tagsField,
                      domain,
                      statuses: Array.from(statusSet),
                      confidenceMin,
                      limit,
                    };
                    setPresets((prev) =>
                      [preset, ...prev.filter((p) => p.name !== name)].slice(0, 20),
                    );
                    setPresetName("");
                    toast.success(`Saved preset "${name}"`);
                  }}
                  style={{ fontSize: "0.7rem", padding: "0.2rem 0.5rem" }}
                  title="Save current question + filters as a named preset"
                >
                  Save
                </button>
              </div>
              {presets.length === 0 && (
                <div style={{ fontSize: "0.65rem", color: "rgb(var(--muted))" }}>
                  No saved presets yet. Save a question + filter combo to reuse.
                </div>
              )}
              {presets.length > 0 && (
                <div style={{ display: "flex", flexDirection: "column", gap: "0.2rem" }}>
                  {presets.map((p) => (
                    <div
                      key={p.name}
                      style={{ display: "flex", gap: "0.2rem", alignItems: "center" }}
                    >
                      <button
                        type="button"
                        onClick={() => {
                          setQuestion(p.question);
                          setNamespacesField(p.namespacesField);
                          setTagsField(p.tagsField);
                          setDomain(p.domain);
                          setStatusSet(new Set(p.statuses));
                          setConfidenceMin(p.confidenceMin);
                          setLimit(p.limit);
                          toast.success(`Loaded preset "${p.name}"`);
                        }}
                        style={{
                          flex: 1,
                          textAlign: "left",
                          padding: "0.2rem 0.4rem",
                          background: "transparent",
                          border: "1px solid rgb(var(--border))",
                          borderRadius: "var(--radius-sm)",
                          color: "rgb(var(--text))",
                          fontFamily: "var(--font-mono)",
                          fontSize: "0.7rem",
                          cursor: "pointer",
                        }}
                      >
                        {p.name}
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          setPresets((prev) => prev.filter((x) => x.name !== p.name));
                          toast.success(`Removed preset "${p.name}"`);
                        }}
                        title="Delete preset"
                        style={{
                          padding: "0.1rem 0.3rem",
                          background: "transparent",
                          border: "none",
                          color: "rgb(var(--muted))",
                          cursor: "pointer",
                        }}
                      >
                        <Trash2 size={10} />
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
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
                    {(["answer", "synthesis", "sources"] as const).map((t) => (
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
                                key={r.revision.revision_id}
                                type="button"
                                className="hud-panel"
                                onClick={() =>
                                  r.revision.memory_key &&
                                  onOpenItem?.(
                                    r.revision.domain,
                                    r.revision.namespace,
                                    r.revision.memory_key,
                                  )
                                }
                                disabled={!r.revision.memory_key || !onOpenItem}
                                style={{
                                  padding: "0.6rem 0.75rem",
                                  textAlign: "left",
                                  background: "transparent",
                                  color: "inherit",
                                  width: "100%",
                                  cursor:
                                    r.revision.memory_key && onOpenItem ? "pointer" : "default",
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
                                    {r.revision.memory_key ?? "(no key)"}
                                  </div>
                                  <div
                                    style={{
                                      display: "flex",
                                      gap: "0.3rem",
                                      alignItems: "center",
                                      fontSize: "0.65rem",
                                    }}
                                  >
                                    <StatusBadge status={r.revision.status} />
                                    {r.score !== undefined && (
                                      <span
                                        style={{
                                          fontFamily: "var(--font-mono)",
                                          color: "rgb(var(--muted))",
                                        }}
                                      >
                                        score {r.score.toFixed(3)}
                                      </span>
                                    )}
                                    <span
                                      style={{
                                        fontFamily: "var(--font-mono)",
                                        color: "rgb(var(--muted))",
                                      }}
                                    >
                                      conf {r.revision.confidence.toFixed(2)}
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
                                  {r.revision.payload.summary || "(no summary)"}
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
                                    {r.revision.tags.slice(0, 5).map((t) => (
                                      <button
                                        type="button"
                                        key={t}
                                        onClick={(e) => {
                                          e.stopPropagation();
                                          const existing = tagsField
                                            .split(",")
                                            .map((x) => x.trim())
                                            .filter(Boolean);
                                          if (existing.includes(t)) return;
                                          setTagsField([...existing, t].join(", "));
                                          toast.success(`Added tag: ${t}`);
                                        }}
                                        title={`Add tag "${t}" to filter and re-ask`}
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
                                          cursor: "pointer",
                                        }}
                                      >
                                        <Tag size={8} /> {t}
                                      </button>
                                    ))}
                                  </div>
                                  <span
                                    style={{
                                      fontSize: "0.6rem",
                                      fontFamily: "var(--font-mono)",
                                      color: "rgb(var(--muted))",
                                    }}
                                  >
                                    {r.revision.namespace}
                                  </span>
                                </div>
                              </button>
                            ))}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}

                  {/* Synthesis tab — LLM-backed answer */}
                  {tab === "synthesis" && (
                    <div>
                      {!active.synthesis && !active.synthesisError && (
                        <div
                          className="hud-panel"
                          style={{
                            padding: "1rem",
                            borderColor: "rgba(var(--primary) / 0.4)",
                            textAlign: "center",
                          }}
                        >
                          <div style={{ fontSize: "0.85rem", marginBottom: "0.75rem", color: "rgb(var(--muted))" }}>
                            Synthesize an LLM-backed answer from the {active.response.results.length} cited
                            source{active.response.results.length === 1 ? "" : "s"}. Cost + token
                            telemetry resolved via go-modelsdev.
                          </div>
                          <button
                            type="button"
                            className="hud-button-primary"
                            onClick={handleSynthesize}
                            disabled={synthLoading}
                          >
                            <span style={{ display: "flex", alignItems: "center", gap: "0.3rem", justifyContent: "center" }}>
                              {synthLoading ? <Spinner size={13} /> : <Sparkles size={13} />}
                              Synthesize
                            </span>
                          </button>
                        </div>
                      )}

                      {active.synthesisError && !active.synthesis && (
                        <div
                          className="hud-panel"
                          style={{ padding: "0.75rem", color: "rgb(var(--danger))" }}
                        >
                          {active.synthesisError}
                          <div style={{ marginTop: "0.4rem" }}>
                            <button
                              type="button"
                              className="hud-button-ghost"
                              onClick={handleSynthesize}
                              disabled={synthLoading}
                              style={{ fontSize: "0.7rem" }}
                            >
                              Retry
                            </button>
                          </div>
                        </div>
                      )}

                      {active.synthesis && (
                        <div>
                          <div
                            className="hud-panel"
                            style={{
                              padding: "1rem",
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
                                marginBottom: "0.5rem",
                              }}
                            >
                              <Sparkles size={12} /> Synthesized answer
                            </div>
                            <div
                              style={{
                                fontSize: "0.9rem",
                                lineHeight: 1.6,
                                whiteSpace: "pre-wrap",
                              }}
                            >
                              {active.synthesis.answer}
                            </div>
                          </div>

                          {/* Telemetry footer */}
                          <div
                            className="hud-panel"
                            style={{
                              padding: "0.5rem 0.75rem",
                              fontSize: "0.7rem",
                              color: "rgb(var(--muted))",
                              fontFamily: "var(--font-mono)",
                              display: "flex",
                              gap: "1rem",
                              flexWrap: "wrap",
                              marginBottom: "0.5rem",
                            }}
                          >
                            <span>
                              {active.synthesis.usage.provider} / {active.synthesis.usage.model}
                            </span>
                            <span>{active.synthesis.usage.latency_ms}ms</span>
                            {active.synthesis.usage.input_tokens > 0 && (
                              <span>
                                {active.synthesis.usage.input_tokens} in / {active.synthesis.usage.output_tokens} out tokens
                              </span>
                            )}
                            {active.synthesis.usage.cost && (
                              <span>${active.synthesis.usage.cost.total_usd.toFixed(6)} total</span>
                            )}
                            {!active.synthesis.usage.cost && (
                              <span style={{ color: "rgb(var(--muted))" }}>
                                cost: unavailable (provider doesn't surface tokens on Complete)
                              </span>
                            )}
                          </div>

                          {/* Numbered sources used by the synthesis */}
                          <div
                            className="hud-label"
                            style={{ marginTop: "0.75rem", marginBottom: "0.4rem", color: "rgb(var(--primary))" }}
                          >
                            Cited sources ({active.synthesis.sources.length})
                          </div>
                          <div style={{ display: "flex", flexDirection: "column", gap: "0.4rem" }}>
                            {active.synthesis.sources.map((src) => (
                              <button
                                key={src.revision_id}
                                type="button"
                                className="hud-panel"
                                onClick={() =>
                                  src.memory_key &&
                                  onOpenItem?.(src.domain, src.namespace, src.memory_key)
                                }
                                disabled={!src.memory_key || !onOpenItem}
                                style={{
                                  padding: "0.5rem 0.75rem",
                                  textAlign: "left",
                                  background: "transparent",
                                  color: "inherit",
                                  width: "100%",
                                  cursor: src.memory_key && onOpenItem ? "pointer" : "default",
                                }}
                              >
                                <div style={{ display: "flex", justifyContent: "space-between", gap: "0.5rem", alignItems: "baseline" }}>
                                  <div style={{ fontSize: "0.8rem", fontFamily: "var(--font-mono)" }}>
                                    <span style={{ color: "rgb(var(--primary))", fontWeight: 600 }}>[{src.n}]</span>{" "}
                                    {src.memory_key ?? "(no key)"}
                                  </div>
                                  <div style={{ fontSize: "0.65rem", color: "rgb(var(--muted))", fontFamily: "var(--font-mono)" }}>
                                    {src.domain} · conf {src.confidence.toFixed(2)}
                                  </div>
                                </div>
                                <div style={{ fontSize: "0.75rem", marginTop: "0.2rem", color: "rgb(var(--muted))" }}>
                                  {src.summary || "(no summary)"}
                                </div>
                                <div style={{ fontSize: "0.65rem", color: "rgb(var(--muted))", fontFamily: "var(--font-mono)", marginTop: "0.2rem" }}>
                                  {src.namespace}
                                </div>
                              </button>
                            ))}
                          </div>
                        </div>
                      )}
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
