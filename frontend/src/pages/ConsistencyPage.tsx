import {
  Button,
  Callout,
  ConfirmDialog,
  EmptyState,
  PageHeader,
  Pill,
  SummaryCards,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@hollis-labs/sysop-ui";
import { ListPageLayout } from "@hollis-labs/sysop-ui/layout";
import { AlertTriangle, CheckCircle, Search, Wrench } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { repairConsistency, scanConsistency } from "../api/client";
import type { ConsistencyRepairResponse, ConsistencyScanResponse } from "../api/types";
import { Spinner } from "../components/ui/Spinner";

type Issue = { type: string; namespace: string; key: string; details: string };

export function ConsistencyPage() {
  const [scanning, setScanning] = useState(false);
  const [repairing, setRepairing] = useState(false);
  const [scanResult, setScanResult] = useState<ConsistencyScanResponse | null>(null);
  const [repairResult, setRepairResult] = useState<ConsistencyRepairResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [showRepairConfirm, setShowRepairConfirm] = useState(false);

  const handleScan = async () => {
    setScanning(true);
    setError(null);
    setRepairResult(null);
    try {
      const response = await scanConsistency();
      setScanResult(response);
      if (response.count === 0) {
        toast.success("No consistency issues found");
      } else {
        toast.warning(`Found ${response.count} issue${response.count === 1 ? "" : "s"}`);
      }
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      setError(message);
      toast.error(`Scan failed: ${message}`);
    } finally {
      setScanning(false);
    }
  };

  const handleRepair = async () => {
    setRepairing(true);
    setError(null);
    setShowRepairConfirm(false);
    try {
      const response = await repairConsistency();
      setRepairResult(response);
      setScanResult(null);
      toast.success(`Repair complete: ${response.rebuilt_heads} heads rebuilt`);
    } catch (reason) {
      const message = reason instanceof Error ? reason.message : String(reason);
      setError(message);
      toast.error(`Repair failed: ${message}`);
    } finally {
      setRepairing(false);
    }
  };

  const actions = (
    <div className="flex items-center gap-2">
      <Button type="button" size="sm" onClick={handleScan} disabled={scanning || repairing}>
        {scanning ? <Spinner size={13} /> : <Search aria-hidden="true" />}
        Scan
      </Button>
      {scanResult && scanResult.count > 0 ? (
        <Button
          type="button"
          variant="destructive"
          size="sm"
          onClick={() => setShowRepairConfirm(true)}
          disabled={repairing}
        >
          {repairing ? <Spinner size={13} /> : <Wrench aria-hidden="true" />}
          Repair
        </Button>
      ) : null}
    </div>
  );

  return (
    <ListPageLayout header={<PageHeader title="Consistency">{actions}</PageHeader>}>
      <section className="border-b border-border-strong bg-panel px-4 py-4 lg:px-6">
        <h2 className="text-sm font-semibold text-text">Database integrity</h2>
        <p className="mt-1 max-w-3xl text-xs leading-5 text-text-subtle">
          Scan head pointers against stored revisions. Repair rebuilds inconsistent pointers and
          modifies the database.
        </p>
      </section>

      {error ? (
        <div className="border-b border-border-strong px-4 py-4 lg:px-6">
          <Callout tone="danger" title="Consistency operation failed">
            {error}
          </Callout>
        </div>
      ) : null}

      {repairResult ? (
        <section aria-labelledby="repair-result-heading">
          <div className="flex items-center gap-3 border-b border-border-strong px-4 py-3 lg:px-6">
            <CheckCircle className="size-4 text-status-done" aria-hidden="true" />
            <h2 id="repair-result-heading" className="text-sm font-semibold text-text">
              Repair complete
            </h2>
            <Pill tone={repairResult.remaining_issues > 0 ? "warning" : "success"} dot>
              {repairResult.remaining_issues > 0 ? "Needs review" : "Consistent"}
            </Pill>
          </div>
          <SummaryCards
            cards={[
              { label: "Heads rebuilt", value: repairResult.rebuilt_heads },
              { label: "Remaining issues", value: repairResult.remaining_issues },
            ]}
          />
          {repairResult.issues.length > 0 ? (
            <div>
              <div className="border-b border-border px-4 py-3 lg:px-6">
                <h3 className="text-xs font-semibold text-warning">Remaining issues</h3>
              </div>
              <IssueTable issues={repairResult.issues} />
            </div>
          ) : null}
        </section>
      ) : null}

      {scanResult ? (
        <section aria-labelledby="scan-result-heading">
          <div className="flex items-center gap-3 border-b border-border-strong px-4 py-3 lg:px-6">
            {scanResult.count === 0 ? (
              <CheckCircle className="size-4 text-status-done" aria-hidden="true" />
            ) : (
              <AlertTriangle className="size-4 text-warning" aria-hidden="true" />
            )}
            <h2 id="scan-result-heading" className="text-sm font-semibold text-text">
              {scanResult.count === 0
                ? "All checks passed"
                : `${scanResult.count} issue${scanResult.count === 1 ? "" : "s"} found`}
            </h2>
            <Pill tone={scanResult.count === 0 ? "success" : "warning"} dot>
              {scanResult.count === 0 ? "Consistent" : "Repair available"}
            </Pill>
          </div>
          {scanResult.count === 0 ? (
            <EmptyState
              variant="empty"
              title="No issues"
              description="The database is consistent."
            />
          ) : (
            <IssueTable issues={scanResult.issues} />
          )}
        </section>
      ) : null}

      {!scanResult && !repairResult && !error ? (
        <EmptyState
          variant="empty"
          title="Run a consistency scan"
          description="Check the database for head-pointer issues before attempting a repair."
          action={{ label: "Scan database", onClick: handleScan }}
        />
      ) : null}

      <ConfirmDialog
        open={showRepairConfirm}
        onOpenChange={setShowRepairConfirm}
        title="Repair consistency issues"
        description={`Rebuild head pointers for ${scanResult?.count ?? 0} consistency issue(s)? This modifies the database.`}
        confirmLabel="Repair"
        busy={repairing}
        onConfirm={handleRepair}
      />
    </ListPageLayout>
  );
}

function IssueTable({ issues }: { issues: Issue[] }) {
  return (
    <div className="min-w-[48rem]">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Type</TableHead>
            <TableHead>Namespace</TableHead>
            <TableHead>Key</TableHead>
            <TableHead>Details</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {issues.map((issue) => (
            <TableRow key={`${issue.type}:${issue.namespace}:${issue.key}:${issue.details}`}>
              <TableCell>
                <Pill tone="warning">{issue.type}</Pill>
              </TableCell>
              <TableCell className="font-mono text-xs text-text-soft">{issue.namespace}</TableCell>
              <TableCell className="font-mono text-xs text-text-soft">{issue.key}</TableCell>
              <TableCell className="text-xs leading-5 text-text-subtle">{issue.details}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
