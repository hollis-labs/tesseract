import {
  Button,
  Callout,
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Textarea,
} from "@hollis-labs/sysop-ui";
import { Send } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { toast } from "sonner";
import { evaluateView, writeRecord } from "../api/client";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

interface Props {
  onWritten: (namespace: string, key: string) => void;
  onOpenPromote: () => void;
}

export function WriteRecordPage({ onWritten, onOpenPromote }: Props) {
  const [namespace, setNamespace] = useState("");
  const [key, setKey] = useState("");
  const [actor, setActor] = useState("");
  const [payload, setPayload] = useState("{\n  \n}");
  const [metadata, setMetadata] = useState("");
  const [reason, setReason] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const nsFetcher = useCallback(() => evaluateView({ revision_scope: "head", limit: 500 }), []);
  const { data: viewData } = usePoll(nsFetcher, 30_000);

  const knownNamespaces = useMemo(() => {
    if (!viewData?.items) return [];
    return Array.from(new Set(viewData.items.map((record) => record.namespace))).sort();
  }, [viewData]);

  const payloadError = useMemo(() => {
    if (!payload.trim()) return "Payload is required";
    try {
      JSON.parse(payload);
      return null;
    } catch (reason) {
      return reason instanceof Error ? reason.message : "Invalid JSON";
    }
  }, [payload]);

  const metadataError = useMemo(() => {
    if (!metadata.trim()) return null;
    try {
      JSON.parse(metadata);
      return null;
    } catch (reason) {
      return reason instanceof Error ? reason.message : "Invalid JSON";
    }
  }, [metadata]);

  const canSubmit = Boolean(
    namespace.trim() &&
      key.trim() &&
      actor.trim() &&
      !payloadError &&
      !metadataError &&
      !submitting,
  );

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      const request: Parameters<typeof writeRecord>[0] = {
        namespace: namespace.trim(),
        key: key.trim(),
        actor: actor.trim(),
        payload: JSON.parse(payload),
      };
      if (metadata.trim()) request.metadata = JSON.parse(metadata);
      if (reason.trim()) request.reason = reason.trim();
      const response = await writeRecord(request);
      toast.success(`Record written: r${response.revision}`);
      onWritten(namespace.trim(), key.trim());
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      setError(message);
      toast.error(`Write failed: ${message}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="min-h-full bg-bg text-text">
      <section className="border-b border-border-strong px-4 py-4">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div className="max-w-2xl">
            <h2 className="text-lg font-semibold tracking-tight">Write a context record</h2>
            <p className="mt-1 text-sm leading-6 text-text-subtle">
              Create the next revision for a namespace and key with a JSON payload and an auditable
              actor.
            </p>
          </div>
          <Button type="button" variant="outline" size="sm" onClick={onOpenPromote}>
            Promote a record
          </Button>
        </div>
      </section>

      <div className="max-w-3xl p-4">
        <Card size="sm">
          <CardHeader className="border-b border-border-strong">
            <div>
              <CardTitle>Record identity</CardTitle>
              <CardDescription className="mt-1">
                Required fields are marked with an asterisk.
              </CardDescription>
            </div>
          </CardHeader>

          <CardContent className="space-y-5">
            {error ? (
              <Callout tone="danger" title="Write failed">
                {error}
              </Callout>
            ) : null}

            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="write-namespace">
                  Namespace <span className="text-danger">*</span>
                </Label>
                <Input
                  id="write-namespace"
                  className="font-mono"
                  list="write-namespace-suggestions"
                  placeholder="app/my-project/session"
                  value={namespace}
                  onChange={(event) => setNamespace(event.target.value)}
                  autoComplete="off"
                  required
                />
                <datalist id="write-namespace-suggestions">
                  {knownNamespaces.map((item) => (
                    <option key={item} value={item} />
                  ))}
                </datalist>
              </div>

              <div className="space-y-2">
                <Label htmlFor="write-key">
                  Key <span className="text-danger">*</span>
                </Label>
                <Input
                  id="write-key"
                  className="font-mono"
                  placeholder="status"
                  value={key}
                  onChange={(event) => setKey(event.target.value)}
                  required
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="write-actor">
                  Actor <span className="text-danger">*</span>
                </Label>
                <Input
                  id="write-actor"
                  className="font-mono"
                  placeholder="user:jane or app:my-agent"
                  value={actor}
                  onChange={(event) => setActor(event.target.value)}
                  required
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="write-reason">Reason (optional)</Label>
                <Input
                  id="write-reason"
                  placeholder="Manual update via UI"
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                />
              </div>
            </div>

            <div className="space-y-2">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <Label htmlFor="write-payload">
                  Payload (JSON) <span className="text-danger">*</span>
                </Label>
                {payloadError && payload.trim() ? (
                  <span id="write-payload-error" className="text-xs text-danger" role="alert">
                    {payloadError}
                  </span>
                ) : null}
              </div>
              <Textarea
                id="write-payload"
                className="min-h-44 resize-y font-mono text-xs leading-5"
                value={payload}
                onChange={(event) => setPayload(event.target.value)}
                spellCheck={false}
                aria-invalid={Boolean(payloadError && payload.trim())}
                aria-describedby={
                  payloadError && payload.trim() ? "write-payload-error" : undefined
                }
                required
              />
            </div>

            <div className="space-y-2">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <Label htmlFor="write-metadata">Metadata (JSON, optional)</Label>
                {metadataError ? (
                  <span id="write-metadata-error" className="text-xs text-danger" role="alert">
                    {metadataError}
                  </span>
                ) : null}
              </div>
              <Textarea
                id="write-metadata"
                className="min-h-20 resize-y font-mono text-xs leading-5"
                value={metadata}
                onChange={(event) => setMetadata(event.target.value)}
                placeholder={'{"source": "ui"}'}
                spellCheck={false}
                aria-invalid={Boolean(metadataError)}
                aria-describedby={metadataError ? "write-metadata-error" : undefined}
              />
            </div>
          </CardContent>

          <CardFooter className="justify-end border-t border-border-strong">
            <Button type="button" onClick={() => void handleSubmit()} disabled={!canSubmit}>
              {submitting ? <Spinner size={14} /> : <Send aria-hidden="true" />}
              Write record
            </Button>
          </CardFooter>
        </Card>
      </div>
    </div>
  );
}
