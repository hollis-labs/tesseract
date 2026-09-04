import { ConfirmDialog } from "@hollis-labs/sysop-ui";

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
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!open) onCancel();
      }}
      title={title}
      description={message}
      {...(confirmLabel ? { confirmLabel } : {})}
      destructive={danger ?? false}
      onConfirm={onConfirm}
      widthClassName="w-[420px] max-w-[calc(100vw-2rem)]"
    />
  );
}
