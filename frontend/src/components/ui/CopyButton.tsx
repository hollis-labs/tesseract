import { useState } from 'react';
import { Copy, Check } from 'lucide-react';

interface Props {
  text: string;
  size?: number;
}

export function CopyButton({ text, size = 14 }: Props) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <button
      onClick={handleCopy}
      title="Copy to clipboard"
      style={{
        background: 'none',
        border: 'none',
        cursor: 'pointer',
        color: copied ? 'rgb(var(--ok))' : 'rgb(var(--muted))',
        padding: '2px',
        display: 'inline-flex',
        alignItems: 'center',
        transition: 'color 0.15s',
      }}
    >
      {copied ? <Check size={size} /> : <Copy size={size} />}
    </button>
  );
}
