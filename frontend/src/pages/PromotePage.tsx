import { useState, useCallback, useMemo } from 'react';
import { ArrowLeft, ArrowRight, Check, Play } from 'lucide-react';
import { toast } from 'sonner';
import { promoteRequest, promoteApprove, promoteApply, getAuditEvents } from '../api/client';
import { usePoll } from '../hooks/usePoll';
import { Spinner } from '../components/ui/Spinner';
import { StatusBadge } from '../components/ui/StatusBadge';
import { EmptyState } from '../components/ui/EmptyState';
import type { AuditEvent } from '../api/types';

interface Props {
  onBack: () => void;
}

type Tab = 'request' | 'dashboard';

export function PromotePage({ onBack }: Props) {
  const [tab, setTab] = useState<Tab>('request');

  return (
    <div>
      <div className="breadcrumbs">
        <button onClick={onBack}><ArrowLeft size={12} /> Write & Promote</button>
        <span style={{ color: 'rgb(var(--muted))' }}>/</span>
        <span>Promote</span>
      </div>

      <div className="page-header">
        <h2 className="page-title">Promote</h2>
      </div>

      {/* Tab bar */}
      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem' }}>
        <button
          className={tab === 'request' ? 'hud-button-primary' : 'hud-button-ghost'}
          onClick={() => setTab('request')}
        >
          New Request
        </button>
        <button
          className={tab === 'dashboard' ? 'hud-button-primary' : 'hud-button-ghost'}
          onClick={() => setTab('dashboard')}
        >
          Promotion Log
        </button>
      </div>

      {tab === 'request' && <PromoteRequestForm />}
      {tab === 'dashboard' && <PromotionDashboard />}
    </div>
  );
}

function PromoteRequestForm() {
  const [srcNamespace, setSrcNamespace] = useState('');
  const [srcKey, setSrcKey] = useState('');
  const [tgtNamespace, setTgtNamespace] = useState('');
  const [tgtKey, setTgtKey] = useState('');
  const [actor, setActor] = useState('');
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<unknown>(null);

  const canSubmit = srcNamespace.trim() && srcKey.trim() && tgtNamespace.trim() && tgtKey.trim() && actor.trim() && !submitting;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    setResult(null);
    try {
      const res = await promoteRequest({
        actor: actor.trim(),
        source_namespace: srcNamespace.trim(),
        source_key: srcKey.trim(),
        target_namespace: tgtNamespace.trim(),
        target_key: tgtKey.trim(),
        reason: reason.trim() || undefined,
      });
      setResult(res);
      toast.success('Promotion requested');
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Request failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="hud-panel" style={{ padding: '1rem', maxWidth: 700 }}>
      {error && (
        <div style={{ padding: '0.5rem 0.75rem', marginBottom: '0.75rem', background: 'rgba(var(--danger) / 0.1)', borderRadius: 'var(--radius-sm)', color: 'rgb(var(--danger))', fontSize: '0.85rem' }}>
          {error}
        </div>
      )}

      {result != null && (
        <div style={{ padding: '0.5rem 0.75rem', marginBottom: '0.75rem', background: 'rgba(var(--ok) / 0.1)', borderRadius: 'var(--radius-sm)', color: 'rgb(var(--ok))', fontSize: '0.85rem' }}>
          Promotion request created successfully. Check the Promotion Log to approve and apply.
        </div>
      )}

      {/* Source */}
      <div className="hud-label" style={{ marginBottom: '0.5rem', color: 'rgb(var(--primary))' }}>Source</div>
      <div className="form-grid">
        <div className="form-field">
          <label className="hud-label">Namespace *</label>
          <input className="hud-input" placeholder="app/test/session" value={srcNamespace} onChange={e => setSrcNamespace(e.target.value)} style={{ width: '100%' }} />
        </div>
        <div className="form-field">
          <label className="hud-label">Key *</label>
          <input className="hud-input" placeholder="status" value={srcKey} onChange={e => setSrcKey(e.target.value)} style={{ width: '100%' }} />
        </div>
      </div>

      {/* Arrow */}
      <div style={{ textAlign: 'center', padding: '0.25rem 0', color: 'rgb(var(--muted))' }}>
        <ArrowRight size={20} />
      </div>

      {/* Target */}
      <div className="hud-label" style={{ marginBottom: '0.5rem', color: 'rgb(var(--primary))' }}>Target</div>
      <div className="form-grid">
        <div className="form-field">
          <label className="hud-label">Namespace *</label>
          <input className="hud-input" placeholder="user/memory/project" value={tgtNamespace} onChange={e => setTgtNamespace(e.target.value)} style={{ width: '100%' }} />
        </div>
        <div className="form-field">
          <label className="hud-label">Key *</label>
          <input className="hud-input" placeholder="status" value={tgtKey} onChange={e => setTgtKey(e.target.value)} style={{ width: '100%' }} />
        </div>
      </div>

      <div className="form-grid" style={{ marginTop: '0.5rem' }}>
        <div className="form-field">
          <label className="hud-label">Actor *</label>
          <input className="hud-input" placeholder="user:jane" value={actor} onChange={e => setActor(e.target.value)} style={{ width: '100%' }} />
        </div>
        <div className="form-field">
          <label className="hud-label">Reason <span style={{ color: 'rgb(var(--muted))' }}>(optional)</span></label>
          <input className="hud-input" placeholder="Promote to user memory" value={reason} onChange={e => setReason(e.target.value)} style={{ width: '100%' }} />
        </div>
      </div>

      <div className="form-actions">
        <button className="hud-button-primary" onClick={handleSubmit} disabled={!canSubmit}>
          <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            {submitting ? <Spinner size={13} /> : <ArrowRight size={13} />}
            Request Promotion
          </span>
        </button>
      </div>
    </div>
  );
}

