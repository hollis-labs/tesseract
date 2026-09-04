import {
  Button,
  Callout,
  Card,
  CardContent,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
} from "@hollis-labs/sysop-ui";
import { ArrowRight, PenSquare, Send, Trash2 } from "lucide-react";
import { type ReactNode, useState } from "react";
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
  onOpenItem?:
    | ((domain: "memory" | "knowledge", namespace: string, key: string) => void)
    | undefined;
  onOpenReview?: (() => void) | undefined;
}

export function MemoryWritePage({ onOpenItem, onOpenReview }: Props) {
  const [tab, setTab] = useState<Tab>("write");

  return (
    <div className="min-h-full bg-bg text-text">
      <section className="border-b border-border-strong px-4 py-4">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold tracking-tight">Memory operations</h2>
            <p className="mt-1 max-w-2xl text-sm leading-6 text-text-subtle">
              Write, promote, or deprecate memory revisions with explicit provenance.
            </p>
          </div>
          {onOpenReview ? (
            <Button type="button" variant="outline" size="sm" onClick={onOpenReview}>
              Review queue
            </Button>
          ) : null}
        </div>
      </section>

      <Tabs
        value={tab}
        onValueChange={(value) => setTab(value as Tab)}
        className="max-w-6xl gap-3 p-4"
      >
        <TabsList variant="line" aria-label="Memory operations">
          <TabsTrigger value="write">Write</TabsTrigger>
          <TabsTrigger value="promote">Promote</TabsTrigger>
          <TabsTrigger value="deprecate">Deprecate</TabsTrigger>
        </TabsList>
        <TabsContent value="write">
          <WriteForm onOpenItem={onOpenItem} />
        </TabsContent>
        <TabsContent value="promote">
          <PromoteForm onOpenItem={onOpenItem} />
        </TabsContent>
        <TabsContent value="deprecate">
          <DeprecateForm />
        </TabsContent>
      </Tabs>
    </div>
  );
}

