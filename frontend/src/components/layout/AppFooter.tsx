interface Props {
  authMode?: string;
}

export function AppFooter({ authMode }: Props) {
  return (
    <footer className="app-footer" role="contentinfo" aria-label="Keyboard shortcuts and status">
      <span className="footer-hint">
        <kbd>R</kbd> refresh
      </span>
      <span className="footer-hint">
        <kbd>?</kbd> help
      </span>
      <span className="footer-hint">
        <kbd>Esc</kbd> back
      </span>
      <span className="footer-spacer" />
      {authMode && (
        <span className="footer-hint">
          auth: {authMode}
        </span>
      )}
      <span className="footer-hint" style={{ opacity: 0.5 }}>
        Tesseract v0.1
      </span>
    </footer>
  );
}
