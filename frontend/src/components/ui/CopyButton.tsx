import { CopyButton as SysopCopyButton } from "@hollis-labs/sysop-ui";

interface Props {
  text: string;
  size?: number;
}

export function CopyButton({ text, size = 14 }: Props) {
  return (
    <SysopCopyButton
      text={text}
      label=""
      copiedLabel=""
      size="icon-xs"
      variant="ghost"
      title="Copy to clipboard"
      aria-label="Copy to clipboard"
      style={{ width: size + 10, height: size + 10 }}
      className="text-text-subtle hover:text-text"
    />
  );
}
