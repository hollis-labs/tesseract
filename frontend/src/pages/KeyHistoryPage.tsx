import {
  Button,
  Callout,
  Card,
  Checkbox,
  Label,
  Pill,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@hollis-labs/sysop-ui";
import { ArrowLeft, ArrowRightLeft, GitCommit, RefreshCw } from "lucide-react";
import { useCallback, useState } from "react";
import { getHistory } from "../api/client";
import type { Record } from "../api/types";
import { CopyButton } from "../components/ui/CopyButton";
import { EmptyState } from "../components/ui/EmptyState";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

interface Props {
  namespace: string;
  recordKey: string;
  onBack: () => void;
  onCompare: (namespace: string, key: string, revA: number, revB: number) => void;
}

export function KeyHistoryPage({ namespace, recordKey, onBack, onCompare }: Props) {
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const fetcher = useCallback(() => getHistory(namespace, recordKey, 100), [namespace, recordKey]);
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);
  const items: Record[] = data?.items ?? [];

  const toggleSelect = (revision: number) => {
    setSelected((previous) => {
      const next = new Set(previous);
      if (next.has(revision)) {
        next.delete(revision);
      } else {
        if (next.size >= 2) {
          const [first] = next;
          if (first !== undefined) next.delete(first);
        }
        next.add(revision);
      }
      return next;
    });
  };

  const canCompare = selected.size === 2;
  const handleCompare = () => {
    const [revisionA, revisionB] = Array.from(selected).sort((a, b) => a - b);
    if (revisionA === undefined || revisionB === undefined) return;
    onCompare(namespace, recordKey, revisionA, revisionB);
  };

  return (
    <div className="min-h-full bg-bg text-text">
      <nav
        className="flex items-center gap-2 border-b border-border-soft px-4 py-2 text-xs text-text-subtle"
        aria-label="Breadcrumb"
      >
        <Button type="button" variant="ghost" size="xs" onClick={onBack}>
          <ArrowLeft aria-hidden="true" />
          Record
        </Button>
        <span aria-hidden="true">/</span>
        <span>History</span>
      </nav>

      <section className="border-b border-border-strong px-4 py-4">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div className="min-w-0 max-w-2xl">
            <h2 className="truncate text-lg font-semibold tracking-tight">
              Revision history for <span className="font-mono">{recordKey}</span>
            </h2>
            <p className="mt-1 break-all font-mono text-xs text-text-subtle">{namespace}</p>
          </div>
          <div className="flex items-center gap-2">
            {canCompare ? (
              <Button type="button" size="sm" onClick={handleCompare}>
                <ArrowRightLeft aria-hidden="true" />
                Compare selected
              </Button>
            ) : null}
            <Button type="button" variant="outline" size="sm" onClick={refresh} disabled={loading}>
              {loading ? <Spinner size={14} /> : <RefreshCw aria-hidden="true" />}
              Refresh
            </Button>
          </div>
        </div>
        <p
          id="history-selection-summary"
          className="mt-3 text-xs text-text-subtle"
          aria-live="polite"
        >
          {items.length} revision{items.length === 1 ? "" : "s"}. Select two to compare.
          {selected.size > 0 ? ` ${selected.size} selected.` : ""}
        </p>
      </section>

      <div className="space-y-4 p-4">
        {error ? (
          <Callout tone="danger" title="History unavailable">
            {error.message}
          </Callout>
        ) : null}

        <Card size="sm" className="overflow-hidden">
          {loading && !data ? (
            <div className="flex justify-center py-12 text-text-subtle">
              <Spinner size={20} />
            </div>
          ) : null}

          {!loading && items.length === 0 ? (
            <EmptyState
              message="No revisions found"
              sub="This key does not have revision history."
            />
          ) : null}

          {items.length > 0 ? (
            <Table aria-describedby="history-selection-summary">
              <TableHeader>
                <TableRow>
                  <TableHead className="w-12">
                    <span className="sr-only">Select</span>
                  </TableHead>
                  <TableHead>Revision</TableHead>
                  <TableHead>Actor</TableHead>
                  <TableHead>Checksum</TableHead>
                  <TableHead className="text-right">Created</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((item, index) => {
                  const isSelected = selected.has(item.revision);
                  const isLatest = index === 0;
                  const checkboxId = `history-revision-${item.revision}`;
                  return (
                    <TableRow key={item.revision} data-state={isSelected ? "selected" : undefined}>
                      <TableCell>
                        <Checkbox
                          id={checkboxId}
                          checked={isSelected}
                          onCheckedChange={() => toggleSelect(item.revision)}
                          aria-label={`Select revision ${item.revision} for comparison`}
                        />
                      </TableCell>
                      <TableCell>
                        <Label
                          htmlFor={checkboxId}
                          className="flex cursor-pointer items-center gap-2 font-mono text-xs"
                        >
                          <GitCommit
                            className={
                              isLatest ? "size-3.5 text-status-doing" : "size-3.5 text-text-subtle"
                            }
                            aria-hidden="true"
                          />
                          <span
                            className={isLatest ? "font-semibold text-status-doing" : "text-text"}
                          >
                            r{item.revision}
                          </span>
                          {isLatest ? <Pill tone="info">HEAD</Pill> : null}
                        </Label>
                      </TableCell>
                      <TableCell className="max-w-48 truncate text-text-soft">
                        {item.actor}
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <span className="font-mono text-[11px] text-text-subtle">
                            {item.checksum?.slice(0, 12)}
                          </span>
                          <CopyButton text={item.checksum} size={11} />
                        </div>
                      </TableCell>
                      <TableCell className="text-right font-mono text-[11px] text-text-subtle">
                        {new Date(item.created_at).toLocaleString()}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          ) : null}
        </Card>
      </div>
    </div>
  );
}
