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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@hollis-labs/sysop-ui";
import { ArrowLeft, ArrowRight, Check, Play, RefreshCw } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { toast } from "sonner";
import { getAuditEvents, promoteApply, promoteApprove, promoteRequest } from "../api/client";
import type { AuditEvent } from "../api/types";
import { EmptyState } from "../components/ui/EmptyState";
import { Spinner } from "../components/ui/Spinner";
import { StatusBadge } from "../components/ui/StatusBadge";
import { usePoll } from "../hooks/usePoll";

interface Props {
  onBack: () => void;
}

type Tab = "request" | "dashboard";

export function PromotePage({ onBack }: Props) {
  const [tab, setTab] = useState<Tab>("request");

  return (
    <div className="min-h-full bg-bg text-text">
      <nav
        className="flex items-center gap-2 border-b border-border-soft px-4 py-2 text-xs text-text-subtle"
        aria-label="Breadcrumb"
      >
        <Button type="button" variant="ghost" size="xs" onClick={onBack}>
          <ArrowLeft aria-hidden="true" />
          Write and promote
        </Button>
        <span aria-hidden="true">/</span>
        <span>Promote</span>
      </nav>

      <section className="border-b border-border-strong px-4 py-4">
        <h2 className="text-lg font-semibold tracking-tight">Promote a context record</h2>
        <p className="mt-1 max-w-2xl text-sm leading-6 text-text-subtle">
          Move an existing record between namespaces through the request, approval, and apply
          workflow.
        </p>
      </section>

      <Tabs value={tab} onValueChange={(value) => setTab(value as Tab)} className="space-y-4 p-4">
        <TabsList variant="line" aria-label="Promotion workflow">
          <TabsTrigger value="request">New request</TabsTrigger>
          <TabsTrigger value="dashboard">Promotion log</TabsTrigger>
        </TabsList>
        <TabsContent value="request">
          <PromoteRequestForm />
        </TabsContent>
        <TabsContent value="dashboard">
          <PromotionDashboard />
        </TabsContent>
      </Tabs>
    </div>
  );
}

