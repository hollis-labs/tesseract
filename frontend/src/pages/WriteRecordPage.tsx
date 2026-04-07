import { useState, useCallback, useMemo } from 'react';
import { Send } from 'lucide-react';
import { toast } from 'sonner';
import { writeRecord, evaluateView } from '../api/client';
import { usePoll } from '../hooks/usePoll';
import { Spinner } from '../components/ui/Spinner';

interface Props {
  onWritten: (namespace: string, key: string) => void;
  onOpenPromote: () => void;
}

export function WriteRecordPage({ onWritten, onOpenPromote }: Props) {
  const [namespace, setNamespace] = useState('');
  const [key, setKey] = useState('');
  const [actor, setActor] = useState('');
  const [payload, setPayload] = useState('{\n  \n}');
  const [metadata, setMetadata] = useState('');
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [nsFocus, setNsFocus] = useState(false);

  // Fetch known namespaces for autocomplete
  const nsFetcher = useCallback(
    () => evaluateView({ revision_scope: 'head', limit: 500 }),
    [],
  );
  const { data: viewData } = usePoll(nsFetcher, 30_000);

  const knownNamespaces = useMemo(() => {
    if (!viewData?.items) return [];
    const set = new Set(viewData.items.map(r => r.namespace));
    return Array.from(set).sort();
  }, [viewData]);

  const nsSuggestions = useMemo(() => {
    if (!namespace.trim() || !nsFocus) return [];
    const q = namespace.toLowerCase();
    return knownNamespaces.filter(ns => ns.toLowerCase().includes(q)).slice(0, 8);
  }, [namespace, knownNamespaces, nsFocus]);

  // JSON validation
  const payloadError = useMemo(() => {
    if (!payload.trim()) return 'Payload is required';
    try {
      JSON.parse(payload);
      return null;
    } catch (err) {
      return err instanceof Error ? err.message : 'Invalid JSON';
    }
  }, [payload]);

  const metadataError = useMemo(() => {
    if (!metadata.trim()) return null;
    try {
      JSON.parse(metadata);
      return null;
    } catch (err) {
      return err instanceof Error ? err.message : 'Invalid JSON';
    }
  }, [metadata]);

  const canSubmit = namespace.trim() && key.trim() && actor.trim() && !payloadError && !metadataError && !submitting;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      const res = await writeRecord({
        namespace: namespace.trim(),
        key: key.trim(),
        actor: actor.trim(),
        payload: JSON.parse(payload),
        metadata: metadata.trim() ? JSON.parse(metadata) : undefined,
        reason: reason.trim() || undefined,
      });
      toast.success(`Record written: r${res.revision}`);
      onWritten(namespace.trim(), key.trim());
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      toast.error(`Write failed: ${msg}`);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Write Record</h2>
        <div className="page-actions">
          <button className="hud-button-ghost" onClick={onOpenPromote}>
            Promote Record
          </button>
        </div>
      </div>

      <div className="hud-panel" style={{ padding: '1rem', maxWidth: 700 }}>
        {error && (
          <div style={{ padding: '0.5rem 0.75rem', marginBottom: '0.75rem', background: 'rgba(var(--danger) / 0.1)', borderRadius: 'var(--radius-sm)', color: 'rgb(var(--danger))', fontSize: '0.85rem' }}>
            {error}
          </div>
        )}

        <div className="form-grid">
          {/* Namespace */}
          <div className="form-field" style={{ position: 'relative' }}>
            <label className="hud-label">Namespace *</label>
            <input
              className="hud-input"
              placeholder="app/my-project/session"
              value={namespace}
              onChange={e => setNamespace(e.target.value)}
              onFocus={() => setNsFocus(true)}
              onBlur={() => setTimeout(() => setNsFocus(false), 150)}
              style={{ width: '100%' }}
            />
            {nsSuggestions.length > 0 && (
              <div style={{
                position: 'absolute',
                top: '100%',
                left: 0,
                right: 0,
                background: 'rgb(var(--panel))',
                border: '1px solid rgb(var(--border))',
                borderRadius: 'var(--radius-sm)',
                zIndex: 10,
                maxHeight: 200,
                overflow: 'auto',
              }}>
                {nsSuggestions.map(ns => (
                  <div
                    key={ns}
                    onMouseDown={() => { setNamespace(ns); setNsFocus(false); }}
                    style={{
                      padding: '0.35rem 0.5rem',
                      fontSize: '0.8rem',
                      cursor: 'pointer',
                      fontFamily: 'var(--font-mono)',
                    }}
                    onMouseEnter={e => (e.currentTarget.style.background = 'rgba(var(--panel2) / 0.8)')}
                    onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
                  >
                    {ns}
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Key */}
          <div className="form-field">
            <label className="hud-label">Key *</label>
            <input
              className="hud-input"
              placeholder="status"
              value={key}
              onChange={e => setKey(e.target.value)}
              style={{ width: '100%' }}
            />
          </div>

          {/* Actor */}
          <div className="form-field">
            <label className="hud-label">Actor *</label>
            <input
              className="hud-input"
              placeholder="user:jane or app:my-agent"
              value={actor}
              onChange={e => setActor(e.target.value)}
              style={{ width: '100%' }}
            />
          </div>

          {/* Reason */}
          <div className="form-field">
            <label className="hud-label">Reason <span style={{ color: 'rgb(var(--muted))' }}>(optional)</span></label>
            <input
              className="hud-input"
              placeholder="Manual update via UI"
              value={reason}
              onChange={e => setReason(e.target.value)}
              style={{ width: '100%' }}
            />
          </div>
        </div>

        {/* Payload */}
        <div className="form-field" style={{ marginTop: '0.25rem' }}>
          <label className="hud-label">
            Payload (JSON) *
            {payloadError && payload.trim() && (
              <span style={{ color: 'rgb(var(--danger))', marginLeft: '0.5rem', textTransform: 'none', letterSpacing: 'normal', fontSize: '0.7rem' }}>
                {payloadError}
              </span>
            )}
          </label>
          <textarea
            className="hud-textarea"
            value={payload}
            onChange={e => setPayload(e.target.value)}
            style={{
              width: '100%',
              minHeight: 160,
              borderColor: payloadError && payload.trim() ? 'rgb(var(--danger))' : undefined,
            }}
            spellCheck={false}
          />
        </div>

        {/* Metadata */}
        <div className="form-field" style={{ marginTop: '0.25rem' }}>
          <label className="hud-label">
            Metadata (JSON) <span style={{ color: 'rgb(var(--muted))' }}>(optional)</span>
            {metadataError && (
              <span style={{ color: 'rgb(var(--danger))', marginLeft: '0.5rem', textTransform: 'none', letterSpacing: 'normal', fontSize: '0.7rem' }}>
                {metadataError}
              </span>
            )}
          </label>
          <textarea
            className="hud-textarea"
            value={metadata}
            onChange={e => setMetadata(e.target.value)}
            placeholder='{"source": "ui"}'
            style={{
              width: '100%',
              minHeight: 60,
              borderColor: metadataError ? 'rgb(var(--danger))' : undefined,
            }}
            spellCheck={false}
          />
        </div>

        <div className="form-actions">
          <button
            className="hud-button-primary"
            onClick={handleSubmit}
            disabled={!canSubmit}
          >
            <span style={{ display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
              {submitting ? <Spinner size={13} /> : <Send size={13} />}
              Write Record
            </span>
          </button>
        </div>
      </div>
    </div>
  );
}
