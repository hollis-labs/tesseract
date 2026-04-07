import { Activity } from 'lucide-react';
import { isDemoMode } from '../../demo/data';
import type { HealthStatus } from '../../api/types';

interface Props {
  pageTitle: string;
  health: HealthStatus | null;
}

export function AppHeader({ pageTitle, health }: Props) {
  const status = health?.status ?? 'unknown';
  const dotClass =
    status === 'ready' ? 'ok' :
    status === 'degraded' ? 'warn' : 'idle';

  return (
    <header className="app-header" role="banner">
      <a href="#main-content" className="skip-link">Skip to content</a>
      <span className="app-header-logo" aria-label="Cortex">CORTEX</span>
      <span className="app-header-subtitle">Content Memory Service</span>
      <span className="app-header-title">{pageTitle}</span>
      {isDemoMode() && (
        <span className="hud-badge-warn" style={{ fontSize: '0.6rem', marginLeft: '0.5rem' }}>DEMO</span>
      )}
      <div className="app-header-status">
        <span className={`status-dot ${dotClass}`} />
        <Activity size={13} />
        <span>{status}</span>
      </div>
    </header>
  );
}
