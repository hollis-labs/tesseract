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
    <div>
      <div className="page-header">
        <h2 className="page-title">Memory &amp; Knowledge Browser</h2>
        <div className="page-actions" style={{ display: "flex", gap: "0.3rem" }}>
          <button
            type="button"
            className="hud-button-ghost"
            onClick={() => setRegisterOpen((v) => !v)}
            title="Register a new namespace"
          >
            <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
              {registerOpen ? <X size={11} /> : <PenSquare size={11} />}
              {registerOpen ? "Cancel" : "Register"}
            </span>
          </button>
          <button
            type="button"
            className="hud-button-ghost"
            onClick={loadAllCounts}
            disabled={loadingAllCounts || filtered.length === 0}
            title="Load record counts for every visible namespace (parallel recall)"
          >
            <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
              {loadingAllCounts ? <Spinner size={11} /> : <ListChecks size={11} />} Load counts
            </span>
          </button>
          <button
            type="button"
            className="hud-button-ghost"
            onClick={loadNamespaces}
            disabled={loadingNs}
          >
            <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
              {loadingNs ? <Spinner size={11} /> : <RefreshCw size={11} />} Refresh
            </span>
          </button>
        </div>
      </div>

      {/* Inline register-namespace form. Minimal: namespace + owner. Policy
          fields are deferred to PolicyManagerPage to keep this surface light. */}
      {registerOpen && (
        <div
          className="hud-panel"
          style={{
            padding: "0.75rem",
            marginBottom: "0.75rem",
            borderColor: "rgba(var(--primary) / 0.4)",
          }}
        >
          <div className="form-grid" style={{ gridTemplateColumns: "2fr 1fr 1fr auto" }}>
            <div className="form-field">
              <label className="hud-label" htmlFor="reg-ns">
                Namespace
              </label>
              <input
                id="reg-ns"
                className="hud-input"
                placeholder="user/<actor>/memory or user/<actor>/knowledge/<scope>"
                value={regNs}
                onChange={(e) => setRegNs(e.target.value)}
                style={{ width: "100%" }}
              />
            </div>
            <div className="form-field">
              <label className="hud-label" htmlFor="reg-owner-type">
                Owner Type
              </label>
              <select
                id="reg-owner-type"
                className="hud-input"
                value={regOwnerType}
                onChange={(e) => setRegOwnerType(e.target.value)}
                style={{ width: "100%" }}
              >
                <option value="user">user</option>
                <option value="app">app</option>
                <option value="system">system</option>
              </select>
            </div>
            <div className="form-field">
              <label className="hud-label" htmlFor="reg-owner-id">
                Owner ID
              </label>
              <input
                id="reg-owner-id"
                className="hud-input"
                placeholder="chrispian / hadron / ..."
                value={regOwnerId}
                onChange={(e) => setRegOwnerId(e.target.value)}
                style={{ width: "100%" }}
              />
            </div>
            <div className="form-field" style={{ alignSelf: "end" }}>
              <button
                type="button"
                className="hud-button-primary"
                onClick={handleRegister}
                disabled={regSubmitting || !regNs.trim() || !regOwnerId.trim()}
              >
                <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                  {regSubmitting ? <Spinner size={11} /> : <PenSquare size={11} />} Register
                </span>
              </button>
            </div>
          </div>
          <div style={{ fontSize: "0.65rem", color: "rgb(var(--muted))", marginTop: "0.3rem" }}>
            Registers with default (empty) policy. Use Policy Manager to set tier, retention,
            allowed_ops.
          </div>
        </div>
      )}

      {/* Domain tabs + search */}
      <div
        style={{ display: "flex", gap: "0.5rem", alignItems: "center", marginBottom: "0.75rem" }}
      >
        <div style={{ display: "flex", gap: "0.25rem" }}>
          {(["both", "memory", "knowledge"] as const).map((d) => (
            <button
              key={d}
              type="button"
              onClick={() => setDomain(d)}
              style={{
                padding: "0.3rem 0.7rem",
                background: domain === d ? "rgba(var(--primary) / 0.12)" : "transparent",
                border: `1px solid ${domain === d ? "rgb(var(--primary))" : "rgb(var(--border))"}`,
                color: domain === d ? "rgb(var(--primary))" : "rgb(var(--muted))",
                cursor: "pointer",
                fontSize: "0.75rem",
                fontFamily: "var(--font-mono)",
                textTransform: "uppercase",
                borderRadius: "var(--radius-sm)",
              }}
            >
              {d}
            </button>
          ))}
        </div>
        <Search size={13} style={{ color: "rgb(var(--muted))", marginLeft: "0.5rem" }} />
        <input
          className="hud-input"
          placeholder="Filter namespaces..."
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          style={{ flex: 1 }}
        />
        <span style={{ fontSize: "0.7rem", color: "rgb(var(--muted))" }}>
          {filtered.length} namespace{filtered.length === 1 ? "" : "s"}
        </span>
      </div>

      {nsError && (
        <div
          className="hud-panel"
          style={{ padding: "0.75rem", color: "rgb(var(--danger))", marginBottom: "0.75rem" }}
        >
          {nsError}
        </div>
      )}

      <div className="hud-panel" style={{ overflow: "auto" }}>
        {loadingNs && namespaces.length === 0 && (
          <div style={{ padding: "2rem", textAlign: "center" }}>
            <Spinner size={20} />
          </div>
        )}

        {!loadingNs && groups.length === 0 && (
          <EmptyState
            message="No namespaces match"
            sub={filter ? "Try a different filter" : "Register a namespace to get started"}
          />
        )}

        {groups.map((group) => (
          <div key={group.prefix}>
            <div
              style={{
                padding: "0.4rem 0.75rem",
                background: "rgba(var(--panel2) / 0.4)",
                borderBottom: "1px solid rgb(var(--border))",
                fontFamily: "var(--font-mono)",
                fontSize: "0.7rem",
                color: "rgb(var(--muted))",
                textTransform: "uppercase",
                letterSpacing: "0.05em",
              }}
            >
              {group.prefix}/ &middot; {group.namespaces.length} namespace
              {group.namespaces.length === 1 ? "" : "s"}
            </div>
            {group.namespaces.map((ns) => {
              const isExpanded = expanded.has(ns.namespace);
              const isLoading = loadingKeys.has(ns.namespace);
              const keys = keysByNamespace[ns.namespace];
              const inferred = inferDomain(ns.namespace);
              return (
                <div
                  key={ns.namespace}
                  style={{ borderBottom: "1px solid rgba(var(--border) / 0.5)" }}
                >
                  <button
                    type="button"
                    onClick={() => toggleExpand(ns.namespace)}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: "0.5rem",
                      width: "100%",
                      padding: "0.5rem 0.75rem",
                      background: "none",
                      border: "none",
                      color: "rgb(var(--text))",
                      cursor: "pointer",
                      fontFamily: "var(--font-mono)",
                      fontSize: "0.8rem",
                      textAlign: "left",
                    }}
                  >
                    {isExpanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                    <FolderOpen size={13} style={{ color: "rgb(var(--primary))" }} />
                    <span style={{ flex: 1 }}>{ns.namespace}</span>
                    <span
                      style={{
                        padding: "0.05rem 0.3rem",
                        background: "rgba(var(--panel2) / 0.6)",
                        border: "1px solid rgb(var(--border))",
                        borderRadius: "var(--radius-sm)",
                        fontSize: "0.6rem",
                        color: "rgb(var(--muted))",
                      }}
                    >
                      {inferred}
                    </span>
                    {totalKeysFor(ns.namespace) !== undefined && (
                      <span style={{ fontSize: "0.65rem", color: "rgb(var(--muted))" }}>
                        {totalKeysFor(ns.namespace)} key
                        {totalKeysFor(ns.namespace) === 1 ? "" : "s"}
                      </span>
                    )}
                    {isLoading && <Spinner size={11} />}
                  </button>

                  {isExpanded && keys && (
                    <div style={{ paddingLeft: "2rem", paddingBottom: "0.4rem" }}>
                      {keys.length === 0 && (
                        <div
                          style={{
                            padding: "0.5rem",
                            fontSize: "0.75rem",
                            color: "rgb(var(--muted))",
                          }}
                        >
                          (no records under this namespace)
                        </div>
                      )}
                      {keys.map((item) => {
                        const itemDomain = (item.domain === "knowledge" ? "knowledge" : "memory") as
                          | "memory"
                          | "knowledge";
                        return (
                          <button
                            type="button"
                            key={item.revision_id}
                            onClick={() =>
                              item.memory_key &&
                              onOpenItem?.(itemDomain, item.namespace, item.memory_key)
                            }
                            disabled={!item.memory_key || !onOpenItem}
                            style={{
                              display: "flex",
                              alignItems: "center",
                              gap: "0.5rem",
                              width: "100%",
                              padding: "0.35rem 0.5rem",
                              background: "none",
                              border: "none",
                              color: "rgb(var(--text))",
                              cursor: item.memory_key && onOpenItem ? "pointer" : "default",
                              fontFamily: "var(--font-mono)",
                              fontSize: "0.75rem",
                              textAlign: "left",
                              borderRadius: "var(--radius-sm)",
                            }}
                          >
                            <FileText size={11} style={{ color: "rgb(var(--muted))" }} />
                            <span style={{ flex: 1 }}>{item.memory_key ?? "(no key)"}</span>
                            <span style={{ fontSize: "0.65rem", color: "rgb(var(--muted))" }}>
                              {item.domain}
                            </span>
                            <span style={{ fontSize: "0.65rem", color: "rgb(var(--muted))" }}>
                              conf {item.confidence.toFixed(2)}
                            </span>
                            {item.tags.length > 0 && (
                              <span
                                style={{
                                  fontSize: "0.6rem",
                                  color: "rgb(var(--muted))",
                                  display: "inline-flex",
                                  alignItems: "center",
                                  gap: "0.15rem",
                                }}
                                title={item.tags.join(", ")}
                              >
                                <Tag size={9} /> {item.tags.length}
                              </span>
                            )}
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}
