import { useCallback } from 'react';
import { Activity, Database, Layers, FileText, Search, PenTool, Eye } from 'lucide-react';
import { getHealth, estimate, getAuditEvents } from '../api/client';
import { usePoll } from '../hooks/usePoll';
import { Spinner } from '../components/ui/Spinner';
import { StatusBadge } from '../components/ui/StatusBadge';
import type { HealthStatus, EstimateResponse, AuditResponse } from '../api/types';
import type { NavPage } from '../components/layout/AppNav';

interface Props {
  health: HealthStatus | null;
  onNavigate: (page: NavPage) => void;
}

export function DashboardPage({ health, onNavigate }: Props) {
  // Estimate total records
  const estimateFetcher = useCallback(
    () => estimate({ revision_scope: 'head', limit: 1 }),
    [],
  );
  const { data: estData } = usePoll<EstimateResponse>(estimateFetcher, 15_000);

  // Recent audit events
  const auditFetcher = useCallback(
    () => getAuditEvents({ limit: 5 }),
    [],
  );
  const { data: auditData, loading: auditLoading } = usePoll<AuditResponse>(auditFetcher, 10_000);

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Dashboard</h2>
      </div>

      {/* Health & Stats */}
      <div className="stats-grid" style={{ marginBottom: '1rem' }}>
        <div className="stat-card">
          <div className="stat-label">Status</div>
          <div className="stat-value">
            {health ? (
              <span style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
                <span style={{
                  width: 8,
                  height: 8,
                  borderRadius: '50%',
                  background: health.status === 'ok' ? 'rgb(var(--ok))' : 'rgb(var(--danger))',
                  display: 'inline-block',
                }} />
                {health.status}
              </span>
            ) : (
              <Spinner size={16} />
            )}
          </div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Records</div>
          <div className="stat-value">{health?.record_count?.toLocaleString() ?? '—'}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Head Records</div>
          <div className="stat-value">{estData?.record_count?.toLocaleString() ?? '—'}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Est. Tokens</div>
          <div className="stat-value">{estData?.token_estimate?.toLocaleString() ?? '—'}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Schema</div>
          <div className="stat-value">v{health?.schema_version ?? '—'}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Consistency</div>
          <div className="stat-value" style={{
            color: health && health.consistency_issues > 0 ? 'rgb(var(--warn))' : undefined,
          }}>
            {health ? (
              health.consistency_issues === 0 ? 'OK' : `${health.consistency_issues} issues`
            ) : '—'}
          </div>
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem' }}>
        {/* Recent Activity */}
        <div className="hud-panel" style={{ padding: '0.75rem' }}>
          <div className="hud-label" style={{ color: 'rgb(var(--primary))', marginBottom: '0.5rem', display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            <Activity size={13} /> Recent Activity
          </div>

          {auditLoading && !auditData && (
            <div style={{ padding: '1rem', textAlign: 'center' }}><Spinner size={16} /></div>
          )}

          {auditData && auditData.items.length === 0 && (
            <div style={{ padding: '1rem', textAlign: 'center', color: 'rgb(var(--muted))', fontSize: '0.8rem' }}>
              No recent events
            </div>
          )}

          {auditData && auditData.items.length > 0 && (
            <div>
              {auditData.items.map(evt => (
                <div
                  key={evt.id}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '0.5rem',
                    padding: '0.35rem 0',
                    borderBottom: '1px solid rgba(var(--border) / 0.3)',
                    fontSize: '0.8rem',
                  }}
                >
                  <StatusBadge
                    status={evt.event_type}
                    variant={evt.event_type.includes('error') ? 'danger' : 'ok'}
                  />
                  <span style={{ fontFamily: 'var(--font-mono)', color: 'rgb(var(--muted))', fontSize: '0.75rem' }}>
                    {evt.namespace}
                  </span>
                  <span>{evt.key}</span>
                  <span style={{ marginLeft: 'auto', color: 'rgb(var(--muted))', fontSize: '0.7rem' }}>
                    {timeAgo(evt.created_at)}
                  </span>
                </div>
              ))}
              <button
                className="hud-button-ghost"
                onClick={() => onNavigate('audit')}
                style={{ width: '100%', marginTop: '0.5rem', fontSize: '0.75rem' }}
              >
                View All Events
              </button>
            </div>
          )}
        </div>

        {/* Quick Actions */}
        <div className="hud-panel" style={{ padding: '0.75rem' }}>
          <div className="hud-label" style={{ color: 'rgb(var(--primary))', marginBottom: '0.5rem' }}>
            Quick Actions
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.4rem' }}>
            <QuickAction icon={<Database size={14} />} label="Context Explorer" sub="Browse namespaces and records" onClick={() => onNavigate('explorer')} />
            <QuickAction icon={<PenTool size={14} />} label="Write Record" sub="Create or update a record" onClick={() => onNavigate('writeRecord')} />
            <QuickAction icon={<Eye size={14} />} label="View Builder" sub="Build and evaluate selectors" onClick={() => onNavigate('viewBuilder')} />
            <QuickAction icon={<Layers size={14} />} label="Packet Builder" sub="Budget-bounded context fetch" onClick={() => onNavigate('packetBuilder')} />
            <QuickAction icon={<Search size={14} />} label="Consistency Scan" sub="Check database health" onClick={() => onNavigate('consistency')} />
            <QuickAction icon={<FileText size={14} />} label="Audit Log" sub="View operation history" onClick={() => onNavigate('audit')} />
          </div>
        </div>
      </div>

      {/* DB Info */}
      {health && (
        <div className="hud-panel" style={{ padding: '0.75rem', marginTop: '0.75rem' }}>
          <div className="hud-label" style={{ marginBottom: '0.3rem' }}>Database</div>
          <div style={{ fontSize: '0.8rem', fontFamily: 'var(--font-mono)', color: 'rgb(var(--muted))' }}>
            {health.db_path}
          </div>
        </div>
      )}
    </div>
  );
}

function QuickAction({ icon, label, sub, onClick }: { icon: React.ReactNode; label: string; sub: string; onClick: () => void }) {
  return (
    <button
      className="hud-button-ghost"
      onClick={onClick}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '0.5rem',
        padding: '0.5rem',
        textAlign: 'left',
        width: '100%',
      }}
    >
      <span style={{ color: 'rgb(var(--primary))', flexShrink: 0 }}>{icon}</span>
      <div>
        <div style={{ fontSize: '0.8rem' }}>{label}</div>
        <div style={{ fontSize: '0.7rem', color: 'rgb(var(--muted))' }}>{sub}</div>
      </div>
    </button>
  );
}

function timeAgo(dateStr: string): string {
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diff = now - then;
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}