function PromoteRequestForm() {
  const [srcNamespace, setSrcNamespace] = useState("");
  const [srcKey, setSrcKey] = useState("");
  const [tgtNamespace, setTgtNamespace] = useState("");
  const [tgtKey, setTgtKey] = useState("");
  const [actor, setActor] = useState("");
  const [reason, setReason] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<unknown>(null);

  const canSubmit = Boolean(
    srcNamespace.trim() &&
      srcKey.trim() &&
      tgtNamespace.trim() &&
      tgtKey.trim() &&
      actor.trim() &&
      !submitting,
  );

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    setResult(null);
    try {
      const request: Parameters<typeof promoteRequest>[0] = {
        actor: actor.trim(),
        source_namespace: srcNamespace.trim(),
        source_key: srcKey.trim(),
        target_namespace: tgtNamespace.trim(),
        target_key: tgtKey.trim(),
      };
      if (reason.trim()) request.reason = reason.trim();
      const response = await promoteRequest(request);
      setResult(response);
      toast.success("Promotion requested");
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      setError(message);
      toast.error(`Request failed: ${message}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Card size="sm" className="max-w-3xl">
      <CardHeader className="border-b border-border-strong">
        <div>
          <CardTitle>Promotion route</CardTitle>
          <CardDescription className="mt-1">
            Required fields are marked with an asterisk.
          </CardDescription>
        </div>
      </CardHeader>

      <CardContent className="space-y-5">
        {error ? (
          <Callout tone="danger" title="Request failed">
            {error}
          </Callout>
        ) : null}
        {result != null ? (
          <Callout tone="success" title="Promotion requested">
            Open the promotion log to approve and apply this request.
          </Callout>
        ) : null}

        <div className="grid items-stretch gap-3 md:grid-cols-[1fr_auto_1fr]">
          <fieldset className="space-y-4 border border-border-soft bg-panel p-4">
            <legend className="px-1 text-sm font-medium text-text">Source</legend>
            <div className="space-y-2">
              <Label htmlFor="promote-source-namespace">
                Namespace <span className="text-danger">*</span>
              </Label>
              <Input
                id="promote-source-namespace"
                className="font-mono"
                placeholder="app/test/session"
                value={srcNamespace}
                onChange={(event) => setSrcNamespace(event.target.value)}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="promote-source-key">
                Key <span className="text-danger">*</span>
              </Label>
              <Input
                id="promote-source-key"
                className="font-mono"
                placeholder="status"
                value={srcKey}
                onChange={(event) => setSrcKey(event.target.value)}
                required
              />
            </div>
          </fieldset>

          <div className="flex items-center justify-center text-text-subtle" aria-hidden="true">
            <ArrowRight className="size-5 rotate-90 md:rotate-0" />
          </div>

          <fieldset className="space-y-4 border border-border-soft bg-panel p-4">
            <legend className="px-1 text-sm font-medium text-text">Target</legend>
            <div className="space-y-2">
              <Label htmlFor="promote-target-namespace">
                Namespace <span className="text-danger">*</span>
              </Label>
              <Input
                id="promote-target-namespace"
                className="font-mono"
                placeholder="user/memory/project"
                value={tgtNamespace}
                onChange={(event) => setTgtNamespace(event.target.value)}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="promote-target-key">
                Key <span className="text-danger">*</span>
              </Label>
              <Input
                id="promote-target-key"
                className="font-mono"
                placeholder="status"
                value={tgtKey}
                onChange={(event) => setTgtKey(event.target.value)}
                required
              />
            </div>
          </fieldset>
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="promote-actor">
              Actor <span className="text-danger">*</span>
            </Label>
            <Input
              id="promote-actor"
              className="font-mono"
              placeholder="user:jane"
              value={actor}
              onChange={(event) => setActor(event.target.value)}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="promote-reason">Reason (optional)</Label>
            <Input
              id="promote-reason"
              placeholder="Promote to user memory"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
          </div>
        </div>
      </CardContent>

      <CardFooter className="justify-end border-t border-border-strong">
        <Button type="button" onClick={() => void handleSubmit()} disabled={!canSubmit}>
          {submitting ? <Spinner size={14} /> : <ArrowRight aria-hidden="true" />}
          Request promotion
        </Button>
      </CardFooter>
    </Card>
  );
}

function PromotionDashboard() {
  const fetcher = useCallback(() => getAuditEvents({ event_type: "promote", limit: 50 }), []);
  const { data, loading, error, refresh } = usePoll(fetcher, 10_000);
  const requestFetcher = useCallback(
    () => getAuditEvents({ event_type: "promote.request", limit: 50 }),
    [],
  );
  const { data: requestData } = usePoll(requestFetcher, 10_000);

  const allEvents = useMemo(() => {
    const events: AuditEvent[] = [];
    if (data?.items) events.push(...data.items);
    if (requestData?.items) events.push(...requestData.items);
    return events.sort((a, b) => b.created_at.localeCompare(a.created_at));
  }, [data, requestData]);

  const handleApprove = async (requestId: string) => {
    try {
      await promoteApprove({ request_id: requestId, actor: "ui-user" });
      toast.success("Promotion approved");
      refresh();
    } catch (reason) {
      toast.error(`Approve failed: ${reason instanceof Error ? reason.message : reason}`);
    }
  };

  const handleApply = async (requestId: string) => {
    try {
      await promoteApply({ request_id: requestId, actor: "ui-user" });
      toast.success("Promotion applied");
      refresh();
    } catch (reason) {
      toast.error(`Apply failed: ${reason instanceof Error ? reason.message : reason}`);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="button" variant="outline" size="sm" onClick={refresh} disabled={loading}>
          {loading ? <Spinner size={14} /> : <RefreshCw aria-hidden="true" />}
          Refresh
        </Button>
      </div>

      {error ? (
        <Callout tone="danger" title="Promotion log unavailable">
          {error.message}
        </Callout>
      ) : null}

      <Card size="sm" className="overflow-hidden">
        {loading && !data ? (
          <div className="flex justify-center py-12 text-text-subtle">
            <Spinner size={20} />
          </div>
        ) : null}

        {!loading && allEvents.length === 0 ? (
          <EmptyState message="No promotion events" sub="Request a promotion to get started." />
        ) : null}

        {allEvents.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Status</TableHead>
                <TableHead>Namespace</TableHead>
                <TableHead>Key</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Time</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {allEvents.map((event) => {
                const requestId = event.record_id;
                const status = event.event_type.includes("apply")
                  ? "applied"
                  : event.event_type.includes("approve")
                    ? "approved"
                    : event.event_type.includes("request")
                      ? "pending"
                      : "success";
                return (
                  <TableRow key={event.id}>
                    <TableCell>
                      <StatusBadge status={status} />
                    </TableCell>
                    <TableCell className="font-mono text-xs">{event.namespace}</TableCell>
                    <TableCell className="font-mono text-xs">{event.key}</TableCell>
                    <TableCell className="text-text-soft">{event.actor}</TableCell>
                    <TableCell className="font-mono text-[11px] text-text-subtle">
                      {new Date(event.created_at).toLocaleString()}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-2">
                        {status === "pending" && requestId ? (
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            onClick={() => void handleApprove(requestId)}
                          >
                            <Check aria-hidden="true" />
                            Approve
                          </Button>
                        ) : null}
                        {status === "approved" && requestId ? (
                          <Button
                            type="button"
                            size="xs"
                            onClick={() => void handleApply(requestId)}
                          >
                            <Play aria-hidden="true" />
                            Apply
                          </Button>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        ) : null}
      </Card>
    </div>
  );
}
