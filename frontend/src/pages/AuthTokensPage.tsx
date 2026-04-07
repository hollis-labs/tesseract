import { useState, useCallback } from 'react';
import { Key, Plus, Trash2, Copy, Check } from 'lucide-react';
import { toast } from 'sonner';
import { listTokens, createToken, revokeToken } from '../api/client';
import { usePoll } from '../hooks/usePoll';
import { Spinner } from '../components/ui/Spinner';
import { EmptyState } from '../components/ui/EmptyState';
import { ConfirmModal } from '../components/ui/ConfirmModal';
import type { AuthToken, TokenCreateResponse } from '../api/types';

type Tab = 'list' | 'create';

export function AuthTokensPage() {
  const [tab, setTab] = useState<Tab>('list');

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Auth & Tokens</h2>
      </div>

      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem' }}>
        <button
          className={tab === 'list' ? 'hud-button-primary' : 'hud-button-ghost'}
          onClick={() => setTab('list')}
        >
          Token List
        </button>
        <button
          className={tab === 'create' ? 'hud-button-primary' : 'hud-button-ghost'}
          onClick={() => setTab('create')}
        >
          <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            <Plus size={13} /> Create Token
          </span>
        </button>
      </div>

      {tab === 'list' && <TokenList />}
      {tab === 'create' && <TokenCreateForm onCreated={() => setTab('list')} />}
    </div>
  );
}

