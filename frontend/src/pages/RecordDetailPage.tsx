import {
  Button,
  Callout,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Pill,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@hollis-labs/sysop-ui";
import { ArrowLeft, Clock, FileText, Hash, RefreshCw, User } from "lucide-react";
import { useCallback, useState } from "react";
import { getHead } from "../api/client";
import { CopyButton } from "../components/ui/CopyButton";
import { JsonViewer } from "../components/ui/JsonViewer";
import { MarkdownViewer } from "../components/ui/MarkdownViewer";
import { Spinner } from "../components/ui/Spinner";
import { usePoll } from "../hooks/usePoll";

interface Props {
  namespace: string;
  recordKey: string;
  onBack: () => void;
  onOpenHistory: (namespace: string, key: string) => void;
}

type Tab = "document" | "json";

interface RawPccPayload extends Record<string, unknown> {
  content?: unknown;
  format?: unknown;
  pcc_file?: unknown;
  project?: unknown;
  synced_at?: unknown;
  word_count?: unknown;
}

/** Detect if payload is a PCC markdown record and extract its fields. */
function parsePccPayload(payload: unknown): {
  isPcc: boolean;
  content: string;
  project: string;
  pccFile: string;
  format: string;
  wordCount: number;
  syncedAt: string;
} | null {
  if (!payload || typeof payload !== "object") return null;
  const parsed = payload as RawPccPayload;
  if (parsed.format === "pcc-markdown-v1" && typeof parsed.content === "string") {
    let markdown = parsed.content as string;
    const frontmatter = markdown.match(/^---\r?\n[\s\S]*?\r?\n---\r?\n?/);
    if (frontmatter) markdown = markdown.slice(frontmatter[0].length);
    return {
      isPcc: true,
      content: markdown,
      project: (parsed.project as string) || "",
      pccFile: (parsed.pcc_file as string) || "",
      format: parsed.format as string,
      wordCount: (parsed.word_count as number) || 0,
      syncedAt: (parsed.synced_at as string) || "",
    };
  }
  return null;
}

export function RecordDetailPage({ namespace, recordKey, onBack, onOpenHistory }: Props) {
  const [activeTab, setActiveTab] = useState<Tab>("document");
  const fetcher = useCallback(() => getHead(namespace, recordKey), [namespace, recordKey]);
  const { data, loading, error, refresh } = usePoll(fetcher, 10_000);
  const record = data?.record;
  const pcc = record ? parsePccPayload(record.payload) : null;

  return (
    <div className="min-h-full bg-bg text-text">
      <nav
        className="flex items-center gap-2 border-b border-border-soft px-4 py-2 text-xs text-text-subtle"
        aria-label="Breadcrumb"
      >
        <Button type="button" variant="ghost" size="xs" onClick={onBack}>
          <ArrowLeft aria-hidden="true" />
          Namespace
        </Button>
        <span aria-hidden="true">/</span>
        <span className="truncate font-mono text-text-soft">{recordKey}</span>
      </nav>

      <section className="border-b border-border-strong px-4 py-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2">
            <h2 className="truncate font-mono text-lg font-semibold tracking-tight">{recordKey}</h2>
            <CopyButton text={recordKey} />
          </div>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onOpenHistory(namespace, recordKey)}
            >
              History
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={refresh} disabled={loading}>
              {loading ? <Spinner size={14} /> : <RefreshCw aria-hidden="true" />}
              Refresh
            </Button>
          </div>
        </div>
        <p className="mt-1 break-all font-mono text-xs text-text-subtle">{namespace}</p>
      </section>

      <div className="space-y-4 p-4">
        {error ? (
          <Callout tone="danger" title="Record unavailable">
            {error.message}
          </Callout>
        ) : null}

        {loading && !record ? (
          <div className="flex justify-center py-12 text-text-subtle">
            <Spinner size={20} />
          </div>
        ) : null}

        {record ? (
          <>
            <section
              className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3"
              aria-label="Record metadata"
            >
              <MetaCard
                icon={<Hash aria-hidden="true" />}
                label="Record ID"
                value={record.record_id}
                copyable
              />
              <MetaCard
                icon={<FileText aria-hidden="true" />}
                label="Namespace"
                value={record.namespace}
                copyable
              />
              <MetaCard label="Revision" value={`r${record.revision}`} />
              <MetaCard icon={<User aria-hidden="true" />} label="Actor" value={record.actor} />
              <MetaCard
                icon={<Clock aria-hidden="true" />}
                label="Created"
                value={new Date(record.created_at).toLocaleString()}
              />
              <MetaCard label="Checksum" value={record.checksum} copyable />
            </section>

            <Tabs
              value={activeTab}
              onValueChange={(value) => setActiveTab(value as Tab)}
              className="space-y-3"
            >
              <TabsList variant="line" aria-label="Record representation">
                <TabsTrigger value="document">Document</TabsTrigger>
                <TabsTrigger value="json">JSON</TabsTrigger>
              </TabsList>

              <TabsContent value="document">
                <Card size="sm">
                  <CardContent>
                    {pcc ? (
                      <>
                        <section
                          className="mb-4 flex flex-wrap gap-1.5"
                          aria-labelledby="document-metadata-title"
                        >
                          <h3 id="document-metadata-title" className="sr-only">
                            Document metadata
                          </h3>
                          <Pill tone="info">{pcc.format}</Pill>
                          {pcc.project ? <Pill>project: {pcc.project}</Pill> : null}
                          {pcc.pccFile ? <Pill>{pcc.pccFile}</Pill> : null}
                          <Pill>{pcc.wordCount} words</Pill>
                          {pcc.syncedAt ? (
                            <Pill>synced {new Date(pcc.syncedAt).toLocaleString()}</Pill>
                          ) : null}
                        </section>
                        <MarkdownViewer content={pcc.content} maxHeight="600px" />
                      </>
                    ) : (
                      <PayloadDocument payload={record.payload} />
                    )}
                  </CardContent>
                </Card>
              </TabsContent>

              <TabsContent value="json">
                <div className="space-y-4">
                  <section aria-labelledby="record-payload-title">
                    <div className="mb-2 flex items-center justify-between gap-3">
                      <h3 id="record-payload-title" className="text-sm font-medium">
                        Payload
                      </h3>
                      <CopyButton text={JSON.stringify(record.payload, null, 2)} />
                    </div>
                    <JsonViewer data={record.payload} maxHeight="600px" />
                  </section>

                  {record.metadata != null ? (
                    <section aria-labelledby="record-metadata-title">
                      <h3 id="record-metadata-title" className="mb-2 text-sm font-medium">
                        Metadata
                      </h3>
                      <JsonViewer data={record.metadata} maxHeight="200px" />
                    </section>
                  ) : null}
                </div>
              </TabsContent>
            </Tabs>
          </>
        ) : null}
      </div>
    </div>
  );
}

