import {
  Button,
  Callout,
  Card,
  CardContent,
  Input,
  Label,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
} from "@hollis-labs/sysop-ui";
import { MessageSquare, Search, Sparkles, Tag, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { listAllNamespaces, synthesisAsk, tesseractLookup } from "../api/client";
import type {
  MemoryStatus,
  SynthesisAskResponse,
  TesseractLookupResponse,
  TesseractLookupResultItem,
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
    // Pages until the registry is exhausted: a blanket search over a capped
    // list would silently miss whatever fell past the cap.
    listAllNamespaces()
      .then((res) => {
        setDefaultNamespaces(res.items.map((n) => n.namespace));
        if (!res.complete) {
          toast.warning(
            `Namespace registry did not finish loading: searching ${res.items.length} of ${res.count}.`,
          );
        }
      })
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
    if (!active?.response) return;
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
        prev.map((e) =>
          e.id === active.id ? { ...e, synthesis: synth, synthesisError: null } : e,
        ),
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
    <div className="min-h-full bg-bg text-text">
      <section className="border-b border-border-strong px-4 py-4">
        <h2 className="text-lg font-semibold tracking-tight">Search and research</h2>
        <p className="mt-1 max-w-2xl text-sm leading-6 text-text-subtle">
          Ask across memory and knowledge, refine the cited set, and synthesize an answer when
          needed.
        </p>
      </section>

      <div className="grid gap-4 p-4 xl:grid-cols-[23rem_minmax(0,1fr)]">
        <aside className="space-y-3" aria-label="Research controls">
          <Card size="sm">
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="search-question">
                  Question <span className="text-danger">*</span>
                </Label>
                <Textarea
                  id="search-question"
                  className="min-h-24"
                  placeholder="What does the store know about X? History of Y? Trace the concept Z…"
                  value={question}
                  onChange={(event) => setQuestion(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) void handleAsk();
                  }}
                  rows={4}
                />
                <p className="text-xs leading-5 text-text-subtle">
                  Press ⌘/Ctrl + Enter to ask. Curated results retain full citation data for
                  synthesis and inspection.
                </p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="search-namespaces">
                  Namespaces{" "}
                  <span className="font-normal text-text-subtle">
                    (blank searches all {defaultNamespaces.length})
                  </span>
                </Label>
                <Input
                  id="search-namespaces"
                  className="font-mono"
                  list="search-namespaces-recents"
                  placeholder="user/jane/memory, user/jane/knowledge/projects"
                  value={namespacesField}
                  onChange={(event) => setNamespacesField(event.target.value)}
                />
                <datalist id="search-namespaces-recents">
                  {recentNamespaces.map((namespace) => (
                    <option key={namespace} value={namespace} label="recent" />
                  ))}
                </datalist>
                {recentNamespaces.length > 0 ? (
                  <div className="flex flex-wrap gap-1">
                    {recentNamespaces.slice(0, 5).map((namespace) => (
                      <Button
                        key={namespace}
                        type="button"
                        variant="outline"
                        size="xs"
                        className="max-w-full truncate font-mono"
                        onClick={() => {
                          const existing = namespacesField
                            .split(",")
                            .map((item) => item.trim())
                            .filter(Boolean);
                          if (!existing.includes(namespace))
                            setNamespacesField([...existing, namespace].join(", "));
                        }}
                        title={`Add ${namespace} to namespaces`}
                      >
                        {namespace}
                      </Button>
                    ))}
                  </div>
                ) : null}
              </div>
              <div className="space-y-2">
                <Label htmlFor="search-tags">
                  Tags <span className="font-normal text-text-subtle">(comma-separated)</span>
                </Label>
                <Input
                  id="search-tags"
                  className="font-mono"
                  placeholder="decision, scope:agent-ops.steward.main"
                  value={tagsField}
                  onChange={(event) => setTagsField(event.target.value)}
                />
              </div>
              <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-[1fr_8rem]">
                <fieldset className="space-y-2">
                  <legend className="text-sm font-medium">Domain</legend>
                  <div className="flex flex-wrap gap-1">
                    {(["both", "memory", "knowledge"] as const).map((item) => (
                      <Button
                        key={item}
                        type="button"
                        variant={domain === item ? "default" : "outline"}
                        size="xs"
                        onClick={() => setDomain(item)}
                        aria-pressed={domain === item}
                      >
                        {item}
                      </Button>
                    ))}
                  </div>
                </fieldset>
                <div className="space-y-2">
                  <Label htmlFor="search-confidence">Min confidence</Label>
                  <Input
                    id="search-confidence"
                    className="font-mono"
                    type="number"
                    step="0.05"
                    min="0"
                    max="1"
                    value={confidenceMin}
                    onChange={(event) => setConfidenceMin(event.target.value)}
                  />
                </div>
              </div>
              <fieldset className="space-y-2">
                <legend className="text-sm font-medium">Statuses</legend>
                <div className="flex flex-wrap gap-1">
                  {STATUS_FILTERS.map((item) => (
                    <Button
                      key={item}
                      type="button"
                      variant={statusSet.has(item) ? "default" : "outline"}
                      size="xs"
                      onClick={() => toggleStatus(item)}
                      aria-pressed={statusSet.has(item)}
                    >
                      {item}
                    </Button>
                  ))}
                </div>
              </fieldset>
              <div className="space-y-2">
                <Label htmlFor="search-limit">Limit</Label>
                <Input
                  id="search-limit"
                  className="font-mono"
                  type="number"
                  min="1"
                  max="500"
                  value={limit}
                  onChange={(event) => setLimit(event.target.value)}
                />
              </div>
              <Button
                type="button"
                className="w-full"
                onClick={() => void handleAsk()}
                disabled={loading || !question.trim()}
              >
                {loading ? <Spinner size={13} /> : <Search aria-hidden="true" />}
                {thread.length > 0 ? "Ask follow-up" : "Ask"}
              </Button>

              <section
                className="space-y-2 border-t border-border-strong pt-3"
                aria-labelledby="search-presets"
              >
                <h3 id="search-presets" className="text-sm font-medium">
                  Presets
                </h3>
                <div className="flex gap-2">
                  <Input
                    aria-label="Preset name"
                    className="min-w-0 flex-1 font-mono"
                    placeholder="Preset name…"
                    value={presetName}
                    onChange={(event) => setPresetName(event.target.value)}
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
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
                      setPresets((previous) =>
                        [preset, ...previous.filter((item) => item.name !== name)].slice(0, 20),
                      );
                      setPresetName("");
                      toast.success(`Saved preset "${name}"`);
                    }}
                  >
                    Save
                  </Button>
                </div>
                {presets.length === 0 ? (
                  <p className="text-xs text-text-subtle">No saved presets yet.</p>
                ) : (
                  <div className="space-y-1">
                    {presets.map((preset) => (
                      <div key={preset.name} className="flex gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="min-w-0 flex-1 justify-start truncate font-mono font-normal"
                          onClick={() => {
                            setQuestion(preset.question);
                            setNamespacesField(preset.namespacesField);
                            setTagsField(preset.tagsField);
                            setDomain(preset.domain);
                            setStatusSet(new Set(preset.statuses));
                            setConfidenceMin(preset.confidenceMin);
                            setLimit(preset.limit);
                            toast.success(`Loaded preset "${preset.name}"`);
                          }}
                        >
                          {preset.name}
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          onClick={() => {
                            setPresets((previous) =>
                              previous.filter((item) => item.name !== preset.name),
                            );
                            toast.success(`Removed preset "${preset.name}"`);
                          }}
                          title="Delete preset"
                          aria-label={`Delete preset ${preset.name}`}
                        >
                          <Trash2 aria-hidden="true" />
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
              </section>
            </CardContent>
          </Card>

          {thread.length > 0 ? (
            <Card size="sm">
              <div className="flex items-center justify-between border-b border-border-strong px-3 py-2">
                <h3 className="flex items-center gap-2 text-sm font-medium">
                  <MessageSquare className="size-4" aria-hidden="true" />
                  Thread ({thread.length})
                </h3>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  onClick={handleClearThread}
                  title="Clear thread"
                  aria-label="Clear thread"
                >
                  <Trash2 aria-hidden="true" />
                </Button>
              </div>
              <div className="p-1">
                {thread.map((entry) => (
                  <Button
                    key={entry.id}
                    type="button"
                    variant="ghost"
                    className={`h-auto w-full flex-col items-start rounded-none border-l-2 px-3 py-2 text-left font-normal ${entry.id === activeId ? "border-status-doing bg-panel-hover-soft" : "border-transparent"}`}
                    onClick={() => setActiveId(entry.id)}
                  >
                    <span className="max-w-full truncate text-sm">{entry.question}</span>
                    <span
                      className={`mt-1 text-xs ${entry.error ? "text-danger" : "text-text-subtle"}`}
                    >
                      {entry.error
                        ? "error"
                        : entry.response
                          ? `${entry.response.results.length} hits`
                          : "loading…"}
                    </span>
                  </Button>
                ))}
              </div>
            </Card>
          ) : null}
        </aside>

        <main className="min-w-0" aria-label="Research results">
          {!active ? (
            <Card size="sm">
              <div className="py-8">
                <EmptyState
                  message="Ask a question to begin."
                  sub="Each ask becomes a thread entry that you can revisit."
                />
              </div>
            </Card>
          ) : (
            <div className="space-y-3">
              <Card size="sm">
                <CardContent>
                  <p className="text-base leading-6">{active.question}</p>
                  <time className="mt-2 block font-mono text-xs text-text-subtle">
                    asked {active.asked_at}
                  </time>
                </CardContent>
              </Card>
              {active.error ? (
                <Callout tone="danger" title="Search failed">
                  {active.error}
                </Callout>
              ) : null}
              {!active.response && !active.error ? (
                <div className="flex justify-center py-8 text-text-subtle">
                  <Spinner size={20} />
                </div>
              ) : null}
              {active.response ? (
                <Tabs value={tab} onValueChange={(value) => setTab(value as Tab)} className="gap-3">
                  <div className="flex flex-wrap items-center gap-1 border-b border-border-strong">
                    <TabsList variant="line" aria-label="Research views">
                      {(["answer", "synthesis", "sources"] as const).map((item) => (
                        <TabsTrigger key={item} value={item}>
                          {item[0]?.toUpperCase()}
                          {item.slice(1)}
                        </TabsTrigger>
                      ))}
                    </TabsList>
                    <span className="ml-auto px-2 font-mono text-xs text-text-subtle">
                      {active.response.results.length} result
                      {active.response.results.length === 1 ? "" : "s"}
                      {active.response.facets.domains
                        ? ` · ${Object.entries(active.response.facets.domains)
                            .map(([itemDomain, count]) => `${itemDomain}: ${count}`)
                            .join(", ")}`
                        : ""}
                    </span>
                  </div>

                  <TabsContent value="answer">
                    <div className="space-y-4">
                      <Callout tone="info" title="Curated answer">
                        The store returned {active.response.results.length} relevance-ranked
                        revision{active.response.results.length === 1 ? "" : "s"}, grouped by domain
                        with summaries first. Open Sources for full citation payloads or Synthesis
                        for an LLM-backed answer.
                      </Callout>
                      {groupedByDomain(active.response.results).map(([itemDomain, items]) => (
                        <section key={itemDomain} aria-labelledby={`results-${itemDomain}`}>
                          <h3
                            id={`results-${itemDomain}`}
                            className="border-b border-border-strong pb-2 text-sm font-medium text-status-doing"
                          >
                            {itemDomain} · {items.length}
                          </h3>
                          <div className="mt-2 space-y-2">
                            {items.map((result) => (
                              <Card key={result.revision.revision_id} size="sm">
                                <CardContent className="space-y-2">
                                  <Button
                                    type="button"
                                    variant="ghost"
                                    className="h-auto w-full flex-col items-stretch p-0 text-left font-normal"
                                    onClick={() =>
                                      result.revision.memory_key &&
                                      onOpenItem?.(
                                        result.revision.domain,
                                        result.revision.namespace,
                                        result.revision.memory_key,
                                      )
                                    }
                                    disabled={!result.revision.memory_key || !onOpenItem}
                                  >
                                    <span className="flex flex-wrap items-center justify-between gap-2">
                                      <span className="font-mono text-sm text-status-doing">
                                        {result.revision.memory_key ?? "(no key)"}
                                      </span>
                                      <span className="flex items-center gap-2">
                                        {result.revision.status !== undefined ? (
                                          <StatusBadge status={result.revision.status} />
                                        ) : null}
                                        {result.score !== undefined ? (
                                          <span className="font-mono text-xs text-text-subtle">
                                            score {result.score.toFixed(3)}
                                          </span>
                                        ) : null}
                                        <span className="font-mono text-xs text-text-subtle">
                                          conf {result.revision.confidence?.toFixed(2) ?? "—"}
                                        </span>
                                      </span>
                                    </span>
                                    <span className="mt-2 whitespace-normal text-sm leading-5 text-text">
                                      {result.revision.payload?.summary || "(no summary)"}
                                    </span>
                                  </Button>
                                  <div className="flex flex-wrap items-center justify-between gap-2">
                                    <span className="flex flex-wrap gap-1">
                                      {(result.revision.tags ?? []).slice(0, 5).map((tag) => (
                                        <Button
                                          type="button"
                                          key={tag}
                                          variant="outline"
                                          size="xs"
                                          className="font-mono"
                                          onClick={() => {
                                            const existing = tagsField
                                              .split(",")
                                              .map((item) => item.trim())
                                              .filter(Boolean);
                                            if (!existing.includes(tag)) {
                                              setTagsField([...existing, tag].join(", "));
                                              toast.success(`Added tag: ${tag}`);
                                            }
                                          }}
                                          title={`Add tag "${tag}" to filter and re-ask`}
                                        >
                                          <Tag aria-hidden="true" />
                                          {tag}
                                        </Button>
                                      ))}
                                    </span>
                                    <span className="break-all font-mono text-xs text-text-subtle">
                                      {result.revision.namespace}
                                    </span>
                                  </div>
                                </CardContent>
                              </Card>
                            ))}
                          </div>
                        </section>
                      ))}
                    </div>
                  </TabsContent>

                  <TabsContent value="synthesis">
                    <div className="space-y-3">
                      {!active.synthesis && !active.synthesisError ? (
                        <Card size="sm" className="border-status-doing">
                          <CardContent className="text-center">
                            <p className="text-sm leading-6 text-text-subtle">
                              Synthesize an LLM-backed answer from {active.response.results.length}{" "}
                              cited source{active.response.results.length === 1 ? "" : "s"}.
                              Provider, model, token, cost, and latency telemetry remain attached.
                            </p>
                            <Button
                              type="button"
                              className="mt-3"
                              onClick={() => void handleSynthesize()}
                              disabled={synthLoading}
                            >
                              {synthLoading ? (
                                <Spinner size={13} />
                              ) : (
                                <Sparkles aria-hidden="true" />
                              )}{" "}
                              Synthesize
                            </Button>
                          </CardContent>
                        </Card>
                      ) : null}
                      {active.synthesisError && !active.synthesis ? (
                        <Callout tone="danger" title="Synthesis failed">
                          <div className="space-y-2">
                            <p>{active.synthesisError}</p>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              onClick={() => void handleSynthesize()}
                              disabled={synthLoading}
                            >
                              Retry
                            </Button>
                          </div>
                        </Callout>
                      ) : null}
                      {active.synthesis ? (
                        <>
                          <Card size="sm" className="border-status-doing">
                            <CardContent>
                              <h3 className="flex items-center gap-2 text-sm font-medium">
                                <Sparkles className="size-4" aria-hidden="true" />
                                Synthesized answer
                              </h3>
                              <p className="mt-3 whitespace-pre-wrap text-sm leading-6">
                                {active.synthesis.answer}
                              </p>
                            </CardContent>
                          </Card>
                          <div className="flex flex-wrap gap-x-4 gap-y-1 border-y border-border-strong bg-panel px-3 py-2 font-mono text-xs text-text-subtle">
                            <span>
                              {active.synthesis.usage.provider} / {active.synthesis.usage.model}
                            </span>
                            <span>{active.synthesis.usage.latency_ms}ms</span>
                            {active.synthesis.usage.input_tokens > 0 ? (
                              <span>
                                {active.synthesis.usage.input_tokens} in /{" "}
                                {active.synthesis.usage.output_tokens} out tokens
                              </span>
                            ) : null}
                            <span>
                              {active.synthesis.usage.cost
                                ? `$${active.synthesis.usage.cost.total_usd.toFixed(6)} total`
                                : "cost unavailable"}
                            </span>
                          </div>
                          <section>
                            <h3 className="mb-2 text-sm font-medium text-status-doing">
                              Cited sources ({active.synthesis.sources.length})
                            </h3>
                            <div className="space-y-2">
                              {active.synthesis.sources.map((source) => (
                                <Button
                                  key={source.revision_id}
                                  type="button"
                                  variant="outline"
                                  className="h-auto w-full flex-col items-stretch px-3 py-2 text-left font-normal"
                                  onClick={() =>
                                    source.memory_key &&
                                    onOpenItem?.(source.domain, source.namespace, source.memory_key)
                                  }
                                  disabled={!source.memory_key || !onOpenItem}
                                >
                                  <span className="flex flex-wrap items-center justify-between gap-2">
                                    <span className="font-mono text-sm">
                                      <strong className="text-status-doing">[{source.n}]</strong>{" "}
                                      {source.memory_key ?? "(no key)"}
                                    </span>
                                    <span className="font-mono text-xs text-text-subtle">
                                      {source.domain} · conf {source.confidence.toFixed(2)}
                                    </span>
                                  </span>
                                  <span className="mt-1 whitespace-normal text-xs text-text-subtle">
                                    {source.summary || "(no summary)"}
                                  </span>
                                  <span className="mt-1 break-all font-mono text-xs text-text-subtle">
                                    {source.namespace}
                                  </span>
                                </Button>
                              ))}
                            </div>
                          </section>
                        </>
                      ) : null}
                    </div>
                  </TabsContent>

                  <TabsContent value="sources">
                    <Card size="sm">
                      <CardContent>
                        <h3 className="mb-3 text-sm font-medium">
                          Raw cited revisions ({active.response.results.length})
                        </h3>
                        <JsonViewer data={active.response.results} maxHeight="600px" />
                      </CardContent>
                    </Card>
                  </TabsContent>
                </Tabs>
              ) : null}
            </div>
          )}
        </main>
      </div>
    </div>
  );
}
