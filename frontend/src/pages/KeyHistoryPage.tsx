import { useState, useCallback } from 'react';
import { ArrowLeft, GitCommit, ArrowRightLeft } from 'lucide-react';
import { usePoll } from '../hooks/usePoll';
import { getHistory } from '../api/client';
import { Spinner } from '../components/ui/Spinner';
import { EmptyState } from '../components/ui/EmptyState';
import { CopyButton } from '../components/ui/CopyButton';
import type { Record } from '../api/types';

interface Props {
  namespace: string;
  recordKey: string;
  onBack: () => void;
  onCompare: (namespace: string, key: string, revA: number, revB: number) => void;
}

export function KeyHistoryPage({ namespace, recordKey, onBack, onCompare }: Props) {
  const [selected, setSelected] = useState<Set<number>>(new Set());

  const fetcher = useCallback(
    () => getHistory(namespace, recordKey, 100),
    [namespace, recordKey],
  );
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);

  const items: Record[] = data?.items ?? [];

  const toggleSelect = (rev: number) => {
    setSelected(prev => {
      const next = new Set(prev);
      if (next.has(rev)) {
        next.delete(rev);
      } else {
        if (next.size >= 2) {
          // Replace oldest selection
          const [first] = next;
          next.delete(first);
        }
        next.add(rev);
      }
      return next;
    });
  };

  const canCompare = selected.size === 2;
  const handleCompare = () => {
    const [a, b] = Array.from(selected).sort((x, y) => x - y);
    onCompare(namespace, recordKey, a, b);
  };

  return (
    <div>
      <div className="breadcrumbs">
        <button onClick={onBack}><ArrowLeft size={12} /> Record</button>
        <span style={{ color: 'rgb(var(--muted))' }}>/</span>
        <span>History</span>
      </div>

      <div className="page-header">
        <h2 className="page-title">History: {recordKey}</h2>
        <div className="page-actions">
          {canCompare && (
            <button className="hud-button-primary" onClick={handleCompare}>
              <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
                <ArrowRightLeft size={13} /> Compare Selected
              </span>
            </button>
          )}
          <button className="hud-button-ghost" onClick={refresh} disabled={loading}>
            {loading ? <Spinner size={12} /> : 'Refresh'}
          </button>
        </div>
      </div>

      <div style={{ fontSize: '0.75rem', color: 'rgb(var(--muted))', marginBottom: '0.75rem' }}>
        {namespace} · {items.length} revision{items.length !== 1 ? 's' : ''}
        {selected.size > 0 && (
          <span style={{ color: 'rgb(var(--primary))' }}>
            {' '}· {selected.size} selected for comparison
          </span>
        )}
      </div>

      {error && (
        <div className="hud-panel" style={{ padding: '0.75rem', color: 'rgb(var(--danger))', marginBottom: '0.75rem' }}>
          Error: {error.message}
        </div>
      )}

      <div className="hud-panel">
        {loading && !data && (
          <div style={{ padding: '2rem', textAlign: 'center' }}><Spinner size={20} /></div>
        )}

        {!loading && items.length === 0 && (
          <EmptyState message="No revisions found" />
        )}

        {items.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column' }}>
            {items.map((item, idx) => {
              const isSelected = selected.has(item.revision);
              const isLatest = idx === 0;
              return (
                <div
                  key={item.revision}
                  onClick={() => toggleSelect(item.revision)}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '0.75rem',
                    padding: '0.6rem 0.75rem',
                    borderBottom: idx < items.length - 1 ? '1px solid rgba(var(--border) / 0.5)' : 'none',
                    cursor: 'pointer',
                    background: isSelected ? 'rgba(var(--primary) / 0.08)' : 'transparent',
                    transition: 'background 0.1s',
                  }}
                  onMouseEnter={e => {
                    if (!isSelected) e.currentTarget.style.background = 'rgba(var(--panel2) / 0.6)';
                  }}
                  onMouseLeave={e => {
                    if (!isSelected) e.currentTarget.style.background = 'transparent';
                  }}
                >
                  {/* Selection checkbox */}
                  <div style={{
                    width: 16,
                    height: 16,
                    borderRadius: 3,
                    border: isSelected
                      ? '2px solid rgb(var(--primary))'
                      : '2px solid rgb(var(--border))',
                    background: isSelected ? 'rgba(var(--primary) / 0.2)' : 'transparent',
                    flexShrink: 0,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}>
                    {isSelected && (
                      <div style={{
                        width: 8,
                        height: 8,
                        borderRadius: 2,
                        background: 'rgb(var(--primary))',
                      }} />
                    )}
                  </div>

                  {/* Timeline dot */}
                  <GitCommit size={14} style={{ color: isLatest ? 'rgb(var(--primary))' : 'rgb(var(--muted))' }} />

                  {/* Revision info */}
                  <span style={{
                    fontWeight: isLatest ? 600 : 400,
                    color: isLatest ? 'rgb(var(--primary))' : 'rgb(var(--text))',
                    minWidth: 40,
                  }}>
                    r{item.revision}
                  </span>

                  <span style={{ flex: 1, fontSize: '0.8rem', color: 'rgb(var(--muted))' }}>
                    {item.actor}
                  </span>

                  <span style={{ fontSize: '0.75rem', color: 'rgb(var(--muted))' }}>
                    {item.checksum?.slice(0, 12)}
                  </span>
                  <CopyButton text={item.checksum} size={11} />

                  <span style={{ fontSize: '0.75rem', color: 'rgb(var(--muted))', minWidth: 140, textAlign: 'right' }}>
                    {new Date(item.created_at).toLocaleString()}
                  </span>

                  {isLatest && (
                    <span className="hud-badge hud-badge-primary">HEAD</span>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
