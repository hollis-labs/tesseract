import { LoaderCircle } from "lucide-react";

interface Props {
  size?: number;
}

export function Spinner({ size = 16 }: Props) {
  return <LoaderCircle aria-label="Loading" className="animate-spin" width={size} height={size} />;
}
