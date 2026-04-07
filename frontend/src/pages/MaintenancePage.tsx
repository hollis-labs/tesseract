import { useState } from 'react';
import { Scissors, Archive } from 'lucide-react';
import { toast } from 'sonner';
import { trimRecords, compactRecords } from '../api/client';
import { Spinner } from '../components/ui/Spinner';
import { ConfirmModal } from '../components/ui/ConfirmModal';
import type { TrimResponse, CompactResponse } from '../api/types';

type Tab = 'trim' | 'compact';

export function MaintenancePage() {
  const [tab, setTab] = useState<Tab>('trim');

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Maintenance</h2>
      </div>

      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem' }}>
        <button
          className={tab === 'trim' ? 'hud-button-primary' : 'hud-button-ghost'}
          onClick={() => setTab('trim')}
        >
          <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            <Scissors size={13} /> Trim
          </span>
        </button>
        <button
          className={tab === 'compact' ? 'hud-button-primary' : 'hud-button-ghost'}
          onClick={() => setTab('compact')}
        >
          <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            <Archive size={13} /> Compact
          </span>
        </button>
      </div>

      {tab === 'trim' && <TrimForm />}
      {tab === 'compact' && <CompactForm />}
    </div>
  );
}

function TrimForm() {
  const [nsPattern, setNsPattern] = useState('*');
  const [retention, setRetention] = useState('720h');
  const [dryRun, setDryRun] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<TrimResponse | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);

  const handleSubmit = async (dry: boolean) => {
    setSubmitting(true);
    setError(null);
    setResult(null);
    setShowConfirm(false);
    try {
      const res = await trimRecords({
        namespace_pattern: nsPattern.trim() || '*',
        retention: retention.trim(),
        dry_run: dry,
      });
      setResult(res);
      if (dry) {
        toast.success(`Dry run: ${res.trimmed} records would be trimmed`);
      } else {
        toast.success(`Trimmed ${res.trimmed} records`);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Trim failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="hud-panel" style={{ padding: '1rem', maxWidth: 700 }}>
      <div style={{ fontSize: '0.85rem', color: 'rgb(var(--muted))', marginBottom: '0.75rem' }}>
        Remove old revisions beyond the retention window. Use dry-run first to preview.
      </div>

      {error && (
        <div style={{ padding: '0.5rem 0.75rem', marginBottom: '0.75rem', background: 'rgba(var(--danger) / 0.1)', borderRadius: 'var(--radius-sm)', color: 'rgb(var(--danger))', fontSize: '0.85rem' }}>
          {error}
        </div>
      )}

      <div className="form-grid">
        <div className="form-field">
          <label className="hud-label">Namespace Pattern</label>
          <input className="hud-input" value={nsPattern} onChange={e => setNsPattern(e.target.value)} style={{ width: '100%' }} />
        </div>
        <div className="form-field">
          <label className="hud-label">Retention</label>
          <input className="hud-input" placeholder="720h" value={retention} onChange={e => setRetention(e.target.value)} style={{ width: '100%' }} />
        </div>
      </div>

      <div style={{ marginTop: '0.5rem' }}>
        <label style={{ display: 'flex', alignItems: 'center', gap: '0.4rem', fontSize: '0.8rem', cursor: 'pointer' }}>
          <input type="checkbox" checked={dryRun} onChange={e => setDryRun(e.target.checked)} />
          Dry run (preview only)
        </label>
      </div>

      <div style={{ display: 'flex', gap: '0.5rem', marginTop: '0.75rem' }}>
        <button className="hud-button" onClick={() => handleSubmit(true)} disabled={submitting}>
          <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            {submitting ? <Spinner size={12} /> : <Scissors size={13} />} Dry Run
          </span>
        </button>
        <button
          className="hud-button-danger"
          onClick={() => dryRun ? handleSubmit(true) : setShowConfirm(true)}
          disabled={submitting}
        >
          <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            <Scissors size={13} /> Trim Now
          </span>
        </button>
      </div>

      {result && <TrimResult result={result} />}

      {showConfirm && (
        <ConfirmModal
          title="Confirm Trim"
          message={`This will permanently delete old revisions matching "${nsPattern}" older than ${retention}. This cannot be undone.`}
          confirmLabel="Trim"
          danger
          onConfirm={() => handleSubmit(false)}
          onCancel={() => setShowConfirm(false)}
        />
      )}
    </div>
  );
}

function TrimResult({ result }: { result: TrimResponse }) {
  return (
    <div className="stats-grid" style={{ marginTop: '0.75rem' }}>
      <div className="stat-card">
        <div className="stat-label">{result.dry_run ? 'Would Trim' : 'Trimmed'}</div>
        <div className="stat-value">{result.trimmed}</div>
      </div>
      <div className="stat-card">
        <div className="stat-label">Pattern</div>
        <div className="stat-value" style={{ fontSize: '0.85rem', fontFamily: 'var(--font-mono)' }}>{result.namespace_pattern}</div>
      </div>
      <div className="stat-card">
        <div className="stat-label">Duration</div>
        <div className="stat-value">{result.duration_ms}ms</div>
      </div>
      {result.dry_run && (
        <div className="stat-card" style={{ borderColor: 'rgba(var(--warn) / 0.4)' }}>
          <div className="stat-label" style={{ color: 'rgb(var(--warn))' }}>Mode</div>
          <div className="stat-value" style={{ color: 'rgb(var(--warn))' }}>Dry Run</div>
        </div>
      )}
    </div>
  );
}

function CompactForm() {
  const [nsPattern, setNsPattern] = useState('*');
  const [maxRevisions, setMaxRevisions] = useState('10');
  const [dryRun, setDryRun] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<CompactResponse | null>(null);
  const [showConfirm, setShowConfirm] = useState(false);

  const handleSubmit = async (dry: boolean) => {
    setSubmitting(true);
    setError(null);
    setResult(null);
    setShowConfirm(false);
    try {
      const res = await compactRecords({
        namespace_pattern: nsPattern.trim() || '*',
        max_revisions: parseInt(maxRevisions) || 10,
        dry_run: dry,
      });
      setResult(res);
      if (dry) {
        toast.success(`Dry run: ${res.compacted} revisions would be compacted`);
      } else {
        toast.success(`Compacted ${res.compacted} revisions`);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Compact failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="hud-panel" style={{ padding: '1rem', maxWidth: 700 }}>
      <div style={{ fontSize: '0.85rem', color: 'rgb(var(--muted))', marginBottom: '0.75rem' }}>
        Reduce revision count per key to a maximum. Use dry-run first to preview.
      </div>

      {error && (
        <div style={{ padding: '0.5rem 0.75rem', marginBottom: '0.75rem', background: 'rgba(var(--danger) / 0.1)', borderRadius: 'var(--radius-sm)', color: 'rgb(var(--danger))', fontSize: '0.85rem' }}>
          {error}
        </div>
      )}

      <div className="form-grid">
        <div className="form-field">
          <label className="hud-label">Namespace Pattern</label>
          <input className="hud-input" value={nsPattern} onChange={e => setNsPattern(e.target.value)} style={{ width: '100%' }} />
        </div>
        <div className="form-field">
          <label className="hud-label">Max Revisions</label>
          <input className="hud-input" type="number" value={maxRevisions} onChange={e => setMaxRevisions(e.target.value)} style={{ width: '100%' }} />
        </div>
      </div>

      <div style={{ marginTop: '0.5rem' }}>
        <label style={{ display: 'flex', alignItems: 'center', gap: '0.4rem', fontSize: '0.8rem', cursor: 'pointer' }}>
          <input type="checkbox" checked={dryRun} onChange={e => setDryRun(e.target.checked)} />
          Dry run (preview only)
        </label>
      </div>

      <div style={{ display: 'flex', gap: '0.5rem', marginTop: '0.75rem' }}>
        <button className="hud-button" onClick={() => handleSubmit(true)} disabled={submitting}>
          <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            {submitting ? <Spinner size={12} /> : <Archive size={13} />} Dry Run
          </span>
        </button>
        <button
          className="hud-button-danger"
          onClick={() => dryRun ? handleSubmit(true) : setShowConfirm(true)}
          disabled={submitting}
        >
          <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            <Archive size={13} /> Compact Now
          </span>
        </button>
      </div>

      {result && (
        <div className="stats-grid" style={{ marginTop: '0.75rem' }}>
          <div className="stat-card">
            <div className="stat-label">{result.dry_run ? 'Would Compact' : 'Compacted'}</div>
            <div className="stat-value">{result.compacted}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Pattern</div>
            <div className="stat-value" style={{ fontSize: '0.85rem', fontFamily: 'var(--font-mono)' }}>{result.namespace_pattern}</div>
          </div>
          <div className="stat-card">
            <div className="stat-label">Duration</div>
            <div className="stat-value">{result.duration_ms}ms</div>
          </div>
          {result.dry_run && (
            <div className="stat-card" style={{ borderColor: 'rgba(var(--warn) / 0.4)' }}>
              <div className="stat-label" style={{ color: 'rgb(var(--warn))' }}>Mode</div>
              <div className="stat-value" style={{ color: 'rgb(var(--warn))' }}>Dry Run</div>
            </div>
          )}
        </div>
      )}

      {showConfirm && (
        <ConfirmModal
          title="Confirm Compact"
          message={`This will permanently remove excess revisions (keeping max ${maxRevisions}) for keys matching "${nsPattern}". This cannot be undone.`}
          confirmLabel="Compact"
          danger
          onConfirm={() => handleSubmit(false)}
          onCancel={() => setShowConfirm(false)}
        />
      )}
    </div>
  );
}
