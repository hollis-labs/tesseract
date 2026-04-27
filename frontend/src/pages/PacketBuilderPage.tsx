import { Calculator, FileText, Package, Play } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { buildPacket, estimate } from "../api/client";
import type { EstimateResponse, PacketManifest, Record } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { JsonViewer } from "../components/ui/JsonViewer";
import { Spinner } from "../components/ui/Spinner";

interface Props {
  onOpenRecord?: (namespace: string, key: string) => void;
}

export function PacketBuilderPage({ onOpenRecord }: Props) {
  // Selector fields
  const [namespaces, setNamespaces] = useState("");
  const [keys, setKeys] = useState("");
  const [revisionScope, setRevisionScope] = useState<"head" | "all">("head");
  const [limit, setLimit] = useState("50");

  // Assembly options
  const [includePins, setIncludePins] = useState(true);
  const [maxItems, setMaxItems] = useState("50");
  const [maxBytes, setMaxBytes] = useState("");
  const [maxTokens, setMaxTokens] = useState("8000");
  const [payloadMode, setPayloadMode] = useState<"full" | "head_only">("full");

  // Results
  const [items, setItems] = useState<Record[] | null>(null);
  const [manifest, setManifest] = useState<PacketManifest | null>(null);
  const [estimateResult, setEstimateResult] = useState<EstimateResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [estimating, setEstimating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [expandedItem, setExpandedItem] = useState<number | null>(null);

  const buildSelector = (): import("../api/types").Selector => {
    const sel: import("../api/types").Selector = {
      revision_scope: revisionScope,
      limit: parseInt(limit) || 50,
    };
    const ns = namespaces.trim();
    if (ns)
      sel.namespaces = ns
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
    const k = keys.trim();
    if (k)
      sel.keys = k
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
    return sel;
  };

  const handleEstimate = async () => {
    setEstimating(true);
    setError(null);
    try {
      const res = await estimate(buildSelector());
      setEstimateResult(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setEstimating(false);
    }
  };

  const handleBuild = async () => {
    setLoading(true);
    setError(null);
    setEstimateResult(null);
    try {
      const req: Parameters<typeof buildPacket>[0] = {
        selector: buildSelector(),
        include_pins: includePins,
        payload_mode: payloadMode,
      };
      const mi = parseInt(maxItems);
      if (Number.isFinite(mi) && mi > 0) req.max_items = mi;
      if (maxBytes.trim()) {
        const mb = parseInt(maxBytes);
        if (Number.isFinite(mb) && mb > 0) req.max_bytes = mb;
      }
      const mt = parseInt(maxTokens);
      if (Number.isFinite(mt) && mt > 0) req.max_tokens_estimate = mt;
      const res = await buildPacket(req);
      setItems(res.items);
      setManifest(res.manifest);
      toast.success(`Packet built: ${res.items?.length ?? 0} items`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Build failed: ${msg}`);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Packet Builder</h2>
      </div>

      <div
        className="hud-panel"
        style={{ padding: "0.9rem 1rem", marginBottom: "0.75rem", borderColor: "rgba(var(--primary) / 0.35)" }}
      >
        <div style={{ fontSize: "0.9rem", marginBottom: "0.3rem" }}>Turn a selector into a bounded packet.</div>
        <div style={{ fontSize: "0.78rem", color: "rgb(var(--muted))", lineHeight: 1.5 }}>
          Use this after you know what records you want. Packet Builder applies item, byte, and
          token budgets so the result is practical to hand to another system.
        </div>
      </div>

      <div className="hud-panel" style={{ padding: "1rem", marginBottom: "0.75rem" }}>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: "0.75rem",
            marginBottom: "0.9rem",
            padding: "0.75rem",
            background: "rgba(var(--panel2) / 0.7)",
            border: "1px solid rgba(var(--border) / 0.8)",
            borderRadius: "var(--radius-sm)",
          }}
        >
          <div>
            <div className="hud-label" style={{ marginBottom: "0.2rem" }}>What It Does</div>
            <div style={{ fontSize: "0.78rem", color: "rgb(var(--muted))", lineHeight: 1.45 }}>
              Starts from a selector, then trims and assembles the result to fit your budget.
            </div>
          </div>
          <div>
            <div className="hud-label" style={{ marginBottom: "0.2rem" }}>How It Relates To View Builder</div>
            <div style={{ fontSize: "0.78rem", color: "rgb(var(--muted))", lineHeight: 1.45 }}>
              View Builder is for inspecting the set. Packet Builder is for producing the final payload.
            </div>
          </div>
        </div>

        {/* Selector */}
        <div className="hud-label" style={{ color: "rgb(var(--primary))", marginBottom: "0.5rem" }}>
          Selector
        </div>
        <div className="form-grid">
          <div className="form-field form-field-full">
            <label className="hud-label">Namespaces</label>
            <input
              className="hud-input"
              placeholder="user/memory/*, app/test/*"
              value={namespaces}
              onChange={(e) => setNamespaces(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label">Keys</label>
            <input
              className="hud-input"
              placeholder="status, config"
              value={keys}
              onChange={(e) => setKeys(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label">Scope</label>
            <select
              className="hud-input"
              value={revisionScope}
              onChange={(e) => setRevisionScope(e.target.value as "head" | "all")}
              style={{ width: "100%" }}
            >
              <option value="head">head</option>
              <option value="all">all</option>
            </select>
          </div>
        </div>

        {/* Assembly options */}
        <div
          className="hud-label"
          style={{ color: "rgb(var(--primary))", marginBottom: "0.5rem", marginTop: "0.75rem" }}
        >
          Budget & Assembly
        </div>
        <div className="form-grid">
          <div className="form-field">
            <label className="hud-label">Max Items</label>
            <input
              className="hud-input"
              type="number"
              value={maxItems}
              onChange={(e) => setMaxItems(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label">Max Tokens</label>
            <input
              className="hud-input"
              type="number"
              value={maxTokens}
              onChange={(e) => setMaxTokens(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label">
              Max Bytes <span style={{ color: "rgb(var(--muted))" }}>(optional)</span>
            </label>
            <input
              className="hud-input"
              type="number"
              placeholder="No limit"
              value={maxBytes}
              onChange={(e) => setMaxBytes(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label">Payload Mode</label>
            <select
              className="hud-input"
              value={payloadMode}
              onChange={(e) => setPayloadMode(e.target.value as "full" | "head_only")}
              style={{ width: "100%" }}
            >
              <option value="full">full</option>
              <option value="head_only">head_only</option>
            </select>
          </div>
        </div>

        <div style={{ marginTop: "0.5rem" }}>
          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.4rem",
              fontSize: "0.8rem",
              color: "rgb(var(--text))",
              cursor: "pointer",
            }}
          >
            <input
              type="checkbox"
              checked={includePins}
              onChange={(e) => setIncludePins(e.target.checked)}
            />
            Include pins
          </label>
        </div>

        <div style={{ fontSize: "0.74rem", color: "rgb(var(--muted))", marginTop: "0.65rem", lineHeight: 1.5 }}>
          `Include pins` brings pinned records into the packet. `Payload mode` controls whether the
          packet contains full payload bodies or only the record heads. `Truncated` means the final
          result was intentionally cut to stay within budget.
        </div>

        {/* Actions */}
        <div style={{ display: "flex", gap: "0.5rem", marginTop: "0.75rem" }}>
          <button className="hud-button" onClick={handleEstimate} disabled={estimating}>
            <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
              {estimating ? <Spinner size={12} /> : <Calculator size={13} />} Estimate
            </span>
          </button>
          <button className="hud-button-primary" onClick={handleBuild} disabled={loading}>
            <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
              {loading ? <Spinner size={12} /> : <Package size={13} />} Build Packet
            </span>
          </button>
        </div>
      </div>

      {/* Estimate result */}
      {estimateResult && (
        <div className="stats-grid" style={{ marginBottom: "0.75rem" }}>
          <div className="stat-card">
            <div className="stat-label">Records</div>
            <div className="stat-value">{estimateResult.record_count}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Bytes</div>
            <div className="stat-value">{formatBytes(estimateResult.total_bytes)}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Tokens (est)</div>
            <div className="stat-value">{estimateResult.token_estimate.toLocaleString()}</div>
          </div>
        </div>
      )}

      {error && (
        <div
          className="hud-panel"
          style={{ padding: "0.75rem", color: "rgb(var(--danger))", marginBottom: "0.75rem" }}
        >
          {error}
        </div>
      )}

      {/* Manifest */}
      {manifest && (
        <div className="stats-grid" style={{ marginBottom: "0.75rem" }}>
          <div className="stat-card">
            <div className="stat-label">Items</div>
            <div className="stat-value">{manifest.items_total}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Pins</div>
            <div className="stat-value">{manifest.pins_included}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Bytes</div>
            <div className="stat-value">{formatBytes(manifest.bytes)}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Tokens</div>
            <div className="stat-value">{manifest.tokens_estimate.toLocaleString()}</div>
          </div>
          {manifest.truncated && (
            <div className="stat-card" style={{ borderColor: "rgba(var(--warn) / 0.4)" }}>
              <div className="stat-label" style={{ color: "rgb(var(--warn))" }}>
                Truncated
              </div>
              <div className="stat-value" style={{ color: "rgb(var(--warn))" }}>
                Yes
              </div>
            </div>
          )}
        </div>
      )}

      {/* Items list */}
      {items && (
        <div className="hud-panel">
          {items.length === 0 && (
            <EmptyState message="Packet is empty" sub="No records matched the selector" />
          )}
          {items.length > 0 &&
            items.map((item, i) => (
              <div
                key={`${item.namespace}-${item.key}-${item.revision}-${i}`}
                style={{
                  borderBottom:
                    i < items.length - 1 ? "1px solid rgba(var(--border) / 0.5)" : "none",
                }}
              >
                <div
                  onClick={() => setExpandedItem(expandedItem === i ? null : i)}
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
                  <FileText size={13} style={{ color: "rgb(var(--primary))", flexShrink: 0 }} />
                  <span style={{ fontSize: "0.8rem", color: "rgb(var(--muted))" }}>
                    {item.namespace}
                  </span>
                  <span style={{ fontSize: "0.85rem" }}>{item.key}</span>
                  <span
                    style={{ fontSize: "0.75rem", color: "rgb(var(--muted))", marginLeft: "auto" }}
                  >
                    r{item.revision}
                  </span>
                  {onOpenRecord && (
                    <button
                      className="hud-button-ghost"
                      onClick={(e) => {
                        e.stopPropagation();
                        onOpenRecord(item.namespace, item.key);
                      }}
                      style={{ padding: "0.1rem 0.3rem", fontSize: "0.6rem" }}
                    >
                      Open
                    </button>
                  )}
                </div>
                {expandedItem === i && item.payload != null && (
                  <div style={{ padding: "0 0.75rem 0.5rem" }}>
                    <JsonViewer data={item.payload} maxHeight="200px" />
                  </div>
                )}
              </div>
            ))}
        </div>
      )}
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
