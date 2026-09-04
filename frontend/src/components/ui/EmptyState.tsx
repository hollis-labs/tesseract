import { EmptyState as SysopEmptyState } from "@hollis-labs/sysop-ui";

interface Props {
  message: string;
  sub?: string;
  icon?: React.ReactNode;
}

export function EmptyState({ message, sub }: Props) {
  return (
    <SysopEmptyState
      variant="empty"
      title={message}
      description={sub ?? "Nothing to show here yet."}
    />
  );
}
