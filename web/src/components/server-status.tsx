import { Badge } from "@/components/ui/badge";
import type { ServerStatus } from "@/lib/servers";

const toneVariant = {
  live: "default",
  pending: "secondary",
  failed: "destructive",
  stale: "outline",
  stopped: "outline",
} as const;

// StatusBadge shows a server's state; a stale one (its node is offline) is
// dimmed, since it's only the node's last report.
export function StatusBadge({ status }: { status: ServerStatus }) {
  return (
    <Badge
      variant={toneVariant[status.tone]}
      className={status.tone === "stale" ? "opacity-60" : undefined}
    >
      {status.label}
    </Badge>
  );
}

// StatusDetail is the reason under a status, when there is one (an
// install's error, an offline node).
export function StatusDetail({ status }: { status: ServerStatus }) {
  if (!status.detail) return null;
  return (
    <p
      className={
        status.tone === "failed"
          ? "mt-1 text-xs text-destructive"
          : "mt-1 text-xs text-muted-foreground"
      }
    >
      {status.detail}
    </p>
  );
}
