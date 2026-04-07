import { useState, useCallback, useMemo, Fragment } from 'react';
import { FileText, ChevronDown, ChevronRight } from 'lucide-react';
import { getAuditEvents } from '../api/client';
import { usePoll } from '../hooks/usePoll';
import { Spinner } from '../components/ui/Spinner';
import { StatusBadge } from '../components/ui/StatusBadge';
import { EmptyState } from '../components/ui/EmptyState';
import { JsonViewer } from '../components/ui/JsonViewer';
import type { AuditResponse } from '../api/types';

const EVENT_TYPES = [
  { value: '', label: 'All Events' },
  { value: 'write', label: 'write' },
  { value: 'promote', label: 'promote' },
  { value: 'promote.request', label: 'promote.request' },
  { value: 'promote.approve', label: 'promote.approve' },
  { value: 'promote.apply', label: 'promote.apply' },
  { value: 'delete', label: 'delete' },
  { value: 'trim', label: 'trim' },
  { value: 'compact', label: 'compact' },
];

export function AuditPage() {
  const [eventType, setEventType] = useState('');
  const [nsFilter, setNsFilter] = useState('');
  const [limit, setLimit] = useState('50');
  const [expandedRow, setExpandedRow] = useState<number | null>(null);

  const fetcher = useCallback(
    (): Promise<AuditResponse> => getAuditEvents({
      event_type: eventType || undefined,
      namespace: nsFilter.trim() || undefined,
      limit: parseInt(limit) || 50,
    }),
    [eventType, nsFilter, limit],
  );
  const { data, loading, error, refresh } = usePoll(fetcher, 10_000);

  const events = useMemo(() => data?.items ?? [], [data]);

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Audit & Ops</h2>
      </div>

      {/* Filters */}
      <div className="hud-panel" style={{ padding: '0.75rem', marginBottom: '0.75rem' }}>
        <div className="form-grid">
          <div className="form-field">
            <label className="hud-label">Event Type</label>
            <select className="hud-input" value={eventType} onChange={e => setEventType(e.target.value)} style={{ width: '100%' }}>
              {EVENT_TYPES.map(t => (
                <option key={t.value} value={t.value}>{t.label}</option>
              ))}
            </select>
          </div>
          <div className="form-field">
            <label className="hud-label">Namespace</label>
            <input className="hud-input" placeholder="Filter by namespace..." value={nsFilter} onChange={e => setNsFilter(e.target.value)} style={{ width: '100%' }} />
          </div>
          <div className="form-field">
            <label className="hud-label">Limit</label>
            <input className="hud-input" type="number" value={limit} onChange={e => setLimit(e.target.value)} style={{ width: '100%' }} />
          </div>
        </div>
      </div>

      {/* Stats */}
      {data && (
        <div className="stats-grid" style={{ marginBottom: '0.75rem' }}>
          <div className="stat-card">
            <div className="stat-label">Events Shown</div>
            <div className="stat-value">{events.length}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Total Matched</div>
            <div className="stat-value">{data.count}</div>
          </div>
        </div>
      )}

      {error && (
        <div className="hud-panel" style={{ padding: '0.75rem', color: 'rgb(var(--danger))', marginBottom: '0.75rem' }}>
          Error: {error.message}
        </div>
      )}

      {/* Event table */}
      <div className="hud-panel">
        <div style={{ display: 'flex', justifyContent: 'flex-end', padding: '0.5rem 0.75rem' }}>
          <button className="hud-button-ghost" onClick={refresh} disabled={loading}>
            {loading ? <Spinner size={12} /> : 'Refresh'}
          </button>
        </div>

        {loading && !data && (
          <div style={{ padding: '2rem', textAlign: 'center' }}><Spinner size={20} /></div>
        )}

        {!loading && events.length === 0 && (
          <EmptyState message="No audit events" sub="Events will appear as operations occur" />
        )}

        {events.length > 0 && (
          <table className="hud-table">
            <thead>
              <tr>
                <th style={{ width: 30 }}></th>
                <th>Type</th>
                <th>Namespace</th>
                <th>Key</th>
                <th>Rev</th>
                <th>Actor</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {events.map(evt => {
                const isExpanded = expandedRow === evt.id;
                const badgeStatus = evt.event_type.includes('error') ? 'failed'
                  : evt.event_type.includes('promote') ? 'pending'
                  : 'success';
                return (
                  <Fragment key={evt.id}>
                    <tr
                      onClick={() => setExpandedRow(isExpanded ? null : evt.id)}
                      style={{ cursor: 'pointer' }}
                    >
                      <td>
                        {evt.metadata != null
                          ? (isExpanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />)
                          : <FileText size={12} style={{ color: 'rgb(var(--muted))' }} />
                        }
                      </td>
                      <td><StatusBadge status={evt.event_type} variant={badgeStatus === 'failed' ? 'danger' : badgeStatus === 'pending' ? 'warn' : 'ok'} /></td>
                      <td style={{ fontSize: '0.8rem', fontFamily: 'var(--font-mono)' }}>{evt.namespace}</td>
                      <td style={{ fontSize: '0.8rem' }}>{evt.key}</td>
                      <td style={{ color: 'rgb(var(--muted))', fontSize: '0.8rem' }}>r{evt.revision}</td>
                      <td style={{ color: 'rgb(var(--muted))' }}>{evt.actor}</td>
                      <td style={{ color: 'rgb(var(--muted))', fontSize: '0.8rem' }}>
                        {new Date(evt.created_at).toLocaleString()}
                      </td>
                    </tr>
                    {isExpanded && evt.metadata != null && (
                      <tr key={`${evt.id}-detail`}>
                        <td colSpan={7} style={{ padding: '0.5rem 0.75rem', background: 'rgba(var(--panel2) / 0.4)' }}>
                          <div className="hud-label" style={{ marginBottom: '0.3rem' }}>Metadata</div>
                          <JsonViewer data={evt.metadata} maxHeight="200px" />
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
