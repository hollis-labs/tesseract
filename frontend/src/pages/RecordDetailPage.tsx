import { useCallback, useState } from 'react';
import { ArrowLeft, Clock, Hash, User, FileText } from 'lucide-react';
import { usePoll } from '../hooks/usePoll';
import { getHead } from '../api/client';
import { Spinner } from '../components/ui/Spinner';
import { CopyButton } from '../components/ui/CopyButton';
import { JsonViewer } from '../components/ui/JsonViewer';
import { MarkdownViewer } from '../components/ui/MarkdownViewer';

interface Props {
  namespace: string;
  recordKey: string;
  onBack: () => void;
  onOpenHistory: (namespace: string, key: string) => void;
}

type Tab = 'document' | 'json';

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
  if (!payload || typeof payload !== 'object') return null;
  const p = payload as Record<string, unknown>;
  if (p.format === 'pcc-markdown-v1' && typeof p.content === 'string') {
    // Strip YAML frontmatter (---\n...\n---) that TipTap would render as an h2
    let md = p.content as string;
    const fmMatch = md.match(/^---\r?\n[\s\S]*?\r?\n---\r?\n?/);
    if (fmMatch) md = md.slice(fmMatch[0].length);
    return {
      isPcc: true,
      content: md,
      project: (p.project as string) || '',
      pccFile: (p.pcc_file as string) || '',
      format: p.format as string,
      wordCount: (p.word_count as number) || 0,
      syncedAt: (p.synced_at as string) || '',
    };
  }
  return null;
}

export function RecordDetailPage({ namespace, recordKey, onBack, onOpenHistory }: Props) {
  const [activeTab, setActiveTab] = useState<Tab>('document');

  const fetcher = useCallback(
    () => getHead(namespace, recordKey),
    [namespace, recordKey],
  );
  const { data, loading, error, refresh } = usePoll(fetcher, 10_000);

  const record = data?.record;
  const pcc = record ? parsePccPayload(record.payload) : null;

  return (
    <div>
      <div className="breadcrumbs">
        <button onClick={onBack}><ArrowLeft size={12} /> Namespace</button>
        <span style={{ color: 'rgb(var(--muted))' }}>/</span>
        <span>{recordKey}</span>
      </div>

      <div className="page-header">
        <h2 className="page-title">{recordKey}</h2>
        <CopyButton text={recordKey} />
        <div className="page-actions">
          <button
            className="hud-button-ghost"
            onClick={() => onOpenHistory(namespace, recordKey)}
          >
            History
          </button>
          <button className="hud-button-ghost" onClick={refresh} disabled={loading}>
            {loading ? <Spinner size={12} /> : 'Refresh'}
          </button>
        </div>
      </div>

      {error && (
        <div className="hud-panel" style={{ padding: '0.75rem', color: 'rgb(var(--danger))', marginBottom: '0.75rem' }}>
          Error: {error.message}
        </div>
      )}

      {loading && !record && (
        <div style={{ padding: '2rem', textAlign: 'center' }}><Spinner size={20} /></div>
      )}

      {record && (
        <>
          {/* Metadata grid */}
          <div className="stats-grid" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))' }}>
            <MetaCard icon={<Hash size={13} />} label="Record ID" value={record.record_id} copyable />
            <MetaCard icon={<FileText size={13} />} label="Namespace" value={record.namespace} copyable />
            <MetaCard label="Revision" value={`r${record.revision}`} />
            <MetaCard icon={<User size={13} />} label="Actor" value={record.actor} />
            <MetaCard icon={<Clock size={13} />} label="Created" value={new Date(record.created_at).toLocaleString()} />
            <MetaCard label="Checksum" value={record.checksum} copyable />
          </div>

          {/* Tabs */}
          <div className="record-tabs">
            <button
              className={`record-tab ${activeTab === 'document' ? 'active' : ''}`}
              onClick={() => setActiveTab('document')}
            >
              Document
            </button>
            <button
              className={`record-tab ${activeTab === 'json' ? 'active' : ''}`}
              onClick={() => setActiveTab('json')}
            >
              JSON
            </button>
          </div>

          {/* Tab content */}
          {activeTab === 'document' && (
            <div className="hud-panel" style={{ padding: '1rem' }}>
              {pcc ? (
                <>
                  {/* PCC document header tags */}
                  <div className="doc-meta-bar">
                    <span className="doc-meta-tag">{pcc.format}</span>
                    <span className="doc-meta-tag">project: {pcc.project}</span>
                    <span className="doc-meta-tag">{pcc.pccFile}</span>
                    <span className="doc-meta-tag">{pcc.wordCount} words</span>
                    {pcc.syncedAt && (
                      <span className="doc-meta-tag">
                        synced {new Date(pcc.syncedAt).toLocaleString()}
                      </span>
                    )}
                  </div>

                  {/* Rendered markdown */}
                  <MarkdownViewer content={pcc.content} maxHeight="600px" />
                </>
              ) : (
                /* Non-PCC payload: formatted key-value display */
                <PayloadDocument payload={record.payload} />
              )}
            </div>
          )}

          {activeTab === 'json' && (
            <div>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '0.4rem' }}>
                <span className="hud-label" style={{ marginBottom: 0 }}>Payload</span>
                <CopyButton text={JSON.stringify(record.payload, null, 2)} />
              </div>
              <JsonViewer data={record.payload} maxHeight="600px" />

              {record.metadata != null && (
                <div style={{ marginTop: '0.75rem' }}>
                  <span className="hud-label">Metadata</span>
                  <JsonViewer data={record.metadata} maxHeight="200px" />
                </div>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}

/** Renders a non-PCC payload as a formatted key-value document. */
function PayloadDocument({ payload }: { payload: unknown }) {
  if (payload === null || payload === undefined) {
    return <span style={{ color: 'rgb(var(--muted))' }}>null</span>;
  }

  if (typeof payload === 'string') {
    return <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', color: 'rgb(var(--text))', margin: 0 }}>{payload}</pre>;
  }

  if (typeof payload !== 'object') {
    return <span style={{ color: 'rgb(var(--text))' }}>{String(payload)}</span>;
  }

  const entries = Object.entries(payload as Record<string, unknown>);

  return (
    <div className="payload-doc">
      {entries.map(([key, value]) => (
        <div key={key} className="payload-doc-field">
          <div className="payload-doc-label">{key}</div>
          <div className="payload-doc-value">
            {typeof value === 'object' && value !== null ? (
              <JsonViewer data={value} maxHeight="200px" />
            ) : typeof value === 'string' && value.length > 120 ? (
              <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, fontSize: '0.85rem' }}>{value}</pre>
            ) : (
              <span>{value === null ? 'null' : String(value)}</span>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

function MetaCard({ icon, label, value, copyable }: {
  icon?: React.ReactNode;
  label: string;
  value: string;
  copyable?: boolean;
}) {
  return (
    <div className="stat-card">
      <div className="stat-label" style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
        {icon}
        {label}
      </div>
      <div style={{
        fontSize: '0.85rem',
        color: 'rgb(var(--text))',
        wordBreak: 'break-all',
        display: 'flex',
        alignItems: 'center',
        gap: '0.3rem',
      }}>
        <span style={{ flex: 1 }}>{value}</span>
        {copyable && <CopyButton text={value} size={12} />}
      </div>
    </div>
  );
}
