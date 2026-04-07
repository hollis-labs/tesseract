import { useState, useEffect } from 'react';
import { ArrowLeft } from 'lucide-react';
import { getHistory } from '../api/client';
import { Spinner } from '../components/ui/Spinner';
import type { Record } from '../api/types';

interface Props {
  namespace: string;
  recordKey: string;
  revisionA: number;
  revisionB: number;
  onBack: () => void;
}

type DiffLine = { type: 'same' | 'added' | 'removed'; text: string };

export function CompareRevisionsPage({ namespace, recordKey, revisionA, revisionB, onBack }: Props) {
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [recA, setRecA] = useState<Record | null>(null);
  const [recB, setRecB] = useState<Record | null>(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    getHistory(namespace, recordKey, 200)
      .then(res => {
        const items = res.items ?? [];
        const a = items.find(r => r.revision === revisionA) ?? null;
        const b = items.find(r => r.revision === revisionB) ?? null;
        setRecA(a);
        setRecB(b);
        if (!a || !b) setError('One or both revisions not found');
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false));
  }, [namespace, recordKey, revisionA, revisionB]);

  const diffLines = recA && recB
    ? computeDiff(
        JSON.stringify(recA.payload, null, 2),
        JSON.stringify(recB.payload, null, 2),
      )
    : [];

  return (
    <div>
      <div className="breadcrumbs">
        <button onClick={onBack}><ArrowLeft size={12} /> History</button>
        <span style={{ color: 'rgb(var(--muted))' }}>/</span>
        <span>Compare r{revisionA} ↔ r{revisionB}</span>
      </div>

      <div className="page-header">
        <h2 className="page-title">
          Compare: r{revisionA} ↔ r{revisionB}
        </h2>
      </div>

      <div style={{ fontSize: '0.75rem', color: 'rgb(var(--muted))', marginBottom: '0.75rem' }}>
        {namespace} / {recordKey}
      </div>

      {error && (
        <div className="hud-panel" style={{ padding: '0.75rem', color: 'rgb(var(--danger))', marginBottom: '0.75rem' }}>
          {error}
        </div>
      )}

      {loading && (
        <div style={{ padding: '2rem', textAlign: 'center' }}><Spinner size={20} /></div>
      )}

      {!loading && recA && recB && (
        <>
          {/* Revision metadata comparison */}
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem', marginBottom: '1rem' }}>
            <RevisionCard label={`r${revisionA}`} record={recA} />
            <RevisionCard label={`r${revisionB}`} record={recB} />
          </div>

          {/* Diff view */}
          <div className="hud-label" style={{ marginBottom: '0.4rem' }}>Payload Diff</div>
          <div className="hud-panel" style={{ padding: 0, overflow: 'auto', maxHeight: '500px' }}>
            <pre style={{
              margin: 0,
              padding: '0.75rem',
              fontFamily: 'var(--font-mono)',
              fontSize: '0.8rem',
              lineHeight: 1.6,
            }}>
              {diffLines.map((line, i) => (
                <div
                  key={i}
                  className={
                    line.type === 'added' ? 'diff-added' :
                    line.type === 'removed' ? 'diff-removed' : ''
                  }
                  style={{ padding: '0 0.25rem' }}
                >
                  <span style={{
                    display: 'inline-block',
                    width: '1.5em',
                    color: line.type === 'added' ? 'rgb(var(--ok))' :
                           line.type === 'removed' ? 'rgb(var(--danger))' :
                           'rgb(var(--muted))',
                    userSelect: 'none',
                  }}>
                    {line.type === 'added' ? '+' : line.type === 'removed' ? '-' : ' '}
                  </span>
                  {line.text}
                </div>
              ))}
              {diffLines.length === 0 && (
                <span style={{ color: 'rgb(var(--muted))' }}>No differences in payload</span>
              )}
            </pre>
          </div>
        </>
      )}
    </div>
  );
}

function RevisionCard({ label, record }: { label: string; record: Record }) {
  return (
    <div className="stat-card">
      <div style={{ fontSize: '0.9rem', fontWeight: 600, color: 'rgb(var(--primary))', marginBottom: '0.5rem' }}>
        {label}
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: '0.2rem', fontSize: '0.8rem' }}>
        <Row label="Actor" value={record.actor} />
        <Row label="Checksum" value={record.checksum?.slice(0, 16) + '...'} />
        <Row label="Created" value={new Date(record.created_at).toLocaleString()} />
      </div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: 'flex', gap: '0.5rem' }}>
      <span style={{ color: 'rgb(var(--muted))', minWidth: 70 }}>{label}</span>
      <span style={{ color: 'rgb(var(--text))' }}>{value}</span>
    </div>
  );
}

// Simple line-based diff of two JSON strings
function computeDiff(textA: string, textB: string): DiffLine[] {
  const linesA = textA.split('\n');
  const linesB = textB.split('\n');

  // LCS-based diff
  const n = linesA.length;
  const m = linesB.length;

  // For very large diffs, fall back to simple comparison
  if (n * m > 100_000) {
    return simpleDiff(linesA, linesB);
  }

  // Build LCS table
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = 1; i <= n; i++) {
    for (let j = 1; j <= m; j++) {
      if (linesA[i - 1] === linesB[j - 1]) {
        dp[i][j] = dp[i - 1][j - 1] + 1;
      } else {
        dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]);
      }
    }
  }

  // Backtrack to produce diff
  const result: DiffLine[] = [];
  let i = n, j = m;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && linesA[i - 1] === linesB[j - 1]) {
      result.push({ type: 'same', text: linesA[i - 1] });
      i--; j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      result.push({ type: 'added', text: linesB[j - 1] });
      j--;
    } else {
      result.push({ type: 'removed', text: linesA[i - 1] });
      i--;
    }
  }

  return result.reverse();
}

function simpleDiff(linesA: string[], linesB: string[]): DiffLine[] {
  const result: DiffLine[] = [];
  for (const line of linesA) result.push({ type: 'removed', text: line });
  for (const line of linesB) result.push({ type: 'added', text: line });
  return result;
}
