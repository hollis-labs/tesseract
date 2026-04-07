interface Props {
  data: unknown;
  maxHeight?: string;
}

export function JsonViewer({ data, maxHeight }: Props) {
  const formatted = formatJson(data);

  return (
    <div
      className="json-viewer"
      style={{ maxHeight, overflow: maxHeight ? 'auto' : undefined }}
      dangerouslySetInnerHTML={{ __html: formatted }}
    />
  );
}

function formatJson(data: unknown): string {
  if (data === null || data === undefined) {
    return '<span class="json-null">null</span>';
  }

  let raw: string;
  if (typeof data === 'string') {
    try {
      // If it's a JSON string, parse and re-format
      const parsed = JSON.parse(data);
      raw = JSON.stringify(parsed, null, 2);
    } catch {
      raw = JSON.stringify(data, null, 2);
    }
  } else {
    raw = JSON.stringify(data, null, 2);
  }

  return syntaxHighlight(raw);
}

function syntaxHighlight(json: string): string {
  return json.replace(
    /("(\\u[a-fA-F0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+-]?\d+)?)/g,
    (match) => {
      if (/^"/.test(match)) {
        if (/:$/.test(match)) {
          // Key
          return `<span class="json-key">${escapeHtml(match.slice(0, -1))}</span>:`;
        }
        // String value
        return `<span class="json-string">${escapeHtml(match)}</span>`;
      }
      if (/true|false/.test(match)) {
        return `<span class="json-bool">${match}</span>`;
      }
      if (/null/.test(match)) {
        return `<span class="json-null">${match}</span>`;
      }
      // Number
      return `<span class="json-number">${match}</span>`;
    },
  );
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}
