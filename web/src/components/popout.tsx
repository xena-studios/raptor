import { ExternalLink } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { shortId } from "@/lib/ids";

// Pop-outs (docs/PANEL.md#pop-outs): the console, files, and stats of a
// server, and a node's stats, each on a page of its own without the app's
// sidebar, opened in a window of its own.
export type Popout =
  | { kind: "console" | "files" | "stats"; orgId: string; nodeId: string; serverId: string }
  | { kind: "node-stats"; orgId: string; nodeId: string };

export function popoutPath(p: Popout): string {
  const org = shortId(p.orgId);
  const node = shortId(p.nodeId);
  if (p.kind === "node-stats") return `/popout/${org}/nodes/${node}/stats`;
  return `/popout/${org}/servers/${node}/${shortId(p.serverId)}/${p.kind}`;
}

// open opens the pop-out in a window of its own, or brings it back to the
// front if it's open already (one window per pop-out, by name).
function open(p: Popout) {
  const name = `raptor-${popoutPath(p).replaceAll("/", "-")}`;
  const [w, h] = p.kind === "files" ? [1200, 800] : [1000, 700];
  const win = window.open(popoutPath(p), name, `popup,width=${w},height=${h}`);
  win?.focus();
}

// PopoutButton opens a pop-out. It's a link too: a middle click or
// Ctrl/Cmd-click opens it in a tab.
export function PopoutButton({ to, label }: { to: Popout; label: string }): ReactNode {
  return (
    <Button
      size="icon-sm"
      variant="ghost"
      aria-label={`Open ${label} in its own window`}
      title={`Open ${label} in its own window`}
      render={
        <a
          href={popoutPath(to)}
          target="_blank"
          rel="noopener"
          onClick={(e) => {
            if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
            e.preventDefault();
            open(to);
          }}
        />
      }
    >
      <ExternalLink />
    </Button>
  );
}
