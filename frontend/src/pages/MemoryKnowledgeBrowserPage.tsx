import {
  Button,
  Callout,
  Card,
  Input,
  Label,
  Pill,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@hollis-labs/sysop-ui";
import {
  ChevronDown,
  ChevronRight,
  FileText,
  FolderOpen,
  ListChecks,
  PenSquare,
  RefreshCw,
  Search,
  Tag,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { listNamespaces, recall, registerNamespace } from "../api/client";
import type { NamespaceListItem, RecallBriefItem } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { Spinner } from "../components/ui/Spinner";

type DomainFilter = "both" | "memory" | "knowledge";

interface Props {
  onOpenItem?: (domain: "memory" | "knowledge", namespace: string, key: string) => void;
}

// Group namespaces by their first path segment so the tree mirrors the tier
// model (user/, app/, etc.) operators are familiar with from Context Explorer.
interface NamespaceGroup {
  prefix: string;
  namespaces: NamespaceListItem[];
}

// Heuristic: if the namespace path contains "/knowledge" the records under it
// are most likely knowledge-domain. Memory is the default fallback. The store
// doesn't tag namespaces with a domain — this is a pure naming convention.
function inferDomain(ns: string): "memory" | "knowledge" {
  return ns.includes("/knowledge") ? "knowledge" : "memory";
}

export function MemoryKnowledgeBrowserPage({ onOpenItem }: Props) {
  const [namespaces, setNamespaces] = useState<NamespaceListItem[]>([]);
  const [loadingNs, setLoadingNs] = useState(true);
  const [nsError, setNsError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [domain, setDomain] = useState<DomainFilter>("both");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [keysByNamespace, setKeysByNamespace] = useState<Record<string, RecallBriefItem[]>>({});
  const [loadingKeys, setLoadingKeys] = useState<Set<string>>(new Set());

  // Eager record counts (separate from expansion). Populated by the "Load
  // counts" button — fires recall in parallel for each visible namespace.
  // Stored in same cache as keysByNamespace so an eager-loaded namespace can
  // be expanded for free.
  const [loadingAllCounts, setLoadingAllCounts] = useState(false);

  // Register-namespace inline form state.
  const [registerOpen, setRegisterOpen] = useState(false);
  const [regNs, setRegNs] = useState("");
  const [regOwnerType, setRegOwnerType] = useState("user");
  const [regOwnerId, setRegOwnerId] = useState("");
  const [regSubmitting, setRegSubmitting] = useState(false);

  const loadNamespaces = useCallback(() => {
    setLoadingNs(true);
    setNsError(null);
    listNamespaces({ limit: 1000 })
      .then((res) => setNamespaces(res.items))
      .catch((err: unknown) => {
        const msg = err instanceof Error ? err.message : String(err);
        setNsError(msg);
        toast.error(`Namespace list failed: ${msg}`);
      })
      .finally(() => setLoadingNs(false));
  }, []);

  useEffect(() => {
    loadNamespaces();
  }, [loadNamespaces]);

  const filtered = useMemo(() => {
    let items = namespaces;
    const q = filter.trim().toLowerCase();
    if (q) items = items.filter((n) => n.namespace.toLowerCase().includes(q));
    if (domain !== "both") {
      items = items.filter((n) => inferDomain(n.namespace) === domain);
    }
    return items;
  }, [namespaces, filter, domain]);

  const groups = useMemo<NamespaceGroup[]>(() => {
    const map = new Map<string, NamespaceListItem[]>();
    for (const ns of filtered) {
      const prefix = ns.namespace.split("/")[0] ?? ns.namespace;
      const arr = map.get(prefix) ?? [];
      arr.push(ns);
      map.set(prefix, arr);
    }
    return Array.from(map.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([prefix, namespaces]) => ({ prefix, namespaces }));
  }, [filtered]);

  const toggleExpand = async (ns: string) => {
    const next = new Set(expanded);
    if (next.has(ns)) {
      next.delete(ns);
      setExpanded(next);
      return;
    }
    next.add(ns);
    setExpanded(next);

    if (keysByNamespace[ns]) return;

    const ld = new Set(loadingKeys);
    ld.add(ns);
    setLoadingKeys(ld);

    try {
      const res = await recall({ namespace: ns, limit: 200, format: "brief" });
      const items = (res.results as RecallBriefItem[]) ?? [];
      setKeysByNamespace((prev) => ({ ...prev, [ns]: items }));
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      toast.error(`Load keys failed for ${ns}: ${msg}`);
    } finally {
      setLoadingKeys((prev) => {
        const n = new Set(prev);
        n.delete(ns);
        return n;
      });
    }
  };

  const totalKeysFor = (ns: string): number | undefined => {
    const items = keysByNamespace[ns];
    return items ? items.length : undefined;
  };

  // Load record counts for every namespace currently in `filtered` (in
  // parallel). Skips namespaces already cached. Bounds concurrency to 8 to
  // avoid hammering the daemon.
  const loadAllCounts = async () => {
    setLoadingAllCounts(true);
    try {
      const targets = filtered.filter((n) => !keysByNamespace[n.namespace]);
      const concurrency = 8;
      let cursor = 0;
      const next: Record<string, RecallBriefItem[]> = {};
      const errors: string[] = [];
      const worker = async () => {
        while (cursor < targets.length) {
          const i = cursor++;
          const t = targets[i];
          if (!t) continue;
          try {
            const res = await recall({ namespace: t.namespace, limit: 500, format: "brief" });
            next[t.namespace] = (res.results as RecallBriefItem[]) ?? [];
          } catch (err) {
            errors.push(`${t.namespace}: ${err instanceof Error ? err.message : String(err)}`);
            next[t.namespace] = [];
          }
        }
      };
      await Promise.all(Array.from({ length: concurrency }, () => worker()));
      setKeysByNamespace((prev) => ({ ...prev, ...next }));
      if (errors.length === 0) {
        toast.success(
          `Loaded counts for ${targets.length} namespace${targets.length === 1 ? "" : "s"}`,
        );
      } else {
        toast.error(`${errors.length}/${targets.length} count loads failed`);
      }
    } finally {
      setLoadingAllCounts(false);
    }
  };

  const handleRegister = async () => {
    const ns = regNs.trim();
    const ownerId = regOwnerId.trim();
    if (!ns || !ownerId) {
      toast.error("Namespace and owner_id are required");
      return;
    }
    setRegSubmitting(true);
    try {
      await registerNamespace(ns, regOwnerType, ownerId, {});
      toast.success(`Registered ${ns}`);
      setRegNs("");
      setRegOwnerId("");
      setRegisterOpen(false);
      loadNamespaces();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      toast.error(`Register failed: ${msg}`);
    } finally {
      setRegSubmitting(false);
    }
  };

  return (
    <div className="min-h-full bg-bg text-text">
      <section className="border-b border-border-strong px-4 py-4">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold tracking-tight">Memory and knowledge browser</h2>
            <p className="mt-1 max-w-2xl text-sm leading-6 text-text-subtle">
              Inspect registered namespaces and their current memory or knowledge keys.
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setRegisterOpen((value) => !value)}
              title="Register a new namespace"
            >
              {registerOpen ? <X aria-hidden="true" /> : <PenSquare aria-hidden="true" />}
              {registerOpen ? "Cancel" : "Register"}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void loadAllCounts()}
              disabled={loadingAllCounts || filtered.length === 0}
              title="Load record counts for every visible namespace"
            >
              {loadingAllCounts ? <Spinner size={11} /> : <ListChecks aria-hidden="true" />} Load
              counts
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={loadNamespaces}
              disabled={loadingNs}
            >
              {loadingNs ? <Spinner size={11} /> : <RefreshCw aria-hidden="true" />} Refresh
            </Button>
          </div>
        </div>
      </section>

      <div className="space-y-3 p-4">
        {registerOpen ? (
          <Card size="sm" className="border-status-doing">
            <div className="grid items-end gap-4 p-4 md:grid-cols-[2fr_1fr_1fr_auto]">
              <div className="space-y-2">
                <Label htmlFor="reg-ns">Namespace</Label>
                <Input
                  id="reg-ns"
                  className="font-mono"
                  placeholder="user/<actor>/memory or user/<actor>/knowledge/<scope>"
                  value={regNs}
                  onChange={(event) => setRegNs(event.target.value)}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="reg-owner-type">Owner type</Label>
                <Select
                  value={regOwnerType}
                  onValueChange={(value) => {
                    if (value) setRegOwnerType(value);
                  }}
                >
                  <SelectTrigger id="reg-owner-type" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {["user", "app", "system"].map((item) => (
                      <SelectItem key={item} value={item}>
                        {item}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-2">
                <Label htmlFor="reg-owner-id">Owner ID</Label>
                <Input
                  id="reg-owner-id"
                  className="font-mono"
                  placeholder="chrispian / hadron / …"
                  value={regOwnerId}
                  onChange={(event) => setRegOwnerId(event.target.value)}
                />
              </div>
              <Button
                type="button"
                onClick={() => void handleRegister()}
                disabled={regSubmitting || !regNs.trim() || !regOwnerId.trim()}
              >
                {regSubmitting ? <Spinner size={11} /> : <PenSquare aria-hidden="true" />} Register
              </Button>
            </div>
            <p className="border-t border-border-soft px-4 py-2 text-xs text-text-subtle">
              Registers with an empty policy. Use Policy Manager to configure tier, retention, and
              allowed operations.
            </p>
          </Card>
        ) : null}

        <section
          className="flex flex-wrap items-center gap-3 border-y border-border-strong bg-panel px-3 py-2"
          aria-label="Browser filters"
        >
          <fieldset className="flex gap-1">
            <legend className="sr-only">Domain</legend>
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
          </fieldset>
          <Search className="size-4 text-text-subtle" aria-hidden="true" />
          <Input
            className="min-w-52 flex-1 font-mono"
            aria-label="Filter namespaces"
            placeholder="Filter namespaces…"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
          <span className="font-mono text-xs text-text-subtle" aria-live="polite">
            {filtered.length} namespace{filtered.length === 1 ? "" : "s"}
          </span>
        </section>

        {nsError ? (
          <Callout tone="danger" title="Namespace list failed">
            {nsError}
          </Callout>
        ) : null}

        <Card size="sm" className="overflow-hidden">
          {loadingNs && namespaces.length === 0 ? (
            <div className="flex justify-center py-8 text-text-subtle">
              <Spinner size={20} />
            </div>
          ) : null}
          {!loadingNs && groups.length === 0 ? (
            <EmptyState
              message="No namespaces match"
              sub={filter ? "Try a different filter" : "Register a namespace to get started"}
            />
          ) : null}
          {groups.map((group) => (
            <section key={group.prefix} aria-label={`${group.prefix} namespaces`}>
              <div className="border-b border-border-strong bg-panel-hover-soft px-3 py-2 font-mono text-xs text-text-subtle">
                {group.prefix}/ · {group.namespaces.length} namespace
                {group.namespaces.length === 1 ? "" : "s"}
              </div>
              {group.namespaces.map((namespaceItem) => {
                const isExpanded = expanded.has(namespaceItem.namespace);
                const isLoading = loadingKeys.has(namespaceItem.namespace);
                const keys = keysByNamespace[namespaceItem.namespace];
                const inferred = inferDomain(namespaceItem.namespace);
                const regionId = `browser-${encodeURIComponent(namespaceItem.namespace)}`;
                return (
                  <div
                    key={namespaceItem.namespace}
                    className="border-b border-border-soft last:border-b-0"
                  >
                    <Button
                      type="button"
                      variant="ghost"
                      className="h-auto w-full justify-start rounded-none px-3 py-2 font-normal"
                      onClick={() => void toggleExpand(namespaceItem.namespace)}
                      aria-expanded={isExpanded}
                      aria-controls={regionId}
                    >
                      {isExpanded ? (
                        <ChevronDown aria-hidden="true" />
                      ) : (
                        <ChevronRight aria-hidden="true" />
                      )}
                      <FolderOpen className="text-status-doing" aria-hidden="true" />
                      <span className="min-w-0 flex-1 truncate text-left font-mono text-xs">
                        {namespaceItem.namespace}
                      </span>
                      <Pill tone="neutral">{inferred}</Pill>
                      {totalKeysFor(namespaceItem.namespace) !== undefined ? (
                        <span className="font-mono text-[11px] text-text-subtle">
                          {totalKeysFor(namespaceItem.namespace)} key
                          {totalKeysFor(namespaceItem.namespace) === 1 ? "" : "s"}
                        </span>
                      ) : null}
                      {isLoading ? <Spinner size={11} /> : null}
                    </Button>
                    {isExpanded && keys ? (
                      <div
                        id={regionId}
                        className="border-t border-border-soft bg-panel-hover-soft py-1 pl-8"
                      >
                        {keys.length === 0 ? (
                          <p className="px-3 py-2 text-xs text-text-subtle">
                            No records under this namespace.
                          </p>
                        ) : null}
                        {keys.map((item) => {
                          const itemDomain = item.domain === "knowledge" ? "knowledge" : "memory";
                          return (
                            <Button
                              type="button"
                              key={item.revision_id}
                              variant="ghost"
                              className="h-auto w-full justify-start rounded-none px-3 py-2 font-normal"
                              onClick={() =>
                                item.memory_key &&
                                onOpenItem?.(itemDomain, item.namespace, item.memory_key)
                              }
                              disabled={!item.memory_key || !onOpenItem}
                            >
                              <FileText className="text-text-subtle" aria-hidden="true" />
                              <span className="min-w-0 flex-1 truncate text-left font-mono text-xs">
                                {item.memory_key ?? "(no key)"}
                              </span>
                              <span className="font-mono text-[11px] text-text-subtle">
                                {item.domain}
                              </span>
                              <span className="font-mono text-[11px] text-text-subtle">
                                conf {item.confidence.toFixed(2)}
                              </span>
                              {item.tags.length > 0 ? (
                                <span
                                  className="flex items-center gap-1 font-mono text-[11px] text-text-subtle"
                                  title={item.tags.join(", ")}
                                >
                                  <Tag className="size-3" aria-hidden="true" />
                                  {item.tags.length}
                                </span>
                              ) : null}
                            </Button>
                          );
                        })}
                      </div>
                    ) : null}
                  </div>
                );
              })}
            </section>
          ))}
        </Card>
      </div>
    </div>
  );
}
