import { useState } from 'react';
import { Search, Wrench, AlertTriangle, CheckCircle } from 'lucide-react';
import { toast } from 'sonner';
import { scanConsistency, repairConsistency } from '../api/client';
import { Spinner } from '../components/ui/Spinner';
import { EmptyState } from '../components/ui/EmptyState';
import { ConfirmModal } from '../components/ui/ConfirmModal';
import type { ConsistencyScanResponse, ConsistencyRepairResponse } from '../api/types';

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
      const res = await scanConsistency();
      setScanResult(res);
      if (res.count === 0) {
        toast.success('No consistency issues found');
      } else {
        toast.warning(`Found ${res.count} issue${res.count === 1 ? '' : 's'}`);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Scan failed: ${msg}`);
    } finally {
      setScanning(false);
    }
  };

  const handleRepair = async () => {
    setRepairing(true);
    setError(null);
    setShowRepairConfirm(false);
    try {
      const res = await repairConsistency();
      setRepairResult(res);
      setScanResult(null);
      toast.success(`Repair complete: ${res.rebuilt_heads} heads rebuilt`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Repair failed: ${msg}`);
    } finally {
      setRepairing(false);
    }
  };

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Consistency</h2>
      </div>

      {/* Actions */}
      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem' }}>
        <button className="hud-button-primary" onClick={handleScan} disabled={scanning || repairing}>
          <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            {scanning ? <Spinner size={13} /> : <Search size={13} />} Scan
          </span>
        </button>
        {scanResult && scanResult.count > 0 && (
          <button className="hud-button-danger" onClick={() => setShowRepairConfirm(true)} disabled={repairing}>
            <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
              {repairing ? <Spinner size={13} /> : <Wrench size={13} />} Repair
            </span>
          </button>
        )}
      </div>

      {error && (
        <div className="hud-panel" style={{ padding: '0.75rem', color: 'rgb(var(--danger))', marginBottom: '0.75rem' }}>
          {error}
        </div>
      )}

      {/* Repair results */}
      {repairResult && (
        <div className="hud-panel" style={{ padding: '1rem', marginBottom: '0.75rem' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.75rem' }}>
            <CheckCircle size={16} style={{ color: 'rgb(var(--ok))' }} />
            <span style={{ fontSize: '0.9rem' }}>Repair Complete</span>
          </div>
          <div className="stats-grid">
            <div className="stat-card">
              <div className="stat-label">Heads Rebuilt</div>
              <div className="stat-value">{repairResult.rebuilt_heads}</div>
            </div>
            <div className="stat-card">
              <div className="stat-label">Remaining Issues</div>
              <div className="stat-value" style={{ color: repairResult.remaining_issues > 0 ? 'rgb(var(--warn))' : 'rgb(var(--ok))' }}>
                {repairResult.remaining_issues}
              </div>
            </div>
          </div>
          {repairResult.issues.length > 0 && (
            <div style={{ marginTop: '0.75rem' }}>
              <div className="hud-label" style={{ marginBottom: '0.3rem', color: 'rgb(var(--warn))' }}>Remaining Issues</div>
              <IssueTable issues={repairResult.issues} />
            </div>
          )}
        </div>
      )}

      {/* Scan results */}
      {scanResult && (
        <div className="hud-panel" style={{ padding: '1rem' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.75rem' }}>
            {scanResult.count === 0 ? (
              <>
                <CheckCircle size={16} style={{ color: 'rgb(var(--ok))' }} />
                <span style={{ fontSize: '0.9rem', color: 'rgb(var(--ok))' }}>All checks passed</span>
              </>
            ) : (
              <>
                <AlertTriangle size={16} style={{ color: 'rgb(var(--warn))' }} />
                <span style={{ fontSize: '0.9rem', color: 'rgb(var(--warn))' }}>
                  {scanResult.count} issue{scanResult.count === 1 ? '' : 's'} found
                </span>
              </>
            )}
          </div>

          {scanResult.count === 0 && (
            <EmptyState message="No issues" sub="Database is consistent" />
          )}

          {scanResult.issues.length > 0 && (
            <IssueTable issues={scanResult.issues} />
          )}
        </div>
      )}

      {!scanResult && !repairResult && !error && (
        <div className="hud-panel">
          <EmptyState message="Run a scan" sub="Check database consistency and repair issues" />
        </div>
      )}

      {showRepairConfirm && (
        <ConfirmModal
          title="Repair Consistency Issues"
          message={`This will attempt to repair ${scanResult?.count ?? 0} consistency issue(s) by rebuilding head pointers. This modifies the database.`}
          confirmLabel="Repair"
          danger
          onConfirm={handleRepair}
          onCancel={() => setShowRepairConfirm(false)}
        />
      )}
    </div>
  );
}

function IssueTable({ issues }: { issues: { type: string; namespace: string; key: string; details: string }[] }) {
  return (
    <table className="hud-table">
      <thead>
        <tr>
          <th>Type</th>
          <th>Namespace</th>
          <th>Key</th>
          <th>Details</th>
        </tr>
      </thead>
      <tbody>
        {issues.map((issue, i) => (
          <tr key={i}>
            <td>
              <span className="hud-badge-warn" style={{ fontSize: '0.65rem' }}>{issue.type}</span>
            </td>
            <td style={{ fontSize: '0.8rem', fontFamily: 'var(--font-mono)' }}>{issue.namespace}</td>
            <td style={{ fontSize: '0.8rem' }}>{issue.key}</td>
            <td style={{ fontSize: '0.8rem', color: 'rgb(var(--muted))' }}>{issue.details}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
