import {
  Button,
  Callout,
  Checkbox,
  ConfirmDialog,
  Input,
  Label,
  PageHeader,
  SummaryCards,
} from "@hollis-labs/sysop-ui";
import { ListPageLayout, TabStrip, type TabStripItem } from "@hollis-labs/sysop-ui/layout";
import { Archive, Scissors } from "lucide-react";
import { type ReactNode, useState } from "react";
import { toast } from "sonner";
import { compactRecords, trimRecords } from "../api/client";
import type { CompactResponse, TrimResponse } from "../api/types";
import { Spinner } from "../components/ui/Spinner";

type Tab = "trim" | "compact";

const TABS: readonly TabStripItem<Tab>[] = [
  { key: "trim", label: "Trim", icon: <Scissors className="size-3.5" aria-hidden="true" /> },
  { key: "compact", label: "Compact", icon: <Archive className="size-3.5" aria-hidden="true" /> },
];

export function MaintenancePage() {
  const [tab, setTab] = useState<Tab>("trim");

  return (
    <ListPageLayout
      header={<PageHeader title="Maintenance" />}
      tabs={<TabStrip tabs={TABS} value={tab} onChange={setTab} />}
    >
      {tab === "trim" ? <TrimForm /> : <CompactForm />}
    </ListPageLayout>
  );
}