function PromotionDashboard() {
  const fetcher = useCallback(
    () => getAuditEvents({ event_type: 'promote', limit: 50 }),
    [],
  );
  const { data, loading, error, refresh } = usePoll(fetcher, 10_000);

  // Also fetch promote.request / promote.approve events
  const reqFetcher = useCallback(
    () => getAuditEvents({ event_type: 'promote.request', limit: 50 }),
    [],
  );
  const { data: reqData } = usePoll(reqFetcher, 10_000);

  const allEvents = useMemo(() => {
    const events: AuditEvent[] = [];
    if (data?.items) events.push(...data.items);
    if (reqData?.items) events.push(...reqData.items);
    return events.sort((a, b) => b.created_at.localeCompare(a.created_at));
  }, [data, reqData]);

  const handleApprove = async (requestId: string) => {
    try {
      await promoteApprove({ request_id: requestId, actor: 'ui-user' });
      toast.success('Promotion approved');
      refresh();
    } catch (err) {
      toast.error(`Approve failed: ${err instanceof Error ? err.message : err}`);
    }
  };

  const handleApply = async (requestId: string) => {
    try {
      await promoteApply({ request_id: requestId, actor: 'ui-user' });
      toast.success('Promotion applied');
      refresh();
    } catch (err) {
      toast.error(`Apply failed: ${err instanceof Error ? err.message : err}`);
    }
  };

  return (
    <div>
      <div style={{ marginBottom: '0.5rem', display: 'flex', justifyContent: 'flex-end' }}>
        <button className="hud-button-ghost" onClick={refresh} disabled={loading}>
          {loading ? <Spinner size={12} /> : 'Refresh'}
        </button>
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

        {!loading && allEvents.length === 0 && (
          <EmptyState message="No promotion events" sub="Request a promotion to get started" />
        )}

        {allEvents.length > 0 && (
          <table className="hud-table">
            <thead>
              <tr>
                <th>Type</th>
                <th>Namespace</th>
                <th>Key</th>
                <th>Actor</th>
                <th>Time</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {allEvents.map(evt => {
                const status = evt.event_type.includes('apply') ? 'applied'
                  : evt.event_type.includes('approve') ? 'approved'
                  : evt.event_type.includes('request') ? 'pending'
                  : 'success';
                return (
                  <tr key={evt.id} style={{ cursor: 'default' }}>
                    <td><StatusBadge status={status} /></td>
                    <td style={{ fontSize: '0.8rem' }}>{evt.namespace}</td>
                    <td style={{ fontSize: '0.8rem' }}>{evt.key}</td>
                    <td style={{ color: 'rgb(var(--muted))' }}>{evt.actor}</td>
                    <td style={{ color: 'rgb(var(--muted))', fontSize: '0.8rem' }}>
                      {new Date(evt.created_at).toLocaleString()}
                    </td>
                    <td>
                      <div style={{ display: 'flex', gap: '0.3rem' }}>
                        {status === 'pending' && evt.record_id && (
                          <button
                            className="hud-button-ghost"
                            onClick={() => handleApprove(evt.record_id)}
                            style={{ padding: '0.15rem 0.4rem', fontSize: '0.65rem' }}
                          >
                            <Check size={11} /> Approve
                          </button>
                        )}
                        {status === 'approved' && evt.record_id && (
                          <button
                            className="hud-button-primary"
                            onClick={() => handleApply(evt.record_id)}
                            style={{ padding: '0.15rem 0.4rem', fontSize: '0.65rem' }}
                          >
                            <Play size={11} /> Apply
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
