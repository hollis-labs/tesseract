import {
  Button,
  Callout,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Pill,
} from "@hollis-labs/sysop-ui";
import { Tag, Telescope } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { recall } from "../api/client";
import type { RecallBriefItem, RecallResponse } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { JsonViewer } from "../components/ui/JsonViewer";
import { Spinner } from "../components/ui/Spinner";

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
    return Array.isArray(parsed) ? parsed.filter((item) => typeof item === "string") : [];
  } catch {
    return [];
  }
}

function pushRecentNamespace(namespace: string): string[] {
  const current = loadRecentNamespaces();
  const next = [namespace, ...current.filter((item) => item !== namespace)].slice(
    0,
    RECENT_NAMESPACES_MAX,
  );
  try {
    window.localStorage.setItem(RECENT_NAMESPACES_KEY, JSON.stringify(next));
  } catch {
    // localStorage may be unavailable; recall itself should still work.
  }
  return next;
}

interface Props {
  onOpenItem?: (domain: "memory" | "knowledge", namespace: string, key: string) => void;
}

function readHashParams(): {
  namespace?: string;
  tags?: string;
  limit?: string;
  format?: "brief" | "full";
  domain?: "memory" | "knowledge";
} {
  if (typeof window === "undefined") return {};
  const hash = window.location.hash;
  const index = hash.indexOf("?");
  if (index < 0) return {};
  const params = new URLSearchParams(hash.slice(index + 1));
  const output: ReturnType<typeof readHashParams> = {};
  const namespace = params.get("namespace");
  if (namespace) output.namespace = namespace;
  const tags = params.get("tags");
  if (tags) output.tags = tags;
  const limit = params.get("limit");
  if (limit) output.limit = limit;
  const format = params.get("format");
  if (format === "brief" || format === "full") output.format = format;
  const domain = params.get("domain");
  if (domain === "memory" || domain === "knowledge") output.domain = domain;
  return output;
}

