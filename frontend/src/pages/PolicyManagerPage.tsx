import { ChevronDown, ChevronRight, Plus, Shield } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { evaluateView, getNamespacePolicy, registerNamespace } from "../api/client";
import type { NamespacePolicy } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { JsonViewer } from "../components/ui/JsonViewer";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

type Tab = "list" | "register";

export function PolicyManagerPage() {
  const [tab, setTab] = useState<Tab>("list");

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Policy Manager</h2>
      </div>

      <div style={{ display: "flex", gap: "0.5rem", marginBottom: "1rem" }}>
        <button
          className={tab === "list" ? "hud-button-primary" : "hud-button-ghost"}
          onClick={() => setTab("list")}
        >
          Namespace Policies
        </button>
        <button
          className={tab === "register" ? "hud-button-primary" : "hud-button-ghost"}
          onClick={() => setTab("register")}
        >
          <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
            <Plus size={13} /> Register Namespace
          </span>
        </button>
      </div>

      {tab === "list" && <PolicyList />}
      {tab === "register" && <RegisterForm onRegistered={() => setTab("list")} />}
    </div>
  );
}

function PolicyList() {
  const fetcher = useCallback(() => evaluateView({ revision_scope: "head", limit: 500 }), []);
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);

  // Extract unique namespaces and fetch policies for each
  const namespaces = useMemo(() => {
    if (!data?.items) return [];
    const set = new Set(data.items.map((r) => r.namespace));
    return Array.from(set).sort();
  }, [data]);

  const [policies, setPolicies] = useState<Map<string, NamespacePolicy>>(new Map());
  const [loadingPolicies, setLoadingPolicies] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);

  const loadPolicies = useCallback(async () => {
    if (namespaces.length === 0) return;
    setLoadingPolicies(true);
    const map = new Map<string, NamespacePolicy>();
    await Promise.allSettled(
      namespaces.map(async (ns) => {
        try {
          const p = await getNamespacePolicy(ns);
          map.set(ns, p);
        } catch {
          // Namespace may not have a registered policy
        }
      }),
    );
    setPolicies(map);
    setLoadingPolicies(false);
  }, [namespaces]);

  // Fetch policies when namespaces change
  useEffect(() => {
    loadPolicies();
  }, [loadPolicies]);

  return (
    <div>
      <div
        style={{
          marginBottom: "0.5rem",
          display: "flex",
          justifyContent: "flex-end",
          gap: "0.5rem",
        }}
      >
        <button
          className="hud-button-ghost"
          onClick={() => {
            refresh();
            loadPolicies();
          }}
          disabled={loading || loadingPolicies}
        >
          {loading || loadingPolicies ? <Spinner size={12} /> : "Refresh"}
        </button>
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

        {!loading && namespaces.length === 0 && (
          <EmptyState message="No namespaces found" sub="Write a record to create a namespace" />
        )}

        {namespaces.length > 0 &&
          namespaces.map((ns) => {
            const policy = policies.get(ns);
            // Server may return a NamespacePolicy with the nested .policy field
            // null/missing; the TS type marks it required but the runtime value
            // doesn't always honor that. Treat absent as empty so the UI degrades
            // to "—" rows instead of black-screening the whole route.
            const p = policy?.policy ?? {};
            const extraKeys = Object.keys(p).filter(
              (k) =>
                ![
                  "tier",
                  "retention",
                  "max_revisions",
                  "max_bytes_per_key",
                  "allowed_ops",
                ].includes(k),
            );
            const isExpanded = expanded === ns;
            return (
              <div key={ns} style={{ borderBottom: "1px solid rgba(var(--border) / 0.5)" }}>
                <div
                  onClick={() => setExpanded(isExpanded ? null : ns)}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "0.5rem",
                    padding: "0.5rem 0.75rem",
                    cursor: "pointer",
                    transition: "background 0.1s",
                  }}
                  onMouseEnter={(e) =>
                    (e.currentTarget.style.background = "rgba(var(--panel2) / 0.6)")
                  }
                  onMouseLeave={(e) => (e.currentTarget.style.background = "transparent")}
                >
                  {isExpanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                  <Shield size={13} style={{ color: "rgb(var(--primary))", flexShrink: 0 }} />
                  <span style={{ fontSize: "0.85rem", fontFamily: "var(--font-mono)" }}>{ns}</span>
                  {policy && (
                    <span style={{ marginLeft: "auto", display: "flex", gap: "0.4rem" }}>
                      {p.tier && (
                        <span className="hud-badge-info" style={{ fontSize: "0.65rem" }}>
                          {p.tier}
                        </span>
                      )}
                      {p.retention && (
                        <span style={{ fontSize: "0.7rem", color: "rgb(var(--muted))" }}>
                          {p.retention}
                        </span>
                      )}
                    </span>
                  )}
                  {!policy && (
                    <span
                      style={{ marginLeft: "auto", fontSize: "0.7rem", color: "rgb(var(--muted))" }}
                    >
                      no policy
                    </span>
                  )}
                </div>
                {isExpanded && (
                  <div style={{ padding: "0 0.75rem 0.75rem" }}>
                    {policy ? (
                      <div
                        style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "0.5rem" }}
                      >
                        <div>
                          <div className="hud-label" style={{ marginBottom: "0.25rem" }}>
                            Owner
                          </div>
                          <div style={{ fontSize: "0.8rem" }}>
                            {policy.owner_type}:{policy.owner_id}
                          </div>
                        </div>
                        <div>
                          <div className="hud-label" style={{ marginBottom: "0.25rem" }}>
                            Tier
                          </div>
                          <div style={{ fontSize: "0.8rem" }}>{p.tier ?? "—"}</div>
                        </div>
                        <div>
                          <div className="hud-label" style={{ marginBottom: "0.25rem" }}>
                            Retention
                          </div>
                          <div style={{ fontSize: "0.8rem" }}>{p.retention ?? "—"}</div>
                        </div>
                        <div>
                          <div className="hud-label" style={{ marginBottom: "0.25rem" }}>
                            Max Revisions
                          </div>
                          <div style={{ fontSize: "0.8rem" }}>{p.max_revisions ?? "—"}</div>
                        </div>
                        <div>
                          <div className="hud-label" style={{ marginBottom: "0.25rem" }}>
                            Max Bytes/Key
                          </div>
                          <div style={{ fontSize: "0.8rem" }}>
                            {p.max_bytes_per_key ? formatBytes(p.max_bytes_per_key) : "—"}
                          </div>
                        </div>
                        <div>
                          <div className="hud-label" style={{ marginBottom: "0.25rem" }}>
                            Allowed Ops
                          </div>
                          <div style={{ fontSize: "0.8rem" }}>
                            {p.allowed_ops?.join(", ") ?? "all"}
                          </div>
                        </div>
                        {extraKeys.length > 0 && (
                          <div className="form-field-full" style={{ gridColumn: "1 / -1" }}>
                            <div className="hud-label" style={{ marginBottom: "0.25rem" }}>
                              Full Policy
                            </div>
                            <JsonViewer data={p} maxHeight="150px" />
                          </div>
                        )}
                      </div>
                    ) : (
                      <div style={{ fontSize: "0.8rem", color: "rgb(var(--muted))" }}>
                        No policy registered for this namespace. Use "Register Namespace" to create
                        one.
                      </div>
                    )}
                  </div>
                )}
              </div>
            );
          })}
      </div>
    </div>
  );
}

