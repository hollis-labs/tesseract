interface Props {
  authMode?: string;
}

export function AppFooter({ authMode }: Props) {
  return (
    <footer
      className="flex h-8 shrink-0 items-center gap-5 border-t border-border-strong bg-bg px-4 text-[11px] text-text-subtle"
      role="contentinfo"
      aria-label="Keyboard shortcuts and status"
    >
      <span className="flex items-center gap-1.5">
        <kbd className="rounded border border-border-strong bg-panel-2 px-1 font-mono text-[10px]">
          ?
        </kbd>{" "}
        help
      </span>
      <span className="flex items-center gap-1.5">
        <kbd className="rounded border border-border-strong bg-panel-2 px-1 font-mono text-[10px]">
          Esc
        </kbd>{" "}
        back
      </span>
      <span className="flex-1" />
      {authMode && <span>auth: {authMode}</span>}
      <span className="text-text-subtle/50">
        Tesseract <span className="font-mono">v0.1</span>
      </span>
    </footer>
  );
}