function writeHashParams(parameters: {
  namespace: string;
  tags: string;
  limit: string;
  format: string;
  domain: string;
}): void {
  if (typeof window === "undefined") return;
  const params = new URLSearchParams();
  if (parameters.namespace) params.set("namespace", parameters.namespace);
  if (parameters.tags) params.set("tags", parameters.tags);
  if (parameters.limit && parameters.limit !== "15") params.set("limit", parameters.limit);
  if (parameters.format && parameters.format !== "brief") params.set("format", parameters.format);
  if (parameters.domain) params.set("domain", parameters.domain);
  const query = params.toString();
  const nextHash = query ? `#recall?${query}` : "#recall";
  if (window.location.hash !== nextHash) history.replaceState(null, "", nextHash);
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

  useEffect(() => {
    writeHashParams({ namespace, tags, limit, format, domain: domainFilter });
  }, [namespace, tags, limit, format, domainFilter]);

  const handleRecall = async () => {
    const requestedNamespace = namespace.trim();
    if (!requestedNamespace) {
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
            .map((tag) => tag.trim())
            .filter(Boolean)
        : undefined;
      const parsedLimit = Number.parseInt(limit, 10);
      const parameters: Parameters<typeof recall>[0] = {
        namespace: requestedNamespace,
        format,
      };
      if (tagList) parameters.tags = tagList;
      if (Number.isFinite(parsedLimit) && parsedLimit > 0) parameters.limit = parsedLimit;
      const result = await recall(parameters);
      setResponse(result);
      setRecentNamespaces(pushRecentNamespace(requestedNamespace));
      toast.success(
        `Returned ${result.meta.returned} result${result.meta.returned === 1 ? "" : "s"}`,
      );
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      setError(message);
      toast.error(`Recall failed: ${message}`);
    } finally {
      setLoading(false);
    }
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") void handleRecall();
  };

  const allBriefItems: RecallBriefItem[] =
    format === "brief" && response ? (response.results as RecallBriefItem[]) : [];
  const briefItems = domainFilter
    ? allBriefItems.filter((item) => item.domain === domainFilter)
    : allBriefItems;
  const facetEntries = response ? Object.entries(response.facets.domains ?? {}) : [];

  return (
    <div className="min-h-full bg-bg text-text">
      <section className="border-b border-border-strong px-4 py-4">
        <h2 className="text-lg font-semibold tracking-tight">Recall relevant memory</h2>
        <p className="mt-1 max-w-2xl text-sm leading-6 text-text-subtle">
          Query one namespace, optionally narrow it with tags, and inspect the ranked memory or
          knowledge revisions returned by the service.
        </p>
      </section>

      <div
        className={
          response
            ? "grid gap-4 p-4 lg:grid-cols-[minmax(18rem,0.75fr)_minmax(0,1.25fr)]"
            : "max-w-2xl p-4"
        }
      >
        <Card size="sm" className="self-start">
          <CardHeader className="border-b border-border-strong">
            <CardTitle>Recall parameters</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="recall-namespace">
                Namespace <span className="text-danger">*</span>
              </Label>
              <Input
                id="recall-namespace"
                className="font-mono"
                list="recall-namespace-suggestions"
                placeholder="user/chrispian/memory"
                value={namespace}
                onChange={(event) => setNamespace(event.target.value)}
                onKeyDown={handleKeyDown}
                aria-describedby="recall-namespace-help"
                required
              />
              <datalist id="recall-namespace-suggestions">
                {recentNamespaces.map((item) => (
                  <option key={`recent-${item}`} value={item} label="recent" />
                ))}
                {NAMESPACE_SUGGESTIONS.map((item) => (
                  <option key={item} value={item} label="template" />
                ))}
              </datalist>
              <p id="recall-namespace-help" className="text-xs leading-5 text-text-subtle">
                Recent picks appear first. Replace the placeholders in namespace templates.
              </p>
            </div>

            <div className="space-y-2">
              <Label htmlFor="recall-tags">Tags (optional, comma-separated)</Label>
              <Input
                id="recall-tags"
                className="font-mono"
                placeholder="decision, scope:agent-ops.steward.main"
                value={tags}
                onChange={(event) => setTags(event.target.value)}
                onKeyDown={handleKeyDown}
              />
            </div>

            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="recall-limit">Limit</Label>
                <Input
                  id="recall-limit"
                  className="font-mono"
                  type="number"
                  min="1"
                  max="500"
                  value={limit}
                  onChange={(event) => setLimit(event.target.value)}
                  onKeyDown={handleKeyDown}
                />
              </div>
              <fieldset className="space-y-2">
                <legend className="text-sm font-medium">Format</legend>
                <div className="flex h-8 items-center gap-4">
                  {(["brief", "full"] as const).map((option) => (
                    <label key={option} className="flex cursor-pointer items-center gap-2 text-sm">
                      <input
                        type="radio"
                        name="recall-format"
                        value={option}
                        checked={format === option}
                        onChange={() => setFormat(option)}
                        className="accent-[var(--theme-color-accent)]"
                      />
                      {option}
                    </label>
                  ))}
                </div>
              </fieldset>
            </div>

            <Button onClick={() => void handleRecall()} disabled={loading || !namespace.trim()}>
              {loading ? <Spinner size={14} /> : <Telescope aria-hidden="true" />}
              Recall
            </Button>
          </CardContent>
        </Card>

        {response ? (
          <section className="min-w-0 space-y-3" aria-label="Recall results">
            <div className="border-y border-border-strong bg-panel px-4 py-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-sm">
                  <span className="text-text-subtle">Namespace </span>
                  <span className="font-mono">{response.meta.namespace}</span>
                </p>
                <p className="font-mono text-xs tabular-nums text-text-subtle">
                  {response.meta.returned} returned / {response.meta.limit} limit /{" "}
                  {response.meta.format}
                </p>
              </div>
              {facetEntries.length > 0 ? (
                <div className="mt-3 flex flex-wrap items-center gap-2">
                  {facetEntries.map(([domain, count]) => {
                    const active = domainFilter === domain;
                    const filterable = domain === "memory" || domain === "knowledge";
                    return (
                      <Button
                        type="button"
                        key={domain}
                        variant={active ? "default" : "outline"}
                        size="xs"
                        onClick={() => {
                          if (!filterable) return;
                          setDomainFilter(active ? "" : (domain as "memory" | "knowledge"));
                        }}
                        disabled={!filterable}
                        aria-pressed={active}
                      >
                        {domain} <span className="font-mono">{count}</span>
                      </Button>
                    );
                  })}
                  {domainFilter ? (
                    <span className="font-mono text-[11px] text-text-subtle" aria-live="polite">
                      showing {briefItems.length} / {allBriefItems.length}
                    </span>
                  ) : null}
                </div>
              ) : null}
            </div>

            {format === "brief" && briefItems.length === 0 ? (
              <EmptyState message="No recall results" sub="Adjust the namespace or tag filter." />
            ) : null}

            {format === "brief" && briefItems.length > 0 ? (
              <div className="border-y border-border-strong bg-panel">
                {briefItems.map((item) => {
                  const domain = item.domain === "knowledge" ? "knowledge" : "memory";
                  const canOpen = Boolean(item.memory_key && onOpenItem);
                  return (
                    <article
                      key={item.revision_id}
                      className="border-b border-border-soft last:border-b-0"
                    >
                      <Button
                        type="button"
                        variant="ghost"
                        className="h-auto w-full items-start justify-start rounded-none px-4 py-3 text-left font-normal"
                        onClick={() => {
                          if (item.memory_key)
                            onOpenItem?.(domain, item.namespace, item.memory_key);
                        }}
                        disabled={!canOpen}
                      >
                        <span className="min-w-0 flex-1">
                          <span className="flex flex-wrap items-baseline justify-between gap-2">
                            <span className="truncate font-mono text-sm text-status-doing">
                              {item.memory_key ?? "(no key)"}
                            </span>
                            <span className="font-mono text-[11px] text-text-subtle">
                              {item.domain} / confidence {item.confidence.toFixed(2)}
                            </span>
                          </span>
                          <span className="mt-1 block whitespace-normal text-sm leading-5 text-text-soft">
                            {item.summary || "(no summary)"}
                          </span>
                        </span>
                      </Button>
                      <div className="flex flex-wrap items-center gap-1.5 px-4 pb-3">
                        {item.tags.slice(0, 6).map((tag) => (
                          <Button
                            type="button"
                            key={tag}
                            variant="outline"
                            size="xs"
                            className="h-5 font-mono text-[10px] text-text-subtle"
                            onClick={() => {
                              const existing = tags
                                .split(",")
                                .map((value) => value.trim())
                                .filter(Boolean);
                              if (existing.includes(tag)) return;
                              setTags([...existing, tag].join(", "));
                              toast.success(`Added tag: ${tag}`);
                            }}
                            title={`Add tag "${tag}" to filter`}
                          >
                            <Tag aria-hidden="true" /> {tag}
                          </Button>
                        ))}
                        {item.tags.length > 6 ? (
                          <Pill tone="neutral">+{item.tags.length - 6}</Pill>
                        ) : null}
                        <time className="ml-auto font-mono text-[10px] text-text-subtle">
                          {item.created_at}
                        </time>
                      </div>
                    </article>
                  );
                })}
              </div>
            ) : null}

            {format === "full" ? (
              <Card size="sm">
                <CardHeader className="border-b border-border-strong">
                  <CardTitle>Raw RecallResult[]</CardTitle>
                </CardHeader>
                <CardContent>
                  <JsonViewer data={response.results} maxHeight="500px" />
                </CardContent>
              </Card>
            ) : null}
          </section>
        ) : null}
      </div>

      {error ? (
        <div className="px-4 pb-4">
          <Callout tone="danger" title="Recall failed">
            {error}
          </Callout>
        </div>
      ) : null}
    </div>
  );
}
