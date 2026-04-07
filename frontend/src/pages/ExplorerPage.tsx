import { useState, useMemo, useCallback } from 'react';
import { Search, ChevronRight, ChevronDown, FileText, FolderOpen } from 'lucide-react';
import { usePoll } from '../hooks/usePoll';
import { evaluateView } from '../api/client';
import { Spinner } from '../components/ui/Spinner';
import { EmptyState } from '../components/ui/EmptyState';
import type { Record } from '../api/types';

interface Props {
  onOpenNamespace: (namespace: string) => void;
  onOpenRecord: (namespace: string, key: string) => void;
}

interface NamespaceGroup {
  namespace: string;
  keys: { key: string; revision: number; actor: string; created_at: string }[];
}

export function ExplorerPage({ onOpenNamespace, onOpenRecord }: Props) {
  const [search, setSearch] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const fetcher = useCallback(
    () => evaluateView({ revision_scope: 'head', limit: 500 }),
    [],
  );
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);

  // Group records by namespace
  const groups = useMemo<NamespaceGroup[]>(() => {
    if (!data?.items) return [];
    const map = new Map<string, NamespaceGroup>();
    for (const item of data.items) {
      let group = map.get(item.namespace);
      if (!group) {
        group = { namespace: item.namespace, keys: [] };
        map.set(item.namespace, group);
      }
      group.keys.push({
        key: item.key,
        revision: item.revision,
        actor: item.actor,
        created_at: item.created_at,
      });
    }
    return Array.from(map.values()).sort((a, b) => a.namespace.localeCompare(b.namespace));
  }, [data]);

  // Filter by search term
  const filtered = useMemo(() => {
    if (!search.trim()) return groups;
    const q = search.toLowerCase();
    return groups
      .map(g => ({
        ...g,
        keys: g.keys.filter(
          k => k.key.toLowerCase().includes(q) || g.namespace.toLowerCase().includes(q),
        ),
      }))
      .filter(g => g.keys.length > 0 || g.namespace.toLowerCase().includes(q));
  }, [groups, search]);

  const toggleExpand = (ns: string) => {
    setExpanded(prev => {
      const next = new Set(prev);
      if (next.has(ns)) next.delete(ns);
      else next.add(ns);
      return next;
    });
  };

  const totalRecords = data?.evaluation_meta?.matched_count ?? 0;

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Context Explorer</h2>
        <div className="page-actions">
          <button className="hud-button-ghost" onClick={refresh} disabled={loading}>
            {loading ? <Spinner size={12} /> : 'Refresh'}
          </button>
        </div>
      </div>

      {/* Search */}
      <div style={{ marginBottom: '0.75rem', display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
        <Search size={14} style={{ color: 'rgb(var(--muted))' }} />
        <input
          className="hud-input"
          placeholder="Filter namespaces and keys..."
          value={search}
          onChange={e => setSearch(e.target.value)}
          style={{ flex: 1 }}
        />
        <span style={{ fontSize: '0.75rem', color: 'rgb(var(--muted))' }}>
          {filtered.length} namespaces · {totalRecords} records
          {data?.evaluation_meta?.truncated && ' (truncated)'}
        </span>
      </div>

      {error && (
        <div className="hud-panel" style={{ padding: '0.75rem', color: 'rgb(var(--danger))', marginBottom: '0.75rem' }}>
          Error: {error.message}
        </div>
      )}

      {/* Namespace tree */}
      <div className="hud-panel" style={{ overflow: 'auto' }}>
        {loading && !data && (
          <div style={{ padding: '2rem', textAlign: 'center' }}>
            <Spinner size={20} />
          </div>
        )}

        {!loading && filtered.length === 0 && (
          <EmptyState message="No records found" sub={search ? 'Try a different search term' : 'Write some records to get started'} />
        )}

        {filtered.map(group => {
          const isExpanded = expanded.has(group.namespace);
          return (
            <div key={group.namespace} style={{ borderBottom: '1px solid rgba(var(--border) / 0.5)' }}>
              {/* Namespace row */}
              <div
                role="button"
                tabIndex={0}
                onClick={() => toggleExpand(group.namespace)}
                onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleExpand(group.namespace); } }}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '0.5rem',
                  width: '100%',
                  padding: '0.6rem 0.75rem',
                  background: 'none',
                  border: 'none',
                  color: 'rgb(var(--text))',
                  cursor: 'pointer',
                  fontFamily: 'var(--font-mono)',
                  fontSize: '0.85rem',
                  textAlign: 'left',
                  transition: 'background 0.1s',
                }}
                onMouseEnter={e => (e.currentTarget.style.background = 'rgba(var(--panel2) / 0.6)')}
                onMouseLeave={e => (e.currentTarget.style.background = 'none')}
              >
                {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <FolderOpen size={14} style={{ color: 'rgb(var(--primary))' }} />
                <span style={{ flex: 1 }}>{group.namespace}</span>
                <span style={{ fontSize: '0.7rem', color: 'rgb(var(--muted))' }}>
                  {group.keys.length} key{group.keys.length !== 1 ? 's' : ''}
                </span>
                <button
                  className="hud-button-ghost"
                  onClick={e => { e.stopPropagation(); onOpenNamespace(group.namespace); }}
                  style={{ padding: '0.15rem 0.4rem', fontSize: '0.65rem' }}
                >
                  Open
                </button>
              </div>

              {/* Expanded keys */}
              {isExpanded && (
                <div style={{ paddingLeft: '2rem', paddingBottom: '0.25rem' }}>
                  {group.keys.map(k => (
                    <button
                      key={k.key}
                      onClick={() => onOpenRecord(group.namespace, k.key)}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: '0.5rem',
                        width: '100%',
                        padding: '0.35rem 0.5rem',
                        background: 'none',
                        border: 'none',
                        color: 'rgb(var(--text))',
                        cursor: 'pointer',
                        fontFamily: 'var(--font-mono)',
                        fontSize: '0.8rem',
                        textAlign: 'left',
                        borderRadius: 'var(--radius-sm)',
                        transition: 'background 0.1s',
                      }}
                      onMouseEnter={e => (e.currentTarget.style.background = 'rgba(var(--panel2) / 0.6)')}
                      onMouseLeave={e => (e.currentTarget.style.background = 'none')}
                    >
                      <FileText size={13} style={{ color: 'rgb(var(--muted))' }} />
                      <span style={{ flex: 1 }}>{k.key}</span>
                      <span style={{ fontSize: '0.7rem', color: 'rgb(var(--muted))' }}>
                        r{k.revision}
                      </span>
                      <span style={{ fontSize: '0.65rem', color: 'rgb(var(--muted))' }}>
                        {k.actor}
                      </span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
