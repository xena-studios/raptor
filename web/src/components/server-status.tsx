import { cn } from "cn";

import { HelpLink } from "@/components/help-link";
import type { ServerStatus } from "@/lib/servers";

// toneText is the one place state gets colour; the label always spells it
// out too, so colour never carries it alone.
const toneText: Record<ServerStatus["tone"], string> = {
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
// What to do when a server failed: where the docs explain it.
const failedHelp: Record<string, { path: string; text: string }> = {
  "Install failed": {
    path: "/help/server-problems/#the-install-failed",
    text: "The console shows where it stopped. Fix the cause, then Reinstall on the Settings tab.",
  },
  Crashed: {
    path: "/help/server-problems/#it-crashed",
    text: "The console shows its last output.",
  },
};

export function StatusDetail({ status }: { status: ServerStatus }) {
  // By label, so it still shows on an offline node's last report.
  const help = failedHelp[status.label];
  if (!status.detail && !help) return null;
  return (
    <p
      className={
        status.tone === "failed"
          ? "mt-1 text-xs text-destructive"
          : "mt-1 text-xs text-muted-foreground"
      }
    >
      {status.detail}
      {help && (
        <span className="text-muted-foreground">
          {status.detail && (/[.!?]$/.test(status.detail) ? " " : ". ")}
          {help.text} <HelpLink path={help.path}>What to check</HelpLink>
        </span>
      )}
    </p>
  );
}
