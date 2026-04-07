import { Inbox } from 'lucide-react';

interface Props {
  message: string;
  sub?: string;
  icon?: React.ReactNode;
}

export function EmptyState({ message, sub, icon }: Props) {
  return (
    <div style={{
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'center',
      justifyContent: 'center',
      padding: '3rem 1rem',
      color: 'rgb(var(--muted))',
      gap: '0.5rem',
    }}>
      {icon ?? <Inbox size={32} strokeWidth={1.5} />}
      <div style={{ fontSize: '0.85rem' }}>{message}</div>
      {sub && <div style={{ fontSize: '0.75rem', opacity: 0.6 }}>{sub}</div>}
    </div>
  );
}
