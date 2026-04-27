import { ArrowRight, PenSquare, Send, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { memoryDeprecate, memoryPromote, memoryWrite } from "../api/client";
import type {
  MemoryDeprecateResponse,
  MemoryRevision,
  MemoryStatus,
  MemoryWriteRequest,
} from "../api/types";
import { Spinner } from "../components/ui/Spinner";
import { StatusBadge } from "../components/ui/StatusBadge";

type Tab = "write" | "promote" | "deprecate";

const STATUS_OPTIONS: MemoryStatus[] = ["draft", "reviewed", "canonical", "deprecated"];

interface Props {
  onOpenItem?: ((domain: "memory" | "knowledge", namespace: string, key: string) => void) | undefined;
  onOpenReview?: (() => void) | undefined;
}

export function MemoryWritePage({ onOpenItem, onOpenReview }: Props) {
  const [tab, setTab] = useState<Tab>("write");

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Memory Write</h2>
        {onOpenReview && (
          <div className="page-actions">
            <button type="button" className="hud-button-ghost" onClick={onOpenReview}>
              Review Queue
            </button>
          </div>
        )}
      </div>

      <div style={{ display: "flex", gap: "0.25rem", marginBottom: "0.75rem", borderBottom: "1px solid rgb(var(--border))" }}>
        {(["write", "promote", "deprecate"] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
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
            {t}
          </button>
        ))}
      </div>

      {tab === "write" && <WriteForm onOpenItem={onOpenItem} />}
      {tab === "promote" && <PromoteForm onOpenItem={onOpenItem} />}
      {tab === "deprecate" && <DeprecateForm />}
    </div>
  );
}

