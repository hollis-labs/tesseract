import {
  Button,
  Callout,
  Card,
  CardContent,
  Pill,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@hollis-labs/sysop-ui";
import { ArrowLeft, History, User } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import {
  getKnowledgeCurrent,
  getKnowledgeHistory,
  getMemoryCurrent,
  getMemoryHistory,
} from "../api/client";
import type { KnowledgeRevision, MemoryRevision } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { JsonViewer } from "../components/ui/JsonViewer";
import { Spinner } from "../components/ui/Spinner";
import { StatusBadge } from "../components/ui/StatusBadge";

interface Props {
  domain: "memory" | "knowledge";
  namespace: string;
  memoryKey: string;
  onBack?: () => void;
}

type Tab = "summary" | "payload" | "history" | "raw";

export function MemoryDetailPage({ domain, namespace, memoryKey, onBack }: Props) {
  const [current, setCurrent] = useState<MemoryRevision | KnowledgeRevision | null>(null);
  const [history, setHistory] = useState<(MemoryRevision | KnowledgeRevision)[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("summary");

  useEffect(() => {
    setLoading(true);
    setError(null);
    setCurrent(null);
    const fetcher = domain === "memory" ? getMemoryCurrent : getKnowledgeCurrent;
    fetcher(namespace, memoryKey)
      .then((rev) => setCurrent(rev))
      .catch((err: unknown) => {
        const msg = err instanceof Error ? err.message : String(err);
        setError(msg);
        toast.error(`Load failed: ${msg}`);
      })
      .finally(() => setLoading(false));
  }, [domain, namespace, memoryKey]);

  const loadHistory = async () => {
    if (history) return;
    setHistoryLoading(true);
    try {
      const fetcher = domain === "memory" ? getMemoryHistory : getKnowledgeHistory;
      const revs = await fetcher(namespace, memoryKey);
      setHistory(revs);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      toast.error(`History load failed: ${msg}`);
    } finally {
      setHistoryLoading(false);
    }
  };

  const handleTab = (t: Tab) => {
    setTab(t);
    if (t === "history") void loadHistory();
  };

  return (
    <div className="min-h-full bg-bg text-text">
      <section className="border-b border-border-strong px-4 py-4">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold tracking-tight">
              {domain === "memory" ? "Memory" : "Knowledge"} revision
            </h2>
            <p className="mt-1 font-mono text-xs text-text-subtle">{namespace}</p>
          </div>
          {onBack ? (
            <Button type="button" variant="outline" size="sm" onClick={onBack}>
              <ArrowLeft aria-hidden="true" /> Back
            </Button>
          ) : null}
        </div>
      </section>

      <div className="space-y-3 p-4">
        <Card size="sm">
          <CardContent className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
            <span className="text-text-subtle">Namespace</span>
            <span className="break-all font-mono">{namespace}</span>
            <span className="text-text-subtle">Key</span>
            <span className="break-all font-mono text-status-doing">{memoryKey}</span>
            {current ? (
              <>
                <span className="text-text-subtle">Status</span>
                <span>
                  <StatusBadge status={current.status} />
                </span>
                <span className="text-text-subtle">Confidence</span>
                <span className="font-mono tabular-nums">{current.confidence.toFixed(3)}</span>
                <span className="text-text-subtle">Created</span>
                <span className="font-mono">{current.created_at}</span>
                <span className="text-text-subtle">Author</span>
                <span className="flex items-center gap-1.5 font-mono">
                  <User className="size-3" aria-hidden="true" /> {current.author.agent_id}
                  {current.author.agent_version ? ` · v${current.author.agent_version}` : null}
                </span>
                {current.tags.length > 0 ? (
                  <>
                    <span className="text-text-subtle">Tags</span>
                    <span className="flex flex-wrap gap-1.5">
                      {current.tags.map((item) => (
                        <Pill key={item} tone="neutral">
                          {item}
                        </Pill>
                      ))}
                    </span>
                  </>
                ) : null}
              </>
            ) : null}
          </CardContent>
        </Card>

        <Tabs value={tab} onValueChange={(value) => handleTab(value as Tab)} className="gap-3">
          <TabsList variant="line" aria-label="Revision views">
            {(["summary", "payload", "history", "raw"] as const).map((item) => (
              <TabsTrigger key={item} value={item}>
                {item === "history" ? <History aria-hidden="true" /> : null}
                {item[0]?.toUpperCase()}
                {item.slice(1)}
              </TabsTrigger>
            ))}
          </TabsList>

          {loading ? (
            <div className="flex justify-center py-8 text-text-subtle">
              <Spinner size={20} />
            </div>
          ) : null}
          {error && !loading ? (
            <Callout tone="danger" title="Revision unavailable">
              {error}
            </Callout>
          ) : null}

          <TabsContent value="summary">
            {!loading && !error && current ? (
              <Card size="sm">
                <CardContent>
                  <p className="text-sm leading-6">{current.payload.summary}</p>
                  {current.payload.body ? (
                    <div className="mt-4 border-t border-border-strong pt-3">
                      <h3 className="text-sm font-medium">Body</h3>
                      <pre className="mt-2 whitespace-pre-wrap font-mono text-xs leading-5">
                        {current.payload.body}
                      </pre>
                    </div>
                  ) : null}
                </CardContent>
              </Card>
            ) : null}
          </TabsContent>

          <TabsContent value="payload">
            {!loading && !error && current ? (
              <Card size="sm">
                <CardContent>
                  <JsonViewer data={current.payload} maxHeight="500px" />
                </CardContent>
              </Card>
            ) : null}
          </TabsContent>
          <TabsContent value="raw">
            {!loading && !error && current ? (
              <Card size="sm">
                <CardContent>
                  <JsonViewer data={current} maxHeight="600px" />
                </CardContent>
              </Card>
            ) : null}
          </TabsContent>

          <TabsContent value="history">
            {!loading && !error ? (
              <section aria-label="Revision history">
                {historyLoading ? (
                  <div className="flex justify-center py-8 text-text-subtle">
                    <Spinner size={20} />
                  </div>
                ) : null}
                {!historyLoading && history?.length === 0 ? (
                  <EmptyState message="No revision history." />
                ) : null}
                {!historyLoading && history && history.length > 0 ? (
                  <div className="space-y-2">
                    {history.map((revision) => (
                      <Card
                        key={revision.revision_id}
                        size="sm"
                        className={
                          revision.revision_id === current?.revision_id
                            ? "border-status-doing"
                            : undefined
                        }
                      >
                        <CardContent className="space-y-2">
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="break-all font-mono text-xs text-text-subtle">
                              {revision.revision_id}
                            </span>
                            <span className="flex flex-wrap items-center gap-2 text-xs text-text-subtle">
                              <StatusBadge status={revision.status} />
                              <span className="font-mono">
                                conf {revision.confidence.toFixed(2)}
                              </span>
                              <span className="font-mono">{revision.created_at}</span>
                            </span>
                          </div>
                          <p className="text-sm">{revision.payload.summary}</p>
                        </CardContent>
                      </Card>
                    ))}
                  </div>
                ) : null}
              </section>
            ) : null}
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
