import { ArrowLeft, History, Tag, User } from "lucide-react";
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
    <div>
      <div className="page-header">
        <h2 className="page-title">{domain === "memory" ? "Memory" : "Knowledge"} Revision</h2>
        <div className="page-actions">
          {onBack && (
            <button type="button" className="hud-button-ghost" onClick={onBack}>
              <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                <ArrowLeft size={13} /> Back
              </span>
            </button>
          )}
        </div>
      </div>

      {/* Identity bar */}
      <div className="hud-panel" style={{ padding: "0.75rem", marginBottom: "0.75rem" }}>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "auto 1fr",
            gap: "0.4rem 1rem",
            fontSize: "0.8rem",
          }}
        >
          <div style={{ color: "rgb(var(--muted))" }}>namespace</div>
          <div style={{ fontFamily: "var(--font-mono)" }}>{namespace}</div>
          <div style={{ color: "rgb(var(--muted))" }}>key</div>
          <div style={{ fontFamily: "var(--font-mono)", color: "rgb(var(--primary))" }}>
            {memoryKey}
          </div>
          {current && (
            <>
              <div style={{ color: "rgb(var(--muted))" }}>status</div>
              <div>
                <StatusBadge status={current.status} />
              </div>
              <div style={{ color: "rgb(var(--muted))" }}>confidence</div>
              <div style={{ fontFamily: "var(--font-mono)" }}>{current.confidence.toFixed(3)}</div>
              <div style={{ color: "rgb(var(--muted))" }}>created</div>
              <div style={{ fontFamily: "var(--font-mono)" }}>{current.created_at}</div>
              <div style={{ color: "rgb(var(--muted))" }}>author</div>
              <div
                style={{
                  fontFamily: "var(--font-mono)",
                  display: "flex",
                  alignItems: "center",
                  gap: "0.3rem",
                }}
              >
                <User size={12} /> {current.author.agent_id}
                {current.author.agent_version && ` · v${current.author.agent_version}`}
              </div>
              {current.tags.length > 0 && (
                <>
                  <div style={{ color: "rgb(var(--muted))" }}>tags</div>
                  <div style={{ display: "flex", gap: "0.3rem", flexWrap: "wrap" }}>
                    {current.tags.map((t) => (
                      <span
                        key={t}
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
                        }}
                      >
                        <Tag size={9} /> {t}
                      </span>
                    ))}
                  </div>
                </>
              )}
            </>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div
        style={{
          display: "flex",
          gap: "0.25rem",
          marginBottom: "0.5rem",
          borderBottom: "1px solid rgb(var(--border))",
        }}
      >
        {(["summary", "payload", "history", "raw"] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => handleTab(t)}
            style={{
              padding: "0.4rem 0.75rem",
              background: tab === t ? "rgba(var(--primary) / 0.08)" : "transparent",
              border: "none",
              borderBottom: tab === t ? "2px solid rgb(var(--primary))" : "2px solid transparent",
              color: tab === t ? "rgb(var(--primary))" : "rgb(var(--muted))",
              cursor: "pointer",
              fontSize: "0.8rem",
              fontFamily: "var(--font-mono)",
              textTransform: "uppercase",
            }}
          >
            {t === "history" && (
              <History size={11} style={{ marginRight: "0.3rem", verticalAlign: "middle" }} />
            )}
            {t}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {loading && (
        <div style={{ padding: "2rem", textAlign: "center" }}>
          <Spinner size={20} />
        </div>
      )}

      {error && !loading && (
        <div className="hud-panel" style={{ padding: "0.75rem", color: "rgb(var(--danger))" }}>
          {error}
        </div>
      )}

      {!loading && !error && current && tab === "summary" && (
        <div className="hud-panel" style={{ padding: "1rem" }}>
          <div style={{ fontSize: "0.95rem", lineHeight: 1.55 }}>{current.payload.summary}</div>
          {current.payload.body && (
            <div
              style={{
                marginTop: "1rem",
                paddingTop: "0.75rem",
                borderTop: "1px solid rgb(var(--border))",
              }}
            >
              <div className="hud-label" style={{ marginBottom: "0.3rem" }}>
                Body
              </div>
              <pre
                style={{ whiteSpace: "pre-wrap", fontSize: "0.8rem", lineHeight: 1.5, margin: 0 }}
              >
                {current.payload.body}
              </pre>
            </div>
          )}
        </div>
      )}

      {!loading && !error && current && tab === "payload" && (
        <div className="hud-panel" style={{ padding: "0.75rem" }}>
          <JsonViewer data={current.payload} maxHeight="500px" />
        </div>
      )}

      {!loading && !error && current && tab === "raw" && (
        <div className="hud-panel" style={{ padding: "0.75rem" }}>
          <JsonViewer data={current} maxHeight="600px" />
        </div>
      )}

      {!loading && !error && tab === "history" && (
        <div>
          {historyLoading && (
            <div style={{ padding: "2rem", textAlign: "center" }}>
              <Spinner size={20} />
            </div>
          )}
          {!historyLoading && history && history.length === 0 && (
            <EmptyState message="No revision history." />
          )}
          {!historyLoading && history && history.length > 0 && (
            <div style={{ display: "flex", flexDirection: "column", gap: "0.4rem" }}>
              {history.map((rev) => (
                <div
                  key={rev.revision_id}
                  className="hud-panel"
                  style={{
                    padding: "0.6rem 0.75rem",
                    border:
                      rev.revision_id === current?.revision_id
                        ? "1px solid rgb(var(--primary))"
                        : undefined,
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
                        fontSize: "0.75rem",
                        color: "rgb(var(--muted))",
                      }}
                    >
                      {rev.revision_id}
                    </div>
                    <div
                      style={{
                        display: "flex",
                        gap: "0.5rem",
                        alignItems: "center",
                        fontSize: "0.7rem",
                        color: "rgb(var(--muted))",
                      }}
                    >
                      <StatusBadge status={rev.status} />
                      <span style={{ fontFamily: "var(--font-mono)" }}>
                        conf {rev.confidence.toFixed(2)}
                      </span>
                      <span style={{ fontFamily: "var(--font-mono)" }}>{rev.created_at}</span>
                    </div>
                  </div>
                  <div style={{ fontSize: "0.8rem", marginTop: "0.3rem" }}>
                    {rev.payload.summary}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