function TrimForm() {
  const [nsPattern, setNsPattern] = useState("*");
  const [retention, setRetention] = useState("720h");
  const [dryRun, setDryRun] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<TrimResponse | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);

  const handleSubmit = async (dry: boolean) => {
    setSubmitting(true);
    setError(null);
    setResult(null);
    setShowConfirm(false);
    try {
      const response = await trimRecords({
        namespace_pattern: nsPattern.trim() || "*",
        retention: retention.trim(),
        dry_run: dry,
      });
      setResult(response);
      if (dry) {
        toast.success(`Dry run: ${response.trimmed} records would be trimmed`);
      } else {
        toast.success(`Trimmed ${response.trimmed} records`);
      }
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      setError(message);
      toast.error(`Trim failed: ${message}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <MaintenanceFormShell
      title="Trim by retention window"
      description="Remove old revisions beyond a retention window. Preview the exact scope with a dry run before deleting data."
    >
      {error ? (
        <Callout tone="danger" title="Trim failed">
          {error}
        </Callout>
      ) : null}

      <form
        className="space-y-5"
        onSubmit={(event) => {
          event.preventDefault();
          if (dryRun) {
            void handleSubmit(true);
          } else {
            setShowConfirm(true);
          }
        }}
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="trim-namespace-pattern">Namespace pattern</Label>
            <Input
              id="trim-namespace-pattern"
              className="font-mono"
              value={nsPattern}
              onChange={(event) => setNsPattern(event.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="trim-retention">Retention</Label>
            <Input
              id="trim-retention"
              className="font-mono"
              placeholder="720h"
              value={retention}
              onChange={(event) => setRetention(event.target.value)}
            />
          </div>
        </div>

        <Label
          htmlFor="trim-dry-run"
          className="flex w-fit cursor-pointer items-center gap-2 text-xs font-normal text-text-soft"
        >
          <Checkbox
            id="trim-dry-run"
            checked={dryRun}
            onCheckedChange={(checked) => setDryRun(checked === true)}
          />
          Dry run (preview only)
        </Label>

        <div className="flex flex-wrap gap-2 border-t border-border pt-4">
          <Button
            type="button"
            variant="outline"
            onClick={() => handleSubmit(true)}
            disabled={submitting}
          >
            {submitting ? <Spinner size={13} /> : <Scissors aria-hidden="true" />}
            Dry run
          </Button>
          <Button type="submit" variant="destructive" disabled={submitting}>
            <Scissors aria-hidden="true" />
            Trim now
          </Button>
        </div>
      </form>

      {result ? <TrimResult result={result} /> : null}

      <ConfirmDialog
        open={showConfirm}
        onOpenChange={setShowConfirm}
        title="Confirm trim"
        description={`Permanently delete old revisions matching "${nsPattern}" older than ${retention}? This cannot be undone.`}
        confirmLabel="Trim"
        busy={submitting}
        onConfirm={() => handleSubmit(false)}
      />
    </MaintenanceFormShell>
  );
}

function TrimResult({ result }: { result: TrimResponse }) {
  return (
    <div className="border-t border-border-strong pt-1">
      <SummaryCards
        cards={[
          { label: result.dry_run ? "Would trim" : "Trimmed", value: result.trimmed },
          { label: "Pattern", value: result.namespace_pattern },
          { label: "Duration", value: `${result.duration_ms}ms` },
          ...(result.dry_run ? [{ label: "Mode", value: "Dry run" }] : []),
        ]}
      />
    </div>
  );
}

function CompactForm() {
  const [nsPattern, setNsPattern] = useState("*");
  const [maxRevisions, setMaxRevisions] = useState("10");
  const [dryRun, setDryRun] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<CompactResponse | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);

  const handleSubmit = async (dry: boolean) => {
    setSubmitting(true);
    setError(null);
    setResult(null);
    setShowConfirm(false);
    try {
      const response = await compactRecords({
        namespace_pattern: nsPattern.trim() || "*",
        max_revisions: parseInt(maxRevisions, 10) || 10,
        dry_run: dry,
      });
      setResult(response);
      if (dry) {
        toast.success(`Dry run: ${response.compacted} revisions would be compacted`);
      } else {
        toast.success(`Compacted ${response.compacted} revisions`);
      }
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      setError(message);
      toast.error(`Compact failed: ${message}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <MaintenanceFormShell
      title="Compact revision history"
      description="Reduce each matching key to a maximum revision count. Preview the affected revisions before changing history."
    >
      {error ? (
        <Callout tone="danger" title="Compaction failed">
          {error}
        </Callout>
      ) : null}

      <form
        className="space-y-5"
        onSubmit={(event) => {
          event.preventDefault();
          if (dryRun) {
            void handleSubmit(true);
          } else {
            setShowConfirm(true);
          }
        }}
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="compact-namespace-pattern">Namespace pattern</Label>
            <Input
              id="compact-namespace-pattern"
              className="font-mono"
              value={nsPattern}
              onChange={(event) => setNsPattern(event.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="compact-max-revisions">Max revisions</Label>
            <Input
              id="compact-max-revisions"
              className="font-mono tabular-nums"
              type="number"
              min={1}
              value={maxRevisions}
              onChange={(event) => setMaxRevisions(event.target.value)}
            />
          </div>
        </div>

        <Label
          htmlFor="compact-dry-run"
          className="flex w-fit cursor-pointer items-center gap-2 text-xs font-normal text-text-soft"
        >
          <Checkbox
            id="compact-dry-run"
            checked={dryRun}
            onCheckedChange={(checked) => setDryRun(checked === true)}
          />
          Dry run (preview only)
        </Label>

        <div className="flex flex-wrap gap-2 border-t border-border pt-4">
          <Button
            type="button"
            variant="outline"
            onClick={() => handleSubmit(true)}
            disabled={submitting}
          >
            {submitting ? <Spinner size={13} /> : <Archive aria-hidden="true" />}
            Dry run
          </Button>
          <Button type="submit" variant="destructive" disabled={submitting}>
            <Archive aria-hidden="true" />
            Compact now
          </Button>
        </div>
      </form>

      {result ? (
        <div className="border-t border-border-strong pt-1">
          <SummaryCards
            cards={[
              {
                label: result.dry_run ? "Would compact" : "Compacted",
                value: result.compacted,
              },
              { label: "Pattern", value: result.namespace_pattern },
              { label: "Duration", value: `${result.duration_ms}ms` },
              ...(result.dry_run ? [{ label: "Mode", value: "Dry run" }] : []),
            ]}
          />
        </div>
      ) : null}

      <ConfirmDialog
        open={showConfirm}
        onOpenChange={setShowConfirm}
        title="Confirm compaction"
        description={`Permanently remove excess revisions, keeping at most ${maxRevisions} for keys matching "${nsPattern}"? This cannot be undone.`}
        confirmLabel="Compact"
        busy={submitting}
        onConfirm={() => handleSubmit(false)}
      />
    </MaintenanceFormShell>
  );
}

function MaintenanceFormShell({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <section className="bg-panel px-4 py-5 lg:px-6">
      <div className="max-w-3xl space-y-5">
        <div>
          <h2 className="text-sm font-semibold text-text">{title}</h2>
          <p className="mt-1 max-w-2xl text-xs leading-5 text-text-subtle">{description}</p>
        </div>
        {children}
      </div>
    </section>
  );
}