function PayloadDocument({ payload }: { payload: unknown }) {
  if (payload === null || payload === undefined) {
    return <span className="text-text-subtle">null</span>;
  }

  if (typeof payload === "string") {
    return <pre className="m-0 whitespace-pre-wrap break-words text-text">{payload}</pre>;
  }

  if (typeof payload !== "object") {
    return <span className="text-text">{String(payload)}</span>;
  }

  return (
    <dl className="divide-y divide-border-soft">
      {Object.entries(payload as Record<string, unknown>).map(([key, value]) => (
        <div key={key} className="grid gap-2 py-3 first:pt-0 last:pb-0 sm:grid-cols-[10rem_1fr]">
          <dt className="break-all font-mono text-xs font-medium text-text-subtle">{key}</dt>
          <dd className="min-w-0 text-sm text-text-soft">
            {typeof value === "object" && value !== null ? (
              <JsonViewer data={value} maxHeight="200px" />
            ) : typeof value === "string" && value.length > 120 ? (
              <pre className="m-0 whitespace-pre-wrap break-words text-sm">{value}</pre>
            ) : (
              <span>{value === null ? "null" : String(value)}</span>
            )}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function MetaCard({
  icon,
  label,
  value,
  copyable,
}: {
  icon?: React.ReactNode;
  label: string;
  value: string;
  copyable?: boolean;
}) {
  return (
    <Card size="sm">
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-xs font-medium text-text-subtle [&_svg]:size-3.5">
          {icon}
          {label}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex min-w-0 items-center gap-2 pt-0">
        <span className="min-w-0 flex-1 break-all font-mono text-xs text-text">{value}</span>
        {copyable ? <CopyButton text={value} size={12} /> : null}
      </CardContent>
    </Card>
  );
}
