import {
  Button,
  Callout,
  CopyButton,
  EmptyState,
  SummaryCards,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@hollis-labs/sysop-ui";
import { DetailHeader, DetailPageLayout } from "@hollis-labs/sysop-ui/layout";
import { FileText, RefreshCw } from "lucide-react";
import { useCallback, useMemo } from "react";
import { evaluateView } from "../api/client";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

interface Props {
  namespace: string;
  onBack: () => void;
  onOpenRecord: (namespace: string, key: string) => void;
}

export function NamespaceDetailPage({ namespace, onBack, onOpenRecord }: Props) {
  const fetcher = useCallback(
    () => evaluateView({ namespaces: [namespace], revision_scope: "head", limit: 200 }),
    [namespace],
  );
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);

  const keys = useMemo(() => {
    if (!data?.items) return [];
    return data.items
      .map((record) => ({
        key: record.key,
        revision: record.revision,
        actor: record.actor,
        created_at: record.created_at,
      }))
      .sort((left, right) => left.key.localeCompare(right.key));
  }, [data]);

  const latestActivity = keys.reduce<string | null>(
    (latest, key) => (latest === null || key.created_at > latest ? key.created_at : latest),
    null,
  );

  return (
    <DetailPageLayout
      header={
        <DetailHeader
          title={namespace}
          backLabel="Explorer"
          onBack={onBack}
          id={namespace}
          actions={
            <>
              <CopyButton text={namespace} label="Copy namespace" size="sm" />
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={refresh}
                disabled={loading}
              >
                {loading ? <Spinner size={13} /> : <RefreshCw aria-hidden="true" />}
                Refresh
              </Button>
            </>
          }
        />
      }
    >
      <SummaryCards
        cards={[
          { label: "Keys", value: keys.length },
          {
            label: "Latest activity",
            value: latestActivity ? new Date(latestActivity).toLocaleString() : "None",
          },
        ]}
      />

      {error ? (
        <div className="border-b border-border-strong px-4 py-4 lg:px-6">
          <Callout tone="danger" title="Namespace unavailable">
            {error.message}
          </Callout>
        </div>
      ) : null}

      {loading && !data ? (
        <div
          className="flex min-h-48 items-center justify-center"
          role="status"
          aria-label="Loading namespace"
        >
          <Spinner size={20} />
        </div>
      ) : null}

      {!loading && keys.length === 0 ? (
        <EmptyState
          variant="empty"
          title="No keys in this namespace"
          description="Records written to this namespace will appear here."
        />
      ) : null}

      {keys.length > 0 ? (
        <div className="min-w-[48rem]">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Key</TableHead>
                <TableHead>Revision</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Last updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {keys.map((key) => (
                <TableRow
                  key={key.key}
                  className="cursor-pointer hover:bg-panel-hover-soft"
                  onClick={() => onOpenRecord(namespace, key.key)}
                >
                  <TableCell>
                    <Button
                      type="button"
                      variant="link"
                      size="sm"
                      className="h-auto justify-start p-0 font-normal text-text"
                      onClick={(event) => {
                        event.stopPropagation();
                        onOpenRecord(namespace, key.key);
                      }}
                      aria-label={`Open record ${key.key}`}
                    >
                      <FileText className="size-3.5 text-text-muted" aria-hidden="true" />
                      <span className="font-mono text-xs">{key.key}</span>
                    </Button>
                  </TableCell>
                  <TableCell className="text-xs tabular-nums text-text-subtle">
                    r{key.revision}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-text-subtle">{key.actor}</TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-text-subtle">
                    {new Date(key.created_at).toLocaleString()}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}
    </DetailPageLayout>
  );
}
