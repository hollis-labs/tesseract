import { Calculator, FileText, Play, Save, Trash2, Upload } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { estimate, evaluateView } from "../api/client";
import type { EstimateResponse, EvaluationMeta, Record, Selector } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { Spinner } from "../components/ui/Spinner";

interface Props {
  onOpenRecord?: (namespace: string, key: string) => void;
}

interface Preset {
  name: string;
  selector: Selector;
}

const STORAGE_KEY = "conduit:viewbuilder:presets";

function loadPresets(): Preset[] {
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "[]");
  } catch {
    return [];
  }
}

function savePresets(presets: Preset[]) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(presets));
}

export function ViewBuilderPage({ onOpenRecord }: Props) {
  // Selector form state
  const [namespaces, setNamespaces] = useState("");
  const [keys, setKeys] = useState("");
  const [revisionScope, setRevisionScope] = useState<"head" | "all">("head");
  const [order, setOrder] = useState("namespace,key,revision");
  const [limit, setLimit] = useState("50");
  const [tagsAny, setTagsAny] = useState("");

  // Results
  const [results, setResults] = useState<Record[] | null>(null);
  const [evalMeta, setEvalMeta] = useState<EvaluationMeta | null>(null);
  const [estimateResult, setEstimateResult] = useState<EstimateResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [estimating, setEstimating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Presets
  const [presets, setPresetsState] = useState<Preset[]>(loadPresets);
  const [presetName, setPresetName] = useState("");

  const buildSelector = (): Selector => {
    const sel: Selector = {
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
    const o = order.trim();
    if (o)
      sel.order = o
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
    const t = tagsAny.trim();
    if (t)
      sel.tags_any = t
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

  const handleEvaluate = async () => {
    setLoading(true);
    setError(null);
    setEstimateResult(null);
    try {
      const res = await evaluateView(buildSelector(), true);
      setResults(res.items);
      setEvalMeta(res.evaluation_meta);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  const handleSavePreset = () => {
    const name = presetName.trim();
    if (!name) return;
    const updated = [
      ...presets.filter((p) => p.name !== name),
      { name, selector: buildSelector() },
    ];
    setPresetsState(updated);
    savePresets(updated);
    setPresetName("");
    toast.success(`Preset "${name}" saved`);
  };

  const handleLoadPreset = (preset: Preset) => {
    const s = preset.selector;
    setNamespaces(s.namespaces?.join(", ") ?? "");
    setKeys(s.keys?.join(", ") ?? "");
    setRevisionScope(s.revision_scope ?? "head");
    setOrder(s.order?.join(", ") ?? "namespace,key,revision");
    setLimit(String(s.limit ?? 50));
    setTagsAny(s.tags_any?.join(", ") ?? "");
    toast.success(`Loaded preset "${preset.name}"`);
  };

  const handleDeletePreset = (name: string) => {
    const updated = presets.filter((p) => p.name !== name);
    setPresetsState(updated);
    savePresets(updated);
  };

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">View Builder</h2>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "1fr 280px", gap: "1rem" }}>
        {/* Main form */}
        <div>
          <div className="hud-panel" style={{ padding: "1rem", marginBottom: "0.75rem" }}>
            <div className="form-grid">
              <div className="form-field form-field-full">
                <label className="hud-label">Namespaces (comma-separated globs)</label>
                <input
                  className="hud-input"
                  placeholder="user/memory/*, app/test/*"
                  value={namespaces}
                  onChange={(e) => setNamespaces(e.target.value)}
                  style={{ width: "100%" }}
                />
              </div>

              <div className="form-field form-field-full">
                <label className="hud-label">Keys (comma-separated)</label>
                <input
                  className="hud-input"
                  placeholder="status, config, preferences"
                  value={keys}
                  onChange={(e) => setKeys(e.target.value)}
                  style={{ width: "100%" }}
                />
              </div>

              <div className="form-field">
                <label className="hud-label">Revision Scope</label>
                <select
                  className="hud-input"
                  value={revisionScope}
                  onChange={(e) => setRevisionScope(e.target.value as "head" | "all")}
                  style={{ width: "100%" }}
                >
                  <option value="head">head (latest only)</option>
                  <option value="all">all revisions</option>
                </select>
              </div>

              <div className="form-field">
                <label className="hud-label">Limit</label>
                <input
                  className="hud-input"
                  type="number"
                  min={1}
                  max={1000}
                  value={limit}
                  onChange={(e) => setLimit(e.target.value)}
                  style={{ width: "100%" }}
                />
              </div>

              <div className="form-field">
                <label className="hud-label">Order</label>
                <input
                  className="hud-input"
                  placeholder="namespace,key,revision"
                  value={order}
                  onChange={(e) => setOrder(e.target.value)}
                  style={{ width: "100%" }}
                />
              </div>

              <div className="form-field">
                <label className="hud-label">Tags (any)</label>
                <input
                  className="hud-input"
                  placeholder="tag1, tag2"
                  value={tagsAny}
                  onChange={(e) => setTagsAny(e.target.value)}
                  style={{ width: "100%" }}
                />
              </div>
            </div>

            {/* Actions */}
            <div style={{ display: "flex", gap: "0.5rem", marginTop: "0.75rem" }}>
              <button className="hud-button" onClick={handleEstimate} disabled={estimating}>
                <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                  {estimating ? <Spinner size={12} /> : <Calculator size={13} />}
                  Estimate
                </span>
              </button>
              <button className="hud-button-primary" onClick={handleEvaluate} disabled={loading}>
                <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                  {loading ? <Spinner size={12} /> : <Play size={13} />}
                  Evaluate
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

          {/* Results table */}
          {results && (
            <div className="hud-panel">
              {evalMeta && (
                <div
                  style={{
                    padding: "0.5rem 0.75rem",
                    borderBottom: "1px solid rgb(var(--border))",
                    fontSize: "0.75rem",
                    color: "rgb(var(--muted))",
                    display: "flex",
                    gap: "1rem",
                  }}
                >
                  <span>Matched: {evalMeta.matched_count}</span>
                  <span>Scope: {evalMeta.normalized_scope}</span>
                  {evalMeta.truncated && (
                    <span style={{ color: "rgb(var(--warn))" }}>Truncated</span>
                  )}
                </div>
              )}

              {results.length === 0 && (
                <EmptyState message="No records matched" sub="Adjust your selector and try again" />
              )}

              {results.length > 0 && (
                <table className="hud-table">
                  <thead>
                    <tr>
                      <th>Namespace</th>
                      <th>Key</th>
                      <th>Rev</th>
                      <th>Actor</th>
                      <th>Created</th>
                    </tr>
                  </thead>
                  <tbody>
                    {results.map((r, i) => (
                      <tr
                        key={`${r.namespace}-${r.key}-${r.revision}-${i}`}
                        onClick={() => onOpenRecord?.(r.namespace, r.key)}
                        style={{ cursor: onOpenRecord ? "pointer" : "default" }}
                      >
                        <td style={{ fontSize: "0.8rem" }}>{r.namespace}</td>
                        <td>
                          <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
                            <FileText
                              size={12}
                              style={{ color: "rgb(var(--primary))", flexShrink: 0 }}
                            />
                            {r.key}
                          </span>
                        </td>
                        <td style={{ color: "rgb(var(--muted))" }}>r{r.revision}</td>
                        <td style={{ color: "rgb(var(--muted))" }}>{r.actor}</td>
                        <td style={{ color: "rgb(var(--muted))", fontSize: "0.8rem" }}>
                          {new Date(r.created_at).toLocaleString()}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          )}
        </div>

        {/* Presets sidebar */}
        <div>
          <div className="hud-panel" style={{ padding: "0.75rem" }}>
            <div className="hud-label" style={{ marginBottom: "0.5rem" }}>
              Save Preset
            </div>
            <div style={{ display: "flex", gap: "0.3rem" }}>
              <input
                className="hud-input"
                placeholder="Preset name"
                value={presetName}
                onChange={(e) => setPresetName(e.target.value)}
                style={{ flex: 1, fontSize: "0.8rem" }}
                onKeyDown={(e) => {
                  if (e.key === "Enter") handleSavePreset();
                }}
              />
              <button
                className="hud-button-ghost"
                onClick={handleSavePreset}
                disabled={!presetName.trim()}
                title="Save"
              >
                <Save size={13} />
              </button>
            </div>
          </div>

          {presets.length > 0 && (
            <div className="hud-panel" style={{ padding: "0.75rem", marginTop: "0.5rem" }}>
              <div className="hud-label" style={{ marginBottom: "0.5rem" }}>
                Saved Presets
              </div>
              {presets.map((p) => (
                <div
                  key={p.name}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "0.4rem",
                    padding: "0.3rem 0",
                    borderBottom: "1px solid rgba(var(--border) / 0.5)",
                  }}
                >
                  <button
                    style={{
                      flex: 1,
                      background: "none",
                      border: "none",
                      color: "rgb(var(--text))",
                      cursor: "pointer",
                      fontFamily: "var(--font-mono)",
                      fontSize: "0.8rem",
                      textAlign: "left",
                      padding: "0.2rem 0",
                      display: "flex",
                      alignItems: "center",
                      gap: "0.3rem",
                    }}
                    onClick={() => handleLoadPreset(p)}
                  >
                    <Upload size={11} style={{ color: "rgb(var(--muted))" }} />
                    {p.name}
                  </button>
                  <button
                    style={{
                      background: "none",
                      border: "none",
                      color: "rgb(var(--muted))",
                      cursor: "pointer",
                      padding: "2px",
                    }}
                    onClick={() => handleDeletePreset(p.name)}
                    title="Delete preset"
                  >
                    <Trash2 size={11} />
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