function TokenList() {
  const fetcher = useCallback(() => listTokens(), []);
  const { data, loading, error, refresh } = usePoll(fetcher, 15_000);
  const [revoking, setRevoking] = useState<string | null>(null);
  const [confirmRevoke, setConfirmRevoke] = useState<AuthToken | null>(null);

  const tokens = data?.tokens ?? [];

  const handleRevoke = async (token: AuthToken) => {
    setRevoking(token.id);
    try {
      await revokeToken(token.id);
      toast.success(`Token "${token.name}" revoked`);
      refresh();
    } catch (err) {
      toast.error(`Revoke failed: ${err instanceof Error ? err.message : err}`);
    } finally {
      setRevoking(null);
      setConfirmRevoke(null);
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

        {!loading && tokens.length === 0 && (
          <EmptyState message="No tokens" sub="Create a token to get started" />
        )}

        {tokens.length > 0 && (
          <table className="hud-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Client ID</th>
                <th>Scopes</th>
                <th>Namespaces</th>
                <th>Created</th>
                <th>Expires</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {tokens.map(t => (
                <tr key={t.id}>
                  <td style={{ fontSize: '0.85rem' }}>
                    <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
                      <Key size={12} style={{ color: 'rgb(var(--primary))' }} /> {t.name}
                    </span>
                  </td>
                  <td style={{ fontSize: '0.8rem', fontFamily: 'var(--font-mono)' }}>{t.client_id}</td>
                  <td>
                    <div style={{ display: 'flex', gap: '0.2rem', flexWrap: 'wrap' }}>
                      {t.scopes.map(s => (
                        <span key={s} className="hud-badge-info" style={{ fontSize: '0.6rem' }}>{s}</span>
                      ))}
                    </div>
                  </td>
                  <td style={{ fontSize: '0.75rem', fontFamily: 'var(--font-mono)', maxWidth: 150, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {t.namespace_globs.join(', ')}
                  </td>
                  <td style={{ color: 'rgb(var(--muted))', fontSize: '0.8rem' }}>
                    {new Date(t.created_at).toLocaleDateString()}
                  </td>
                  <td style={{ color: 'rgb(var(--muted))', fontSize: '0.8rem' }}>
                    {new Date(t.expires_at).toLocaleDateString()}
                  </td>
                  <td>
                    {t.revoked ? (
                      <span className="hud-badge-danger" style={{ fontSize: '0.65rem' }}>revoked</span>
                    ) : new Date(t.expires_at) < new Date() ? (
                      <span className="hud-badge-warn" style={{ fontSize: '0.65rem' }}>expired</span>
                    ) : (
                      <span className="hud-badge-ok" style={{ fontSize: '0.65rem' }}>active</span>
                    )}
                  </td>
                  <td>
                    {!t.revoked && (
                      <button
                        className="hud-button-ghost"
                        onClick={() => setConfirmRevoke(t)}
                        disabled={revoking === t.id}
                        style={{ padding: '0.15rem 0.4rem', fontSize: '0.65rem' }}
                      >
                        {revoking === t.id ? <Spinner size={10} /> : <Trash2 size={11} />} Revoke
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {confirmRevoke && (
        <ConfirmModal
          title="Revoke Token"
          message={`Are you sure you want to revoke token "${confirmRevoke.name}"? This cannot be undone.`}
          confirmLabel="Revoke"
          danger
          onConfirm={() => handleRevoke(confirmRevoke)}
          onCancel={() => setConfirmRevoke(null)}
        />
      )}
    </div>
  );
}

interface TokenCreateFormProps {
  onCreated: () => void;
}

const AVAILABLE_SCOPES = ['read', 'write', 'promote.request', 'promote.approve', 'promote.apply', 'admin'];

function TokenCreateForm({ onCreated }: TokenCreateFormProps) {
  const [name, setName] = useState('');
  const [clientId, setClientId] = useState('');
  const [scopes, setScopes] = useState<string[]>(['read']);
  const [nsGlobs, setNsGlobs] = useState('');
  const [ttl, setTtl] = useState('720h');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<TokenCreateResponse | null>(null);
  const [copied, setCopied] = useState(false);

  const canSubmit = name.trim() && clientId.trim() && scopes.length > 0 && !submitting && !result;

  const toggleScope = (scope: string) => {
    setScopes(prev =>
      prev.includes(scope) ? prev.filter(s => s !== scope) : [...prev, scope],
    );
  };

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      const res = await createToken({
        name: name.trim(),
        client_id: clientId.trim(),
        scopes,
        namespace_globs: nsGlobs.trim() ? nsGlobs.split(',').map(s => s.trim()).filter(Boolean) : ['*'],
        ttl: ttl.trim() || undefined,
      });
      setResult(res);
      toast.success('Token created — copy the value now!');
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Create failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  const handleCopy = async () => {
    if (!result) return;
    await navigator.clipboard.writeText(result.token);
    setCopied(true);
    toast.success('Token copied');
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="hud-panel" style={{ padding: '1rem', maxWidth: 700 }}>
      {error && (
        <div style={{ padding: '0.5rem 0.75rem', marginBottom: '0.75rem', background: 'rgba(var(--danger) / 0.1)', borderRadius: 'var(--radius-sm)', color: 'rgb(var(--danger))', fontSize: '0.85rem' }}>
          {error}
        </div>
      )}

      {result && (
        <div style={{ marginBottom: '1rem' }}>
          <div style={{ padding: '0.75rem', background: 'rgba(var(--ok) / 0.1)', border: '1px solid rgba(var(--ok) / 0.3)', borderRadius: 'var(--radius-sm)', marginBottom: '0.5rem' }}>
            <div className="hud-label" style={{ color: 'rgb(var(--ok))', marginBottom: '0.3rem' }}>
              Token Created — Copy this value now (it won't be shown again)
            </div>
            <div style={{
              display: 'flex',
              alignItems: 'center',
              gap: '0.5rem',
              background: 'rgb(var(--bg))',
              padding: '0.5rem',
              borderRadius: 'var(--radius-sm)',
              fontFamily: 'var(--font-mono)',
              fontSize: '0.8rem',
              wordBreak: 'break-all',
            }}>
              <span style={{ flex: 1 }}>{result.token}</span>
              <button className="hud-button-ghost" onClick={handleCopy} style={{ flexShrink: 0, padding: '0.2rem 0.4rem' }}>
                {copied ? <Check size={13} style={{ color: 'rgb(var(--ok))' }} /> : <Copy size={13} />}
              </button>
            </div>
          </div>
          <button className="hud-button-primary" onClick={onCreated}>
            Done — Go to Token List
          </button>
        </div>
      )}

      {!result && (
        <>
          <div className="form-grid">
            <div className="form-field">
              <label className="hud-label">Name *</label>
              <input className="hud-input" placeholder="my-agent-token" value={name} onChange={e => setName(e.target.value)} style={{ width: '100%' }} />
            </div>
            <div className="form-field">
              <label className="hud-label">Client ID *</label>
              <input className="hud-input" placeholder="app:my-agent" value={clientId} onChange={e => setClientId(e.target.value)} style={{ width: '100%' }} />
            </div>
          </div>

          <div className="form-field" style={{ marginTop: '0.5rem' }}>
            <label className="hud-label">Scopes *</label>
            <div style={{ display: 'flex', gap: '0.3rem', flexWrap: 'wrap', marginTop: '0.25rem' }}>
              {AVAILABLE_SCOPES.map(scope => (
                <label
                  key={scope}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '0.3rem',
                    padding: '0.25rem 0.5rem',
                    borderRadius: 'var(--radius-sm)',
                    border: '1px solid rgb(var(--border))',
                    cursor: 'pointer',
                    fontSize: '0.8rem',
                    background: scopes.includes(scope) ? 'rgba(var(--primary) / 0.1)' : 'transparent',
                  }}
                >
                  <input
                    type="checkbox"
                    checked={scopes.includes(scope)}
                    onChange={() => toggleScope(scope)}
                    style={{ accentColor: 'rgb(var(--primary))' }}
                  />
                  {scope}
                </label>
              ))}
            </div>
          </div>

          <div className="form-grid" style={{ marginTop: '0.5rem' }}>
            <div className="form-field">
              <label className="hud-label">Namespace Globs <span style={{ color: 'rgb(var(--muted))' }}>(comma-separated)</span></label>
              <input className="hud-input" placeholder="* (all namespaces)" value={nsGlobs} onChange={e => setNsGlobs(e.target.value)} style={{ width: '100%' }} />
            </div>
            <div className="form-field">
              <label className="hud-label">TTL</label>
              <input className="hud-input" placeholder="720h" value={ttl} onChange={e => setTtl(e.target.value)} style={{ width: '100%' }} />
            </div>
          </div>

          <div className="form-actions">
            <button className="hud-button-primary" onClick={handleSubmit} disabled={!canSubmit}>
              <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
                {submitting ? <Spinner size={13} /> : <Key size={13} />}
                Create Token
              </span>
            </button>
          </div>
        </>
      )}
    </div>
  );
}
