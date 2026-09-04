import { Button, Callout, Input } from "@hollis-labs/sysop-ui";
import { ChevronDown, ChevronRight, FileText, FolderOpen, RefreshCw, Search } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { evaluateView } from "../api/client";
import type { Record } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

interface Props {
  onOpenNamespace: (namespace: string) => void;
  onOpenRecord: (namespace: string, key: string) => void;
}

interface NamespaceGroup {
  namespace: string;
  keys: { key: string; revision: number; actor: string; created_at: string }[];
}

export function ExplorerPage({ onOpenNamespace, onOpenRecord }: Props) {
  const [search, setSearch] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const fetcher = useCallback(() => evaluateView({ revision_scope: "head", limit: 500 }), []);
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);

  const groups = useMemo<NamespaceGroup[]>(() => {
    if (!data?.items) return [];
    const map = new Map<string, NamespaceGroup>();
    for (const item of data.items as Record[]) {
      let group = map.get(item.namespace);
      if (!group) {
        group = { namespace: item.namespace, keys: [] };
        map.set(item.namespace, group);
      }
      group.keys.push({
        key: item.key,
        revision: item.revision,
        actor: item.actor,
        created_at: item.created_at,
      });
    }
    return Array.from(map.values()).sort((a, b) => a.namespace.localeCompare(b.namespace));
  }, [data]);

  const filtered = useMemo(() => {
    if (!search.trim()) return groups;
    const query = search.toLowerCase();
    return groups
      .map((group) => ({
        ...group,
        keys: group.keys.filter(
          (item) =>
            item.key.toLowerCase().includes(query) || group.namespace.toLowerCase().includes(query),
        ),
      }))
      .filter((group) => group.keys.length > 0 || group.namespace.toLowerCase().includes(query));
  }, [groups, search]);

  const toggleExpand = (namespace: string) => {
    setExpanded((previous) => {
      const next = new Set(previous);
      if (next.has(namespace)) next.delete(namespace);
      else next.add(namespace);
      return next;
    });
  };

  const totalRecords = data?.evaluation_meta?.matched_count ?? 0;

  return (
    <div className="flex min-h-full flex-col bg-bg text-text">
      <section className="border-b border-border-strong px-4 py-4">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div className="max-w-2xl">
            <h2 className="text-lg font-semibold tracking-tight">Browse context by namespace</h2>
            <p className="mt-1 text-sm leading-6 text-text-subtle">
              Expand a namespace to inspect its current keys, or open the namespace for revision
              history and metadata.
            </p>
          </div>
          <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
            {loading ? <Spinner size={14} /> : <RefreshCw aria-hidden="true" />}
            Refresh
          </Button>
        </div>
      </section>

      <section
        className="flex flex-wrap items-center gap-3 border-b border-border-strong bg-panel px-4 py-3"
        aria-label="Explorer filters"
      >
        <Search className="size-4 shrink-0 text-text-subtle" aria-hidden="true" />
        <Input
          className="min-w-52 flex-1 font-mono"
          aria-label="Filter namespaces and keys"
          placeholder="Filter namespaces and keys…"
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
        <span className="font-mono text-xs tabular-nums text-text-subtle" aria-live="polite">
          {filtered.length} namespaces / {totalRecords} records
          {data?.evaluation_meta?.truncated ? " / truncated" : ""}
        </span>
      </section>

      {error ? (
        <div className="px-4 pt-4">
          <Callout tone="danger" title="Explorer unavailable">
            {error.message}
          </Callout>
        </div>
      ) : null}

      <section className="min-h-0 flex-1 py-3" aria-label="Namespaces">
        <div className="border-y border-border-strong bg-panel">
          {loading && !data ? (
            <div className="flex justify-center py-12 text-text-subtle">
              <Spinner size={20} />
            </div>
          ) : null}

          {!loading && filtered.length === 0 ? (
            <EmptyState
              message="No records found"
              sub={search ? "Try a different search term." : "Write a record to create context."}
            />
          ) : null}

          {filtered.map((group) => {
            const isExpanded = expanded.has(group.namespace);
            const regionId = `namespace-${encodeURIComponent(group.namespace)}`;
            return (
              <div key={group.namespace} className="border-b border-border-soft last:border-b-0">
                <div className="flex items-center gap-2 px-2 py-1">
                  <Button
                    variant="ghost"
                    className="h-auto min-w-0 flex-1 justify-start px-2 py-2 font-normal"
                    onClick={() => toggleExpand(group.namespace)}
                    aria-expanded={isExpanded}
                    aria-controls={regionId}
                  >
                    {isExpanded ? (
                      <ChevronDown className="text-text-subtle" aria-hidden="true" />
                    ) : (
                      <ChevronRight className="text-text-subtle" aria-hidden="true" />
                    )}
                    <FolderOpen className="text-status-doing" aria-hidden="true" />
                    <span className="truncate font-mono text-sm text-text">{group.namespace}</span>
                    <span className="ml-auto shrink-0 font-mono text-[11px] text-text-subtle">
                      {group.keys.length} key{group.keys.length === 1 ? "" : "s"}
                    </span>
                  </Button>
                  <Button
                    variant="outline"
                    size="xs"
                    onClick={() => onOpenNamespace(group.namespace)}
                  >
                    Open
                  </Button>
                </div>

                {isExpanded ? (
                  <div
                    id={regionId}
                    className="border-t border-border-soft bg-panel-hover-soft py-1 pl-8"
                  >
                    {group.keys.map((item) => (
                      <Button
                        key={item.key}
                        variant="ghost"
                        className="h-auto w-full justify-start rounded-none px-3 py-2 font-normal"
                        onClick={() => onOpenRecord(group.namespace, item.key)}
                      >
                        <FileText className="text-text-subtle" aria-hidden="true" />
                        <span className="min-w-0 flex-1 truncate text-left font-mono text-xs text-text">
                          {item.key}
                        </span>
                        <span className="font-mono text-[11px] text-text-subtle">
                          r{item.revision}
                        </span>
                        <span className="max-w-40 truncate text-[11px] text-text-subtle">
                          {item.actor}
                        </span>
                      </Button>
                    ))}
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      </section>
    </div>
  );
}
