import type { ServerStatus } from "@/lib/servers";
import { cn } from "@/lib/utils";

// toneText is the one place state gets colour; the label always spells it
// out too, so colour never carries it alone.
export const toneText: Record<ServerStatus["tone"], string> = {
  live: "text-ok",
  pending: "text-warn",
  failed: "text-destructive",
  stale: "text-muted-foreground opacity-60",
  stopped: "text-muted-foreground",
};

// StatusBadge shows a state as a readout: [ RUNNING ]. A stale one (its
// node is offline) is dimmed, since it's only the node's last report.
export function StatusBadge({ status, className }: { status: ServerStatus; className?: string }) {
  return (
    <span
      className={cn(
        "shrink-0 font-mono text-xs tracking-wide whitespace-nowrap uppercase tabular-nums",
        toneText[status.tone],
        className,
      )}
    >
      [ {status.label} ]
    </span>
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
