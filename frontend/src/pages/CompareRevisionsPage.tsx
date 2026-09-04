import { Button, Callout, Card, CardContent, CardHeader, CardTitle } from "@hollis-labs/sysop-ui";
import { ArrowLeft } from "lucide-react";
import { useEffect, useState } from "react";
import { getHistory } from "../api/client";
import type { Record } from "../api/types";
import { Spinner } from "../components/ui/Spinner";

interface Props {
  namespace: string;
  recordKey: string;
  revisionA: number;
  revisionB: number;
  onBack: () => void;
}

type DiffLine = { id: string; type: "same" | "added" | "removed"; text: string };

export function CompareRevisionsPage({
  namespace,
  recordKey,
  revisionA,
  revisionB,
  onBack,
}: Props) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [recordA, setRecordA] = useState<Record | null>(null);
  const [recordB, setRecordB] = useState<Record | null>(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    getHistory(namespace, recordKey, 200)
      .then((response) => {
        const items = response.items ?? [];
        const first = items.find((record) => record.revision === revisionA) ?? null;
        const second = items.find((record) => record.revision === revisionB) ?? null;
        setRecordA(first);
        setRecordB(second);
        if (!first || !second) setError("One or both revisions were not found.");
      })
      .catch((reason) => setError(reason instanceof Error ? reason.message : String(reason)))
      .finally(() => setLoading(false));
  }, [namespace, recordKey, revisionA, revisionB]);

  const diffLines =
    recordA && recordB
      ? computeDiff(
          JSON.stringify(recordA.payload, null, 2),
          JSON.stringify(recordB.payload, null, 2),
        )
      : [];

  return (
    <div className="min-h-full bg-bg text-text">
      <nav
        className="flex items-center gap-2 border-b border-border-soft px-4 py-2 text-xs text-text-subtle"
        aria-label="Breadcrumb"
      >
        <Button type="button" variant="ghost" size="xs" onClick={onBack}>
          <ArrowLeft aria-hidden="true" />
          History
        </Button>
        <span aria-hidden="true">/</span>
        <span>
          Compare r{revisionA} and r{revisionB}
        </span>
      </nav>

      <section className="border-b border-border-strong px-4 py-4">
        <h2 className="text-lg font-semibold tracking-tight">
          Compare revisions r{revisionA} and r{revisionB}
        </h2>
        <p className="mt-1 break-all font-mono text-xs text-text-subtle">
          {namespace} / {recordKey}
        </p>
      </section>

      <div className="space-y-4 p-4">
        {error ? (
          <Callout tone="danger" title="Comparison unavailable">
            {error}
          </Callout>
        ) : null}

        {loading ? (
          <div className="flex justify-center py-12 text-text-subtle">
            <Spinner size={20} />
          </div>
        ) : null}

        {!loading && recordA && recordB ? (
          <>
            <section className="grid gap-3 sm:grid-cols-2" aria-label="Revision metadata">
              <RevisionCard label={`r${revisionA}`} record={recordA} />
              <RevisionCard label={`r${revisionB}`} record={recordB} />
            </section>

            <section aria-labelledby="payload-diff-title">
              <h3 id="payload-diff-title" className="mb-2 text-sm font-medium">
                Payload diff
              </h3>
              <Card size="sm" className="max-h-[500px] overflow-auto">
                <CardContent className="p-0">
                  {diffLines.length > 0 ? (
                    <pre className="m-0 py-3 font-mono text-xs leading-6">
                      {diffLines.map((line) => (
                        <span
                          key={line.id}
                          className={
                            line.type === "added"
                              ? "block bg-status-done/10 px-3 text-status-done"
                              : line.type === "removed"
                                ? "block bg-status-blocked/10 px-3 text-status-blocked"
                                : "block px-3 text-text-soft"
                          }
                        >
                          <span className="mr-2 inline-block w-3 select-none" aria-hidden="true">
                            {line.type === "added" ? "+" : line.type === "removed" ? "-" : " "}
                          </span>
                          {line.text}
                          {"\n"}
                        </span>
                      ))}
                    </pre>
                  ) : (
                    <p className="px-4 py-8 text-center text-sm text-text-subtle">
                      No differences in payload
                    </p>
                  )}
                </CardContent>
              </Card>
            </section>
          </>
        ) : null}
      </div>
    </div>
  );
}

function RevisionCard({ label, record }: { label: string; record: Record }) {
  return (
    <Card size="sm">
      <CardHeader className="border-b border-border-strong">
        <CardTitle className="font-mono text-status-doing">{label}</CardTitle>
      </CardHeader>
      <CardContent>
        <dl className="space-y-2 text-xs">
          <Row label="Actor" value={record.actor} />
          <Row label="Checksum" value={`${record.checksum?.slice(0, 16)}...`} mono />
          <Row label="Created" value={new Date(record.created_at).toLocaleString()} />
        </dl>
      </CardContent>
    </Card>
  );
}

function Row({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[5rem_1fr] gap-3">
      <dt className="text-text-subtle">{label}</dt>
      <dd className={mono ? "break-all font-mono text-text-soft" : "text-text-soft"}>{value}</dd>
    </div>
  );
}

function computeDiff(textA: string, textB: string): DiffLine[] {
  const linesA = textA.split("\n");
  const linesB = textB.split("\n");
  const n = linesA.length;
  const m = linesB.length;

  if (n * m > 100_000) return simpleDiff(linesA, linesB);

  const table: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = 1; i <= n; i++) {
    for (let j = 1; j <= m; j++) {
      const row = table[i];
      if (!row) continue;
      if (linesA[i - 1] === linesB[j - 1]) {
        row[j] = cell(table, i - 1, j - 1) + 1;
      } else {
        row[j] = Math.max(cell(table, i - 1, j), cell(table, i, j - 1));
      }
    }
  }

  const result: DiffLine[] = [];
  let i = n;
  let j = m;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && linesA[i - 1] === linesB[j - 1]) {
      result.push({ id: `same-${i}-${j}`, type: "same", text: linesA[i - 1] ?? "" });
      i--;
      j--;
    } else if (j > 0 && (i === 0 || cell(table, i, j - 1) >= cell(table, i - 1, j))) {
      result.push({ id: `added-${i}-${j}`, type: "added", text: linesB[j - 1] ?? "" });
      j--;
    } else {
      result.push({ id: `removed-${i}-${j}`, type: "removed", text: linesA[i - 1] ?? "" });
      i--;
    }
  }

  return result.reverse();
}

function cell(table: number[][], row: number, column: number): number {
  return table[row]?.[column] ?? 0;
}

function simpleDiff(linesA: string[], linesB: string[]): DiffLine[] {
  const result: DiffLine[] = [];
  for (const [index, line] of linesA.entries()) {
    result.push({ id: `removed-${index}`, type: "removed", text: line });
  }
  for (const [index, line] of linesB.entries()) {
    result.push({ id: `added-${index}`, type: "added", text: line });
  }
  return result;
}
