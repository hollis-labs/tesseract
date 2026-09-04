import { Button, Callout, Card, CardContent, Input, Label, Textarea } from "@hollis-labs/sysop-ui";
import { BookOpen } from "lucide-react";
import { type ReactNode, useState } from "react";
import { toast } from "sonner";
import { knowledgeWrite } from "../api/client";
import type { KnowledgeRevision, KnowledgeWriteRequest } from "../api/types";
import { Spinner } from "../components/ui/Spinner";

interface Props {
  onOpenItem?:
    | ((domain: "memory" | "knowledge", namespace: string, key: string) => void)
    | undefined;
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
    sessionId.trim() &&
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
        session_id: sessionId.trim(),
      };
      if (key.trim()) req.key = key.trim();
      if (body.trim()) req.body = body.trim();
      if (authorVersion.trim()) req.author.agent_version = authorVersion.trim();
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
    <div className="min-h-full bg-bg text-text">
      <section className="border-b border-border-strong px-4 py-4">
        <h2 className="text-lg font-semibold tracking-tight">Write knowledge</h2>
        <p className="mt-1 max-w-2xl text-sm leading-6 text-text-subtle">
          Capture a durable reference with its source pointer, provenance, and retrieval metadata.
        </p>
      </section>

      <div className="max-w-6xl p-4">
        <Card size="sm">
          <CardContent className="space-y-4">
            {error ? (
              <Callout tone="danger" title="Knowledge write failed">
                {error}
              </Callout>
            ) : null}
            {result ? (
              <Callout tone="success" title={`Wrote revision ${result.revision_id}`}>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="font-mono text-xs">
                    {result.namespace} / {result.memory_key ?? "(no key)"} · kind{" "}
                    {result.facets?.kind ?? kind} · source {result.facets?.source ?? source}
                  </span>
                  {onOpenItem && result.memory_key ? (
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      onClick={() =>
                        onOpenItem("knowledge", result.namespace, result.memory_key ?? "")
                      }
                    >
                      Open detail
                    </Button>
                  ) : null}
                </div>
              </Callout>
            ) : null}

            <div className="grid gap-4 md:grid-cols-[2fr_1fr_1fr]">
              <Field id="kw-namespace" label="Namespace" required>
                <Input
                  id="kw-namespace"
                  className="font-mono"
                  placeholder="user/<actor>/knowledge/<scope>"
                  value={namespace}
                  onChange={(event) => setNamespace(event.target.value)}
                />
              </Field>
              <Field id="kw-key" label="Key">
                <Input
                  id="kw-key"
                  className="font-mono"
                  placeholder="design.doc"
                  value={key}
                  onChange={(event) => setKey(event.target.value)}
                />
              </Field>
              <Field id="kw-supersedes" label="Supersedes">
                <Input
                  id="kw-supersedes"
                  className="font-mono"
                  placeholder="revision id"
                  value={supersedes}
                  onChange={(event) => setSupersedes(event.target.value)}
                />
              </Field>
            </div>

            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-[1fr_1fr_1fr_2fr]">
              <Field id="kw-kind" label="Kind" required>
                <Input
                  id="kw-kind"
                  className="font-mono"
                  value={kind}
                  onChange={(event) => setKind(event.target.value)}
                />
              </Field>
              <Field id="kw-source" label="Source" required>
                <Input
                  id="kw-source"
                  className="font-mono"
                  value={source}
                  onChange={(event) => setSource(event.target.value)}
                />
              </Field>
              <Field id="kw-pointer-scheme" label="Pointer scheme" required>
                <Input
                  id="kw-pointer-scheme"
                  className="font-mono"
                  value={pointerScheme}
                  onChange={(event) => setPointerScheme(event.target.value)}
                />
              </Field>
              <Field id="kw-pointer-locator" label="Pointer locator" required>
                <Input
                  id="kw-pointer-locator"
                  className="font-mono"
                  placeholder="/docs/spec.md or https://…"
                  value={pointerLocator}
                  onChange={(event) => setPointerLocator(event.target.value)}
                />
              </Field>
            </div>

            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <Field id="kw-author" label="Author agent_id" required>
                <Input
                  id="kw-author"
                  className="font-mono"
                  value={authorAgentId}
                  onChange={(event) => setAuthorAgentId(event.target.value)}
                />
              </Field>
              <Field id="kw-author-version" label="Author version">
                <Input
                  id="kw-author-version"
                  className="font-mono"
                  value={authorVersion}
                  onChange={(event) => setAuthorVersion(event.target.value)}
                />
              </Field>
              <Field id="kw-session-id" label="Session ID" required>
                <Input
                  id="kw-session-id"
                  className="font-mono"
                  value={sessionId}
                  onChange={(event) => setSessionId(event.target.value)}
                />
              </Field>
              <Field id="kw-confidence" label="Confidence">
                <Input
                  id="kw-confidence"
                  className="font-mono"
                  type="number"
                  min="0"
                  max="1"
                  step="0.05"
                  value={confidence}
                  onChange={(event) => setConfidence(event.target.value)}
                />
              </Field>
            </div>

            <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
              <Field id="kw-tags" label="Tags">
                <Input
                  id="kw-tags"
                  className="font-mono"
                  placeholder="doc, architecture, source:repo"
                  value={tagsField}
                  onChange={(event) => setTagsField(event.target.value)}
                />
              </Field>
              <Field id="kw-ttl" label="TTL seconds">
                <Input
                  id="kw-ttl"
                  className="font-mono"
                  type="number"
                  min="0"
                  value={ttlSeconds}
                  onChange={(event) => setTtlSeconds(event.target.value)}
                />
              </Field>
            </div>

            <Field id="kw-summary" label="Summary" required>
              <Textarea
                id="kw-summary"
                rows={2}
                placeholder="Short operator-facing summary of the knowledge item."
                value={summary}
                onChange={(event) => setSummary(event.target.value)}
              />
            </Field>
            <Field id="kw-body" label="Body">
              <Textarea
                id="kw-body"
                className="font-mono"
                rows={7}
                placeholder="Long-form body content…"
                value={body}
                onChange={(event) => setBody(event.target.value)}
              />
            </Field>

            <Button type="button" onClick={() => void handleSubmit()} disabled={!canSubmit}>
              {submitting ? <Spinner size={13} /> : <BookOpen aria-hidden="true" />} Write knowledge
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function Field({
  id,
  label,
  required = false,
  children,
}: {
  id: string;
  label: string;
  required?: boolean;
  children: ReactNode;
}) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>
        {label}
        {required ? <span className="text-danger"> *</span> : null}
      </Label>
      {children}
    </div>
  );
}