function WriteForm({
  onOpenItem,
}: {
  onOpenItem?:
    | ((domain: "memory" | "knowledge", namespace: string, key: string) => void)
    | undefined;
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

  const canSubmit = namespace.trim() && authorAgentId.trim() && summary.trim() && !submitting;

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
    <Card size="sm">
      <CardContent className="space-y-4">
        {error ? (
          <Callout tone="danger" title="Memory write failed">
            {error}
          </Callout>
        ) : null}
        {result ? (
          <Callout tone="success" title={`Wrote revision ${result.revision_id}`}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="font-mono text-xs">
                {result.namespace} / {result.memory_key ?? "(no key)"} · status {result.status} ·
                conf {result.confidence.toFixed(2)}
              </span>
              {onOpenItem && result.memory_key ? (
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  onClick={() => onOpenItem("memory", result.namespace, result.memory_key ?? "")}
                >
                  Open detail
                </Button>
              ) : null}
            </div>
          </Callout>
        ) : null}

        <div className="grid gap-4 md:grid-cols-[2fr_1fr_1fr]">
          <Field id="mw-namespace" label="Namespace" required>
            <Input
              id="mw-namespace"
              className="font-mono"
              placeholder="user/<actor>/memory"
              value={namespace}
              onChange={(event) => setNamespace(event.target.value)}
            />
          </Field>
          <Field id="mw-key" label="Memory key">
            <Input
              id="mw-key"
              className="font-mono"
              placeholder="decisions_…"
              value={memoryKey}
              onChange={(event) => setMemoryKey(event.target.value)}
            />
          </Field>
          <Field id="mw-status" label="Status">
            <Select
              value={status}
              onValueChange={(value) => {
                if (value) setStatus(value as MemoryStatus);
              }}
            >
              <SelectTrigger id="mw-status" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STATUS_OPTIONS.map((item) => (
                  <SelectItem key={item} value={item}>
                    {item}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </div>
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <Field id="mw-author" label="Author agent_id" required>
            <Input
              id="mw-author"
              className="font-mono"
              placeholder="steward / nanite / …"
              value={authorAgentId}
              onChange={(event) => setAuthorAgentId(event.target.value)}
            />
          </Field>
          <Field id="mw-author-ver" label="Author version">
            <Input
              id="mw-author-ver"
              className="font-mono"
              placeholder="0.1.0"
              value={authorVersion}
              onChange={(event) => setAuthorVersion(event.target.value)}
            />
          </Field>
          <Field id="mw-confidence" label="Confidence">
            <Input
              id="mw-confidence"
              className="font-mono"
              type="number"
              step="0.05"
              min="0"
              max="1"
              value={confidence}
              onChange={(event) => setConfidence(event.target.value)}
            />
          </Field>
          <Field id="mw-supersedes" label="Supersedes (revision ID)">
            <Input
              id="mw-supersedes"
              className="font-mono"
              placeholder="01HX…"
              value={supersedes}
              onChange={(event) => setSupersedes(event.target.value)}
            />
          </Field>
        </div>
        <Field id="mw-tags" label="Tags (comma-separated)">
          <Input
            id="mw-tags"
            className="font-mono"
            placeholder="decision, scope:agent-ops.steward.main"
            value={tagsField}
            onChange={(event) => setTagsField(event.target.value)}
          />
        </Field>
        <Field id="mw-summary" label="Summary" required>
          <Textarea
            id="mw-summary"
            placeholder="One-sentence summary of the memory."
            value={summary}
            onChange={(event) => setSummary(event.target.value)}
            rows={2}
          />
        </Field>
        <Field id="mw-body" label="Body (optional, supports Markdown)">
          <Textarea
            id="mw-body"
            className="font-mono"
            placeholder="Long-form body content…"
            value={body}
            onChange={(event) => setBody(event.target.value)}
            rows={6}
          />
        </Field>
        <Button type="button" onClick={() => void handleSubmit()} disabled={!canSubmit}>
          {submitting ? <Spinner size={13} /> : <Send aria-hidden="true" />} Write memory
        </Button>
      </CardContent>
    </Card>
  );
}

function PromoteForm({
  onOpenItem,
}: {
  onOpenItem?:
    | ((domain: "memory" | "knowledge", namespace: string, key: string) => void)
    | undefined;
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
    srcNamespace.trim() &&
    srcMemoryId.trim() &&
    tgtNamespace.trim() &&
    actorAgentId.trim() &&
    !submitting;

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
    <Card size="sm">
      <CardContent className="space-y-4">
        {error ? (
          <Callout tone="danger" title="Promotion failed">
            {error}
          </Callout>
        ) : null}
        {result ? (
          <Callout tone="success" title="Memory promoted">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="font-mono text-xs">
                {result.namespace} · revision {result.revision_id}
              </span>
              {onOpenItem && result.memory_key ? (
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  onClick={() => onOpenItem("memory", result.namespace, result.memory_key ?? "")}
                >
                  Open detail
                </Button>
              ) : null}
            </div>
          </Callout>
        ) : null}
        <p className="text-sm leading-6 text-text-subtle">
          Promote a memory revision into an existing target namespace.
        </p>
        <fieldset className="space-y-3">
          <legend className="text-sm font-medium text-status-doing">Source</legend>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field id="mp-src-ns" label="Namespace" required>
              <Input
                id="mp-src-ns"
                className="font-mono"
                placeholder="app/<id>/memory"
                value={srcNamespace}
                onChange={(event) => setSrcNamespace(event.target.value)}
              />
            </Field>
            <Field id="mp-src-id" label="Memory ID" required>
              <Input
                id="mp-src-id"
                className="font-mono"
                placeholder="01KP… (memory_id, not revision_id)"
                value={srcMemoryId}
                onChange={(event) => setSrcMemoryId(event.target.value)}
              />
            </Field>
          </div>
        </fieldset>
        <ArrowRight className="mx-auto text-text-subtle" aria-hidden="true" />
        <fieldset className="space-y-3">
          <legend className="text-sm font-medium text-status-doing">Target</legend>
          <div className="grid gap-4 md:grid-cols-[2fr_1fr_1fr]">
            <Field id="mp-tgt-ns" label="Namespace" required>
              <Input
                id="mp-tgt-ns"
                className="font-mono"
                placeholder="user/<actor>/memory"
                value={tgtNamespace}
                onChange={(event) => setTgtNamespace(event.target.value)}
              />
            </Field>
            <Field id="mp-actor" label="Actor agent_id" required>
              <Input
                id="mp-actor"
                className="font-mono"
                placeholder="steward"
                value={actorAgentId}
                onChange={(event) => setActorAgentId(event.target.value)}
              />
            </Field>
            <Field id="mp-actor-ver" label="Actor version">
              <Input
                id="mp-actor-ver"
                className="font-mono"
                placeholder="0.1.0"
                value={actorVersion}
                onChange={(event) => setActorVersion(event.target.value)}
              />
            </Field>
          </div>
        </fieldset>
        <Button type="button" onClick={() => void handleSubmit()} disabled={!canSubmit}>
          {submitting ? <Spinner size={13} /> : <PenSquare aria-hidden="true" />} Promote
        </Button>
      </CardContent>
    </Card>
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
    <Card size="sm">
      <CardContent className="space-y-4">
        {error ? (
          <Callout tone="danger" title="Deprecation failed">
            {error}
          </Callout>
        ) : null}
        {result ? (
          <Callout tone="warning" title="Revision deprecated">
            <span className="flex items-center gap-2">
              <StatusBadge status={result.status} />
              <span className="font-mono text-xs">{result.revision_id}</span>
            </span>
          </Callout>
        ) : null}
        <p className="text-sm leading-6 text-text-subtle">
          Mark a memory revision as deprecated. It remains in history but no longer surfaces as the
          head, and the operation is recorded in the audit log.
        </p>
        <Field id="md-revision" label="Revision ID" required>
          <Input
            id="md-revision"
            className="font-mono"
            placeholder="01HX… (revision_id, not memory_id)"
            value={revisionId}
            onChange={(event) => setRevisionId(event.target.value)}
          />
        </Field>
        <Button
          type="button"
          variant="destructive"
          onClick={() => void handleSubmit()}
          disabled={!canSubmit}
        >
          {submitting ? <Spinner size={13} /> : <Trash2 aria-hidden="true" />} Deprecate
        </Button>
      </CardContent>
    </Card>
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