function WriteForm({
  onOpenItem,
}: {
  onOpenItem?: ((domain: "memory" | "knowledge", namespace: string, key: string) => void) | undefined;
}) {
  const [namespace, setNamespace] = useState("");
  const [memoryKey, setMemoryKey] = useState("");
  const [supersedes, setSupersedes] = useState("");
  const [status, setStatus] = useState<MemoryStatus>("canonical");
  const [authorAgentId, setAuthorAgentId] = useState("");
  const [authorVersion, setAuthorVersion] = useState("");
  const [confidence, setConfidence] = useState("0.9");
  const [tagsField, setTagsField] = useState("");
  const [summary, setSummary] = useState("");
  const [body, setBody] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<MemoryRevision | null>(null);

  const canSubmit =
    namespace.trim() && authorAgentId.trim() && summary.trim() && !submitting;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    setResult(null);
    try {
      const req: MemoryWriteRequest = {
        namespace: namespace.trim(),
        author: { agent_id: authorAgentId.trim() },
        payload: { summary: summary.trim() },
      };
      if (memoryKey.trim()) req.memory_key = memoryKey.trim();
      if (supersedes.trim()) req.supersedes = supersedes.trim();
      if (status) req.status = status;
      if (authorVersion.trim()) req.author.agent_version = authorVersion.trim();
      const conf = parseFloat(confidence);
      if (Number.isFinite(conf)) req.confidence = conf;
      if (tagsField.trim()) {
        req.tags = tagsField
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean);
      }
      if (body.trim()) req.payload.body = body.trim();
      const res = await memoryWrite(req);
      setResult(res);
      toast.success(`Wrote memory revision ${res.revision_id}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Write failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="hud-panel" style={{ padding: "1rem", maxWidth: 800 }}>
      {error && (
        <div style={{ padding: "0.5rem 0.75rem", marginBottom: "0.75rem", background: "rgba(var(--danger) / 0.1)", color: "rgb(var(--danger))", fontSize: "0.85rem", borderRadius: "var(--radius-sm)" }}>
          {error}
        </div>
      )}

      {result && (
        <div className="hud-panel" style={{ padding: "0.75rem", marginBottom: "0.75rem", borderColor: "rgba(var(--ok) / 0.4)" }}>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "0.5rem", marginBottom: "0.4rem" }}>
            <div style={{ fontSize: "0.85rem", color: "rgb(var(--ok))" }}>
              Wrote revision <span style={{ fontFamily: "var(--font-mono)" }}>{result.revision_id}</span>
            </div>
            {onOpenItem && result.memory_key && (
              <button
                type="button"
                className="hud-button-ghost"
                onClick={() => onOpenItem("memory", result.namespace, result.memory_key!)}
                style={{ fontSize: "0.7rem", padding: "0.2rem 0.4rem" }}
              >
                Open detail
              </button>
            )}
          </div>
          <div style={{ fontSize: "0.75rem", color: "rgb(var(--muted))", fontFamily: "var(--font-mono)" }}>
            {result.namespace} / {result.memory_key ?? "(no key)"} · status {result.status} · conf {result.confidence.toFixed(2)}
          </div>
        </div>
      )}

      <div className="form-grid" style={{ gridTemplateColumns: "2fr 1fr 1fr" }}>
        <div className="form-field">
          <label className="hud-label" htmlFor="mw-namespace">
            Namespace <span style={{ color: "rgb(var(--danger))" }}>*</span>
          </label>
          <input id="mw-namespace" className="hud-input" placeholder="user/<actor>/memory" value={namespace} onChange={(e) => setNamespace(e.target.value)} style={{ width: "100%" }} />
        </div>
        <div className="form-field">
          <label className="hud-label" htmlFor="mw-key">
            Memory key <span style={{ color: "rgb(var(--muted))" }}>(optional)</span>
          </label>
          <input id="mw-key" className="hud-input" placeholder="decisions_…" value={memoryKey} onChange={(e) => setMemoryKey(e.target.value)} style={{ width: "100%" }} />
        </div>
        <div className="form-field">
          <label className="hud-label" htmlFor="mw-status">
            Status
          </label>
          <select id="mw-status" className="hud-input" value={status} onChange={(e) => setStatus(e.target.value as MemoryStatus)} style={{ width: "100%" }}>
            {STATUS_OPTIONS.map((s) => (
              <option key={s} value={s}>{s}</option>
            ))}
          </select>
        </div>
      </div>

      <div className="form-grid" style={{ gridTemplateColumns: "1fr 1fr 1fr 1fr", marginTop: "0.5rem" }}>
        <div className="form-field">
          <label className="hud-label" htmlFor="mw-author">
            Author agent_id <span style={{ color: "rgb(var(--danger))" }}>*</span>
          </label>
          <input id="mw-author" className="hud-input" placeholder="steward / nanite / …" value={authorAgentId} onChange={(e) => setAuthorAgentId(e.target.value)} style={{ width: "100%" }} />
        </div>
        <div className="form-field">
          <label className="hud-label" htmlFor="mw-author-ver">
            Author version
          </label>
          <input id="mw-author-ver" className="hud-input" placeholder="0.1.0" value={authorVersion} onChange={(e) => setAuthorVersion(e.target.value)} style={{ width: "100%" }} />
        </div>
        <div className="form-field">
          <label className="hud-label" htmlFor="mw-confidence">
            Confidence
          </label>
          <input id="mw-confidence" className="hud-input" type="number" step="0.05" min="0" max="1" value={confidence} onChange={(e) => setConfidence(e.target.value)} style={{ width: "100%" }} />
        </div>
        <div className="form-field">
          <label className="hud-label" htmlFor="mw-supersedes">
            Supersedes <span style={{ color: "rgb(var(--muted))" }}>(revision id)</span>
          </label>
          <input id="mw-supersedes" className="hud-input" placeholder="01HX…" value={supersedes} onChange={(e) => setSupersedes(e.target.value)} style={{ width: "100%" }} />
        </div>
      </div>

      <div className="form-field" style={{ marginTop: "0.5rem" }}>
        <label className="hud-label" htmlFor="mw-tags">
          Tags <span style={{ color: "rgb(var(--muted))" }}>(comma-separated)</span>
        </label>
        <input id="mw-tags" className="hud-input" placeholder="decision, scope:agent-ops.steward.main" value={tagsField} onChange={(e) => setTagsField(e.target.value)} style={{ width: "100%" }} />
      </div>

      <div className="form-field" style={{ marginTop: "0.5rem" }}>
        <label className="hud-label" htmlFor="mw-summary">
          Summary <span style={{ color: "rgb(var(--danger))" }}>*</span>
        </label>
        <textarea id="mw-summary" className="hud-textarea" placeholder="One-sentence summary of the memory." value={summary} onChange={(e) => setSummary(e.target.value)} rows={2} style={{ width: "100%" }} />
      </div>

      <div className="form-field" style={{ marginTop: "0.5rem" }}>
        <label className="hud-label" htmlFor="mw-body">
          Body <span style={{ color: "rgb(var(--muted))" }}>(optional, supports markdown)</span>
        </label>
        <textarea id="mw-body" className="hud-textarea" placeholder="Long-form body content..." value={body} onChange={(e) => setBody(e.target.value)} rows={6} style={{ width: "100%", fontFamily: "var(--font-mono)" }} />
      </div>

      <div style={{ marginTop: "0.75rem" }}>
        <button type="button" className="hud-button-primary" onClick={handleSubmit} disabled={!canSubmit}>
          <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
            {submitting ? <Spinner size={13} /> : <Send size={13} />} Write Memory
          </span>
        </button>
      </div>
    </div>
  );
}

function PromoteForm({
  onOpenItem,
}: {
  onOpenItem?: ((domain: "memory" | "knowledge", namespace: string, key: string) => void) | undefined;
}) {
  const [srcNamespace, setSrcNamespace] = useState("");
  const [srcMemoryId, setSrcMemoryId] = useState("");
  const [tgtNamespace, setTgtNamespace] = useState("");
  const [actorAgentId, setActorAgentId] = useState("");
  const [actorVersion, setActorVersion] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<MemoryRevision | null>(null);

  const canSubmit =
    srcNamespace.trim() && srcMemoryId.trim() && tgtNamespace.trim() && actorAgentId.trim() && !submitting;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    setResult(null);
    try {
      const req: Parameters<typeof memoryPromote>[0] = {
        source_namespace: srcNamespace.trim(),
        source_memory_id: srcMemoryId.trim(),
        target_namespace: tgtNamespace.trim(),
        actor_agent_id: actorAgentId.trim(),
      };
      if (actorVersion.trim()) req.actor_version = actorVersion.trim();
      const res = await memoryPromote(req);
      setResult(res);
      toast.success("Memory promoted");
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Promote failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="hud-panel" style={{ padding: "1rem", maxWidth: 800 }}>
      {error && (
        <div style={{ padding: "0.5rem 0.75rem", marginBottom: "0.75rem", background: "rgba(var(--danger) / 0.1)", color: "rgb(var(--danger))", fontSize: "0.85rem", borderRadius: "var(--radius-sm)" }}>
          {error}
        </div>
      )}

      {result && (
        <div className="hud-panel" style={{ padding: "0.75rem", marginBottom: "0.75rem", borderColor: "rgba(var(--ok) / 0.4)" }}>
          <div style={{ fontSize: "0.85rem", color: "rgb(var(--ok))", marginBottom: "0.4rem" }}>
            Promoted to <span style={{ fontFamily: "var(--font-mono)" }}>{result.namespace}</span> as revision <span style={{ fontFamily: "var(--font-mono)" }}>{result.revision_id}</span>
          </div>
          {onOpenItem && result.memory_key && (
            <button
              type="button"
              className="hud-button-ghost"
              onClick={() => onOpenItem("memory", result.namespace, result.memory_key!)}
              style={{ fontSize: "0.7rem", padding: "0.2rem 0.4rem" }}
            >
              Open detail
            </button>
          )}
        </div>
      )}

      <div style={{ fontSize: "0.75rem", color: "rgb(var(--muted))", marginBottom: "0.75rem" }}>
        Promotes a memory revision from a source namespace into a target namespace. Both namespaces must already be registered.
      </div>

      <div className="hud-label" style={{ color: "rgb(var(--primary))", marginBottom: "0.5rem" }}>Source</div>
      <div className="form-grid" style={{ gridTemplateColumns: "2fr 2fr" }}>
        <div className="form-field">
          <label className="hud-label" htmlFor="mp-src-ns">
            Namespace <span style={{ color: "rgb(var(--danger))" }}>*</span>
          </label>
          <input id="mp-src-ns" className="hud-input" placeholder="app/<id>/memory" value={srcNamespace} onChange={(e) => setSrcNamespace(e.target.value)} style={{ width: "100%" }} />
        </div>
        <div className="form-field">
          <label className="hud-label" htmlFor="mp-src-id">
            Memory ID <span style={{ color: "rgb(var(--danger))" }}>*</span>
          </label>
          <input id="mp-src-id" className="hud-input" placeholder="01KP… (memory_id, not revision_id)" value={srcMemoryId} onChange={(e) => setSrcMemoryId(e.target.value)} style={{ width: "100%" }} />
        </div>
      </div>

      <div style={{ textAlign: "center", padding: "0.4rem 0", color: "rgb(var(--muted))" }}>
        <ArrowRight size={20} />
      </div>

      <div className="hud-label" style={{ color: "rgb(var(--primary))", marginBottom: "0.5rem" }}>Target</div>
      <div className="form-grid" style={{ gridTemplateColumns: "2fr 1fr 1fr" }}>
        <div className="form-field">
          <label className="hud-label" htmlFor="mp-tgt-ns">
            Namespace <span style={{ color: "rgb(var(--danger))" }}>*</span>
          </label>
          <input id="mp-tgt-ns" className="hud-input" placeholder="user/<actor>/memory" value={tgtNamespace} onChange={(e) => setTgtNamespace(e.target.value)} style={{ width: "100%" }} />
        </div>
        <div className="form-field">
          <label className="hud-label" htmlFor="mp-actor">
            Actor agent_id <span style={{ color: "rgb(var(--danger))" }}>*</span>
          </label>
          <input id="mp-actor" className="hud-input" placeholder="steward" value={actorAgentId} onChange={(e) => setActorAgentId(e.target.value)} style={{ width: "100%" }} />
        </div>
        <div className="form-field">
          <label className="hud-label" htmlFor="mp-actor-ver">
            Actor version
          </label>
          <input id="mp-actor-ver" className="hud-input" placeholder="0.1.0" value={actorVersion} onChange={(e) => setActorVersion(e.target.value)} style={{ width: "100%" }} />
        </div>
      </div>

      <div style={{ marginTop: "0.75rem" }}>
        <button type="button" className="hud-button-primary" onClick={handleSubmit} disabled={!canSubmit}>
          <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
            {submitting ? <Spinner size={13} /> : <PenSquare size={13} />} Promote
          </span>
        </button>
      </div>
    </div>
  );
}

function DeprecateForm() {
  const [revisionId, setRevisionId] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<MemoryDeprecateResponse | null>(null);

  const canSubmit = revisionId.trim() && !submitting;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    setResult(null);
    try {
      const res = await memoryDeprecate({ revision_id: revisionId.trim() });
      setResult(res);
      toast.success(`Deprecated ${res.revision_id}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Deprecate failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="hud-panel" style={{ padding: "1rem", maxWidth: 600 }}>
      {error && (
        <div style={{ padding: "0.5rem 0.75rem", marginBottom: "0.75rem", background: "rgba(var(--danger) / 0.1)", color: "rgb(var(--danger))", fontSize: "0.85rem", borderRadius: "var(--radius-sm)" }}>
          {error}
        </div>
      )}

      {result && (
        <div className="hud-panel" style={{ padding: "0.75rem", marginBottom: "0.75rem", borderColor: "rgba(var(--warn) / 0.4)" }}>
          <div style={{ fontSize: "0.85rem", display: "flex", alignItems: "center", gap: "0.5rem" }}>
            <StatusBadge status={result.status} />
            <span style={{ fontFamily: "var(--font-mono)" }}>{result.revision_id}</span>
          </div>
        </div>
      )}

      <div style={{ fontSize: "0.75rem", color: "rgb(var(--muted))", marginBottom: "0.75rem" }}>
        Marks a memory revision as deprecated. The revision is not deleted — it remains in history but no longer surfaces as the head. Audit log records the deprecation.
      </div>

      <div className="form-field">
        <label className="hud-label" htmlFor="md-revision">
          Revision ID <span style={{ color: "rgb(var(--danger))" }}>*</span>
        </label>
        <input id="md-revision" className="hud-input" placeholder="01HX… (revision_id, not memory_id)" value={revisionId} onChange={(e) => setRevisionId(e.target.value)} style={{ width: "100%", fontFamily: "var(--font-mono)" }} />
      </div>

      <div style={{ marginTop: "0.75rem" }}>
        <button type="button" className="hud-button-primary" onClick={handleSubmit} disabled={!canSubmit}>
          <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
            {submitting ? <Spinner size={13} /> : <Trash2 size={13} />} Deprecate
          </span>
        </button>
      </div>
    </div>
  );
}
