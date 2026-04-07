interface Props {
  title: string;
  message: string;
  confirmLabel?: string;
  danger?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmModal({ title, message, confirmLabel, danger, onConfirm, onCancel }: Props) {
  return (
    <div className="hud-modal-overlay" onClick={onCancel} role="dialog" aria-modal="true" aria-label={title}>
      <div className="hud-modal" onClick={e => e.stopPropagation()} style={{ maxWidth: '420px' }}>
        <div style={{ marginBottom: '0.75rem', fontWeight: 600, fontSize: '0.9rem', letterSpacing: '0.05em' }}>
          {title}
        </div>
        <div style={{ fontSize: '0.85rem', color: 'rgb(var(--muted))', marginBottom: '1.25rem', lineHeight: 1.5 }}>
          {message}
        </div>
        <div style={{ display: 'flex', gap: '0.5rem', justifyContent: 'flex-end' }}>
          <button className="hud-button-ghost" onClick={onCancel}>Cancel</button>
          <button
            className={danger ? 'hud-button-danger' : 'hud-button-primary'}
            onClick={onConfirm}
          >
            {confirmLabel ?? 'Confirm'}
          </button>
        </div>
      </div>
    </div>
  );
}
