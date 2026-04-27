import { BookOpen, Send } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { knowledgeWrite } from "../api/client";
import type { KnowledgeRevision, KnowledgeWriteRequest } from "../api/types";
import { Spinner } from "../components/ui/Spinner";

interface Props {
  onOpenItem?: ((domain: "memory" | "knowledge", namespace: string, key: string) => void) | undefined;
}

export function KnowledgeWritePage({ onOpenItem }: Props) {
  const [namespace, setNamespace] = useState("");
  const [key, setKey] = useState("");
  const [kind, setKind] = useState("doc");
  const [source, setSource] = useState("filesystem");
  const [pointerScheme, setPointerScheme] = useState("file");
  const [pointerLocator, setPointerLocator] = useState("");
  const [summary, setSummary] = useState("");
  const [body, setBody] = useState("");
  const [authorAgentId, setAuthorAgentId] = useState("");
  const [authorVersion, setAuthorVersion] = useState("");
  const [sessionId, setSessionId] = useState("");
  const [tagsField, setTagsField] = useState("");
  const [confidence, setConfidence] = useState("0.9");
  const [supersedes, setSupersedes] = useState("");
  const [ttlSeconds, setTtlSeconds] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<KnowledgeRevision | null>(null);

  const canSubmit =
    namespace.trim() &&
    kind.trim() &&
    source.trim() &&
    pointerScheme.trim() &&
    pointerLocator.trim() &&
    summary.trim() &&
    authorAgentId.trim() &&
    !submitting;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    setResult(null);
    try {
      const req: KnowledgeWriteRequest = {
        namespace: namespace.trim(),
        kind: kind.trim(),
        source: source.trim(),
        pointer: {
          scheme: pointerScheme.trim(),
          locator: pointerLocator.trim(),
        },
        summary: summary.trim(),
        author: { agent_id: authorAgentId.trim() },
      };
      if (key.trim()) req.key = key.trim();
      if (body.trim()) req.body = body.trim();
      if (authorVersion.trim()) req.author.agent_version = authorVersion.trim();
      if (sessionId.trim()) req.session_id = sessionId.trim();
      if (supersedes.trim()) req.supersedes = supersedes.trim();
      if (tagsField.trim()) {
        req.tags = tagsField
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean);
      }
      const parsedConfidence = parseFloat(confidence);
      if (Number.isFinite(parsedConfidence)) req.confidence = parsedConfidence;
      const parsedTTL = parseInt(ttlSeconds, 10);
      if (Number.isFinite(parsedTTL) && parsedTTL > 0) req.ttl_seconds = parsedTTL;

      const res = await knowledgeWrite(req);
      setResult(res);
      toast.success(`Wrote knowledge revision ${res.revision_id}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Knowledge write failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Knowledge Write</h2>
      </div>

      <div className="hud-panel" style={{ padding: "1rem", maxWidth: 860 }}>
        {error && (
          <div
            style={{
              padding: "0.5rem 0.75rem",
              marginBottom: "0.75rem",
              background: "rgba(var(--danger) / 0.1)",
              color: "rgb(var(--danger))",
              fontSize: "0.85rem",
              borderRadius: "var(--radius-sm)",
            }}
          >
            {error}
          </div>
        )}

        {result && (
          <div
            className="hud-panel"
            style={{
              padding: "0.75rem",
              marginBottom: "0.75rem",
              borderColor: "rgba(var(--ok) / 0.4)",
            }}
          >
            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
                gap: "0.5rem",
                marginBottom: "0.35rem",
              }}
            >
              <div style={{ fontSize: "0.85rem", color: "rgb(var(--ok))" }}>
                Wrote revision <span style={{ fontFamily: "var(--font-mono)" }}>{result.revision_id}</span>
              </div>
              {onOpenItem && result.memory_key && (
                <button
                  type="button"
                  className="hud-button-ghost"
                  onClick={() => onOpenItem("knowledge", result.namespace, result.memory_key!)}
                  style={{ fontSize: "0.7rem", padding: "0.2rem 0.4rem" }}
                >
                  Open detail
                </button>
              )}
            </div>
            <div style={{ fontSize: "0.75rem", color: "rgb(var(--muted))", fontFamily: "var(--font-mono)" }}>
              {result.namespace} / {result.memory_key ?? "(no key)"} · kind {result.facets?.kind ?? kind} · source {result.facets?.source ?? source}
            </div>
          </div>
        )}

        <div className="form-grid" style={{ gridTemplateColumns: "2fr 1fr 1fr" }}>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-namespace">
              Namespace <span style={{ color: "rgb(var(--danger))" }}>*</span>
            </label>
            <input
              id="kw-namespace"
              className="hud-input"
              placeholder="user/<actor>/knowledge/<scope>"
              value={namespace}
              onChange={(e) => setNamespace(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-key">
              Key
            </label>
            <input
              id="kw-key"
              className="hud-input"
              placeholder="design.doc"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-supersedes">
              Supersedes
            </label>
            <input
              id="kw-supersedes"
              className="hud-input"
              placeholder="revision id"
              value={supersedes}
              onChange={(e) => setSupersedes(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
        </div>

        <div className="form-grid" style={{ gridTemplateColumns: "1fr 1fr 1fr 2fr", marginTop: "0.5rem" }}>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-kind">
              Kind <span style={{ color: "rgb(var(--danger))" }}>*</span>
            </label>
            <input id="kw-kind" className="hud-input" value={kind} onChange={(e) => setKind(e.target.value)} style={{ width: "100%" }} />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-source">
              Source <span style={{ color: "rgb(var(--danger))" }}>*</span>
            </label>
            <input id="kw-source" className="hud-input" value={source} onChange={(e) => setSource(e.target.value)} style={{ width: "100%" }} />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-pointer-scheme">
              Pointer Scheme <span style={{ color: "rgb(var(--danger))" }}>*</span>
            </label>
            <input id="kw-pointer-scheme" className="hud-input" value={pointerScheme} onChange={(e) => setPointerScheme(e.target.value)} style={{ width: "100%" }} />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-pointer-locator">
              Pointer Locator <span style={{ color: "rgb(var(--danger))" }}>*</span>
            </label>
            <input
              id="kw-pointer-locator"
              className="hud-input"
              placeholder="/docs/spec.md or https://…"
              value={pointerLocator}
              onChange={(e) => setPointerLocator(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
        </div>

        <div className="form-grid" style={{ gridTemplateColumns: "1fr 1fr 1fr 1fr", marginTop: "0.5rem" }}>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-author">
              Author agent_id <span style={{ color: "rgb(var(--danger))" }}>*</span>
            </label>
            <input id="kw-author" className="hud-input" value={authorAgentId} onChange={(e) => setAuthorAgentId(e.target.value)} style={{ width: "100%" }} />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-author-version">
              Author version
            </label>
            <input id="kw-author-version" className="hud-input" value={authorVersion} onChange={(e) => setAuthorVersion(e.target.value)} style={{ width: "100%" }} />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-session-id">
              Session ID
            </label>
            <input id="kw-session-id" className="hud-input" value={sessionId} onChange={(e) => setSessionId(e.target.value)} style={{ width: "100%" }} />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-confidence">
              Confidence
            </label>
            <input id="kw-confidence" className="hud-input" type="number" min="0" max="1" step="0.05" value={confidence} onChange={(e) => setConfidence(e.target.value)} style={{ width: "100%" }} />
          </div>
        </div>

        <div className="form-grid" style={{ gridTemplateColumns: "2fr 1fr", marginTop: "0.5rem" }}>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-tags">
              Tags
            </label>
            <input
              id="kw-tags"
              className="hud-input"
              placeholder="doc, architecture, source:repo"
              value={tagsField}
              onChange={(e) => setTagsField(e.target.value)}
              style={{ width: "100%" }}
            />
          </div>
          <div className="form-field">
            <label className="hud-label" htmlFor="kw-ttl">
              TTL Seconds
            </label>
            <input id="kw-ttl" className="hud-input" type="number" min="0" value={ttlSeconds} onChange={(e) => setTtlSeconds(e.target.value)} style={{ width: "100%" }} />
          </div>
        </div>

        <div className="form-field" style={{ marginTop: "0.5rem" }}>
          <label className="hud-label" htmlFor="kw-summary">
            Summary <span style={{ color: "rgb(var(--danger))" }}>*</span>
          </label>
          <textarea
            id="kw-summary"
            className="hud-textarea"
            rows={2}
            placeholder="Short operator-facing summary of the knowledge item."
            value={summary}
            onChange={(e) => setSummary(e.target.value)}
            style={{ width: "100%" }}
          />
        </div>

        <div className="form-field" style={{ marginTop: "0.5rem" }}>
          <label className="hud-label" htmlFor="kw-body">
            Body
          </label>
          <textarea
            id="kw-body"
            className="hud-textarea"
            rows={7}
            placeholder="Long-form body content..."
            value={body}
            onChange={(e) => setBody(e.target.value)}
            style={{ width: "100%", fontFamily: "var(--font-mono)" }}
          />
        </div>

        <div style={{ marginTop: "0.75rem" }}>
          <button type="button" className="hud-button-primary" onClick={handleSubmit} disabled={!canSubmit}>
            <span style={{ display: "flex", alignItems: "center", gap: "0.3rem" }}>
              {submitting ? <Spinner size={13} /> : <BookOpen size={13} />}
              <Send size={13} style={{ marginLeft: "0.15rem" }} />
              Write Knowledge
            </span>
          </button>
        </div>
      </div>
    </div>
  );
}
