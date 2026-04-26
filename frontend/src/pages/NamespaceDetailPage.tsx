import { ArrowLeft, FileText } from "lucide-react";
import { useCallback, useMemo } from "react";
import { evaluateView } from "../api/client";
import { CopyButton } from "../components/ui/CopyButton";
import { EmptyState } from "../components/ui/EmptyState";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

interface Props {
  namespace: string;
  onBack: () => void;
  onOpenRecord: (namespace: string, key: string) => void;
}

export function NamespaceDetailPage({ namespace, onBack, onOpenRecord }: Props) {
  const fetcher = useCallback(
    () => evaluateView({ namespaces: [namespace], revision_scope: "head", limit: 200 }),
    [namespace],
  );
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);

  const keys = useMemo(() => {
    if (!data?.items) return [];
    return data.items
      .map((r) => ({
        key: r.key,
        revision: r.revision,
        actor: r.actor,
        created_at: r.created_at,
        checksum: r.checksum,
      }))
      .sort((a, b) => a.key.localeCompare(b.key));
  }, [data]);

  const latestActivity =
    keys.length > 0
      ? keys.reduce(
          (latest, k) => (k.created_at > latest ? k.created_at : latest),
          keys[0]!.created_at,
        )
      : null;

  return (
    <div>
      <div className="breadcrumbs">
        <button onClick={onBack}>
          <ArrowLeft size={12} /> Explorer
        </button>
        <ChevronSep />
        <span>{namespace}</span>
      </div>

      <div className="page-header">
        <h2 className="page-title">{namespace}</h2>
        <CopyButton text={namespace} />
        <div className="page-actions">
          <button className="hud-button-ghost" onClick={refresh} disabled={loading}>
            {loading ? <Spinner size={12} /> : "Refresh"}
          </button>
        </div>
      </div>

      {/* Stats bar */}
      <div
        style={{
          display: "flex",
          gap: "1.5rem",
          marginBottom: "1rem",
          fontSize: "0.8rem",
          color: "rgb(var(--muted))",
        }}
      >
        <span>
          {keys.length} key{keys.length !== 1 ? "s" : ""}
        </span>
        {latestActivity && <span>Latest: {new Date(latestActivity).toLocaleString()}</span>}
      </div>

      {error && (
        <div
          className="hud-panel"
          style={{ padding: "0.75rem", color: "rgb(var(--danger))", marginBottom: "0.75rem" }}
        >
          Error: {error.message}
        </div>
      )}

      <div className="hud-panel">
        {loading && !data && (
          <div style={{ padding: "2rem", textAlign: "center" }}>
            <Spinner size={20} />
          </div>
        )}

        {!loading && keys.length === 0 && <EmptyState message="No keys in this namespace" />}

        {keys.length > 0 && (
          <table className="hud-table">
            <thead>
              <tr>
                <th>Key</th>
                <th>Revision</th>
                <th>Actor</th>
                <th>Last Updated</th>
              </tr>
            </thead>
            <tbody>
              {keys.map((k) => (
                <tr key={k.key} onClick={() => onOpenRecord(namespace, k.key)}>
                  <td>
                    <div style={{ display: "flex", alignItems: "center", gap: "0.4rem" }}>
                      <FileText size={13} style={{ color: "rgb(var(--primary))", flexShrink: 0 }} />
                      {k.key}
                    </div>
                  </td>
                  <td style={{ color: "rgb(var(--muted))" }}>r{k.revision}</td>
                  <td style={{ color: "rgb(var(--muted))" }}>{k.actor}</td>
                  <td style={{ color: "rgb(var(--muted))", fontSize: "0.8rem" }}>
                    {new Date(k.created_at).toLocaleString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function ChevronSep() {
  return <span style={{ color: "rgb(var(--muted))", fontSize: "0.7rem" }}>/</span>;
}