interface RegisterFormProps {
  onRegistered: () => void;
}

function RegisterForm({ onRegistered }: RegisterFormProps) {
  const [namespace, setNamespace] = useState("");
  const [ownerType, setOwnerType] = useState("user");
  const [ownerId, setOwnerId] = useState("");
  const [tier, setTier] = useState("standard");
  const [retention, setRetention] = useState("");
  const [maxRevisions, setMaxRevisions] = useState("");
  const [maxBytesPerKey, setMaxBytesPerKey] = useState("");
  const [allowedOps, setAllowedOps] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const canSubmit = namespace.trim() && ownerId.trim() && !submitting;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      const policy: Parameters<typeof registerNamespace>[3] = {};
      if (tier) policy.tier = tier;
      if (retention.trim()) policy.retention = retention.trim();
      if (maxRevisions.trim()) {
        const n = parseInt(maxRevisions);
        if (Number.isFinite(n) && n > 0) policy.max_revisions = n;
      }
      if (maxBytesPerKey.trim()) {
        const n = parseInt(maxBytesPerKey);
        if (Number.isFinite(n) && n > 0) policy.max_bytes_per_key = n;
      }
      if (allowedOps.trim()) {
        policy.allowed_ops = allowedOps
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean);
      }
      await registerNamespace(namespace.trim(), ownerType, ownerId.trim(), policy);
      toast.success(`Namespace "${namespace.trim()}" registered`);
      onRegistered();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Registration failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="hud-panel" style={{ padding: "1rem", maxWidth: 700 }}>
      {error && (
        <div
          style={{
            padding: "0.5rem 0.75rem",
            marginBottom: "0.75rem",
            background: "rgba(var(--danger) / 0.1)",
            borderRadius: "var(--radius-sm)",
            color: "rgb(var(--danger))",
            fontSize: "0.85rem",
          }}
        >
          {error}
        </div>
      )}

      <div className="hud-label" style={{ marginBottom: "0.5rem", color: "rgb(var(--primary))" }}>
        Namespace
      </div>
      <div className="form-grid">
        <div className="form-field form-field-full">
          <label className="hud-label">Namespace Pattern *</label>
          <input
            className="hud-input"
            placeholder="app/my-project/*"
            value={namespace}
            onChange={(e) => setNamespace(e.target.value)}
            style={{ width: "100%" }}
          />
        </div>
      </div>

      <div
        className="hud-label"
        style={{ marginBottom: "0.5rem", marginTop: "0.75rem", color: "rgb(var(--primary))" }}
      >
        Owner
      </div>
      <div className="form-grid">
        <div className="form-field">
          <label className="hud-label">Owner Type *</label>
          <select
            className="hud-input"
            value={ownerType}
            onChange={(e) => setOwnerType(e.target.value)}
            style={{ width: "100%" }}
          >
            <option value="user">user</option>
            <option value="app">app</option>
            <option value="system">system</option>
          </select>
        </div>
        <div className="form-field">
          <label className="hud-label">Owner ID *</label>
          <input
            className="hud-input"
            placeholder="jane"
            value={ownerId}
            onChange={(e) => setOwnerId(e.target.value)}
            style={{ width: "100%" }}
          />
        </div>
      </div>

      <div
        className="hud-label"
        style={{ marginBottom: "0.5rem", marginTop: "0.75rem", color: "rgb(var(--primary))" }}
      >
        Policy
      </div>
      <div className="form-grid">
        <div className="form-field">
          <label className="hud-label">Tier</label>
          <select
            className="hud-input"
            value={tier}
            onChange={(e) => setTier(e.target.value)}
            style={{ width: "100%" }}
          >
            <option value="standard">standard</option>
            <option value="premium">premium</option>
            <option value="ephemeral">ephemeral</option>
          </select>
        </div>
        <div className="form-field">
          <label className="hud-label">Retention</label>
          <input
            className="hud-input"
            placeholder="720h (30 days)"
            value={retention}
            onChange={(e) => setRetention(e.target.value)}
            style={{ width: "100%" }}
          />
        </div>
        <div className="form-field">
          <label className="hud-label">Max Revisions</label>
          <input
            className="hud-input"
            type="number"
            placeholder="100"
            value={maxRevisions}
            onChange={(e) => setMaxRevisions(e.target.value)}
            style={{ width: "100%" }}
          />
        </div>
        <div className="form-field">
          <label className="hud-label">Max Bytes/Key</label>
          <input
            className="hud-input"
            type="number"
            placeholder="1048576"
            value={maxBytesPerKey}
            onChange={(e) => setMaxBytesPerKey(e.target.value)}
            style={{ width: "100%" }}
          />
        </div>
        <div className="form-field form-field-full">
          <label className="hud-label">
            Allowed Ops <span style={{ color: "rgb(var(--muted))" }}>(comma-separated)</span>
          </label>
          <input
            className="hud-input"
            placeholder="read, write, promote"
            value={allowedOps}
            onChange={(e) => setAllowedOps(e.target.value)}
            style={{ width: "100%" }}
          />
        </div>
      </div>

      <div className="form-actions">
        <button className="hud-button-primary" onClick={handleSubmit} disabled={!canSubmit}>
          <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
            {submitting ? <Spinner size={13} /> : <Shield size={13} />}
            Register Namespace
          </span>
        </button>
      </div>
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
