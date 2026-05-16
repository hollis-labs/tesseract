import { Keyboard, Navigation, Monitor, Zap } from 'lucide-react';

const SHORTCUTS: { key: string; description: string }[] = [
  { key: 'R', description: 'Refresh current view data' },
  { key: '?', description: 'Open this help page' },
  { key: 'Escape', description: 'Navigate back (detail → list)' },
];

const NAV_SECTIONS = [
  {
    title: 'Search & Recall',
    items: [
      { name: 'View Builder', description: 'Build and save selectors to inspect matching records before packaging them.' },
      { name: 'Packet Builder', description: 'Turn a selector into a bounded context packet with item, byte, and token budgets.' },
      { name: 'Broker', description: 'Generate a recommended selector and assembly plan from a high-level intent.' },
      { name: 'Recall / Search & Research', description: 'Search memory and knowledge directly when you want answers instead of manual selector building.' },
    ],
  },
  {
    title: 'Write & Curate',
    items: [
      { name: 'Memory Review', description: 'Triage low-confidence, reviewed, deprecated, and promotable memory items.' },
      { name: 'Memory Write / Knowledge Write / Context Write', description: 'Write domain-specific records without hand-authoring JSON.' },
      { name: 'Policy Manager', description: 'Create or update namespace ownership and guardrails like allowed ops, retention, and schema keys.' },
    ],
  },
  {
    title: 'Ops & System',
    items: [
      { name: 'Auth & Tokens', description: 'Create and manage API tokens. Set scopes and namespace globs. Token values shown once on creation.' },
      { name: 'Consistency', description: 'Scan database for consistency issues. Repair by rebuilding head pointers.' },
      { name: 'Maintenance', description: 'Trim old revisions by retention window. Compact to max revision count. Dry-run before committing.' },
      { name: 'Audit & Ops', description: 'Review recent events and operational activity across the system.' },
      { name: 'Dashboard', description: 'System overview with health status, record counts, recent activity, and quick action links.' },
    ],
  },
];

export function HelpPage() {
  return (
    <div>
      <div className="page-header">
        <h2 className="page-title">Help</h2>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem' }}>
        {/* Keyboard Shortcuts */}
        <div className="hud-panel" style={{ padding: '1rem' }}>
          <div className="hud-label" style={{ color: 'rgb(var(--primary))', marginBottom: '0.75rem', display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            <Keyboard size={14} /> Keyboard Shortcuts
          </div>
          <table className="hud-table">
            <thead>
              <tr>
                <th>Key</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {SHORTCUTS.map(s => (
                <tr key={s.key}>
                  <td>
                    <kbd style={{
                      display: 'inline-block',
                      padding: '0.15rem 0.4rem',
                      background: 'rgba(var(--panel2) / 0.8)',
                      border: '1px solid rgb(var(--border))',
                      borderRadius: 'var(--radius-sm)',
                      fontFamily: 'var(--font-mono)',
                      fontSize: '0.8rem',
                    }}>
                      {s.key}
                    </kbd>
                  </td>
                  <td style={{ fontSize: '0.85rem' }}>{s.description}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div style={{ fontSize: '0.75rem', color: 'rgb(var(--muted))', marginTop: '0.5rem' }}>
            Shortcuts are disabled when a text input, modal, or dropdown is focused.
          </div>
        </div>

        {/* Quick Tips */}
        <div className="hud-panel" style={{ padding: '1rem' }}>
          <div className="hud-label" style={{ color: 'rgb(var(--primary))', marginBottom: '0.75rem', display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
            <Zap size={14} /> Quick Tips
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
            <Tip title="Demo Mode" text="Add ?demo=1 to the URL to browse with mock data (no backend required)." />
            <Tip title="Navigation" text="Click breadcrumbs to go back. Use Escape for quick back navigation." />
            <Tip title="JSON Payloads" text="Click any record row to expand and view its JSON payload inline." />
            <Tip title="View Presets" text="Save frequently-used selectors in View Builder. Stored in browser localStorage." />
            <Tip title="Dry Run" text="Always use dry-run mode first for Trim and Compact operations before committing." />
            <Tip title="Token Security" text="Token values are shown only once at creation. Copy immediately." />
          </div>
        </div>
      </div>

      {/* Screen Reference */}
      <div className="hud-panel" style={{ padding: '1rem', marginTop: '0.75rem' }}>
        <div className="hud-label" style={{ color: 'rgb(var(--primary))', marginBottom: '0.75rem', display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
          <Navigation size={14} /> Screen Reference
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '1rem' }}>
          {NAV_SECTIONS.map(section => (
            <div key={section.title}>
              <div style={{ fontSize: '0.8rem', fontWeight: 600, marginBottom: '0.5rem', letterSpacing: '0.05em', textTransform: 'uppercase', color: 'rgb(var(--muted))' }}>
                {section.title}
              </div>
              {section.items.map(item => (
                <div key={item.name} style={{ marginBottom: '0.5rem' }}>
                  <div style={{ fontSize: '0.85rem', fontWeight: 500 }}>{item.name}</div>
                  <div style={{ fontSize: '0.75rem', color: 'rgb(var(--muted))', lineHeight: 1.4 }}>{item.description}</div>
                </div>
              ))}
            </div>
          ))}
        </div>
      </div>

      {/* API & Version */}
      <div className="hud-panel" style={{ padding: '1rem', marginTop: '0.75rem' }}>
        <div className="hud-label" style={{ color: 'rgb(var(--primary))', marginBottom: '0.5rem', display: 'flex', alignItems: 'center', gap: '0.3rem' }}>
          <Monitor size={14} /> About
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.5rem', fontSize: '0.8rem' }}>
          <div>
            <span style={{ color: 'rgb(var(--muted))' }}>Product:</span> Tesseract — Content Memory Service
          </div>
          <div>
            <span style={{ color: 'rgb(var(--muted))' }}>API Base:</span> <code style={{ fontFamily: 'var(--font-mono)' }}>/v1</code>
          </div>
          <div>
            <span style={{ color: 'rgb(var(--muted))' }}>Frontend:</span> React 18 + TypeScript + Vite
          </div>
          <div>
            <span style={{ color: 'rgb(var(--muted))' }}>Backend:</span> Go + SQLite + go:embed
          </div>
        </div>
      </div>
    </div>
  );
}

function Tip({ title, text }: { title: string; text: string }) {
  return (
    <div style={{ padding: '0.4rem 0.5rem', borderLeft: '2px solid rgb(var(--primary))', fontSize: '0.8rem' }}>
      <span style={{ fontWeight: 600 }}>{title}:</span>{' '}
      <span style={{ color: 'rgb(var(--muted))' }}>{text}</span>
    </div>
  );
}
