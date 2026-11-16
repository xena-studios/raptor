import { Check, Copy, ExternalLink, Loader2, Share2 } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { message } from "@/lib/errors";
import { logTail, shareableLog } from "@/lib/share-log";
import { orgClient } from "@/lib/transport";

// ShareLogButton shares a log on mclo.gs for support (#429), after saying
// what's sent: the Panel hides IP addresses and node hostnames first, and
// the link is public to whoever has it.
export function ShareLogButton({
  orgId,
  nodeId,
  serverId,
  name,
  content,
  className,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  // "console", or the file's path.
  name: string;
  // Read when the dialog opens, so it's the log as it is then.
  content: () => string;
  className?: string;
}) {
  const [open, setOpen] = useState<string>();
  return (
    <>
      <Button
        size="icon-sm"
        variant="ghost"
        className={className}
        aria-label="Share the log"
        title="Share the log (mclo.gs)"
        onClick={() => setOpen(content())}
      >
        <Share2 />
      </Button>
      {open !== undefined && (
        <ShareLogDialog
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          name={name}
          content={open}
          onClose={() => setOpen(undefined)}
        />
      )}
    </>
  );
}

function ShareLogDialog({
  orgId,
  nodeId,
  serverId,
  name,
  content,
  onClose,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  name: string;
  content: string;
  onClose: () => void;
}) {
  const log = logTail(shareableLog(content));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [url, setUrl] = useState("");
  const [copied, setCopied] = useState(false);
  const what = name === "console" ? "the console" : name.split("/").pop();

  async function share() {
    setBusy(true);
    setError("");
    try {
      const res = await orgClient.shareLog({ orgId, nodeId, serverId, name, content: log.text });
      setUrl(res.url);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{url ? "Log shared" : `Share ${what}?`}</DialogTitle>
          <DialogDescription>
            {url
              ? "Send this link to whoever's helping you. Anyone with it can read the log."
              : "It goes to mclo.gs, a site for sharing game server logs, which highlights errors and gives you a link. Anyone with the link can read it."}
          </DialogDescription>
        </DialogHeader>
        {url ? (
          <div className="flex items-center gap-2 rounded-lg border px-3 py-2">
            <code className="min-w-0 flex-1 truncate text-sm">{url}</code>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Copy the link"
              onClick={async () => {
                await navigator.clipboard.writeText(url);
                setCopied(true);
                setTimeout(() => setCopied(false), 1500);
              }}
            >
              {copied ? <Check /> : <Copy />}
            </Button>
            <a
              href={url}
              target="_blank"
              rel="noreferrer"
              aria-label="Open the log"
              className="text-muted-foreground hover:text-foreground"
            >
              <ExternalLink className="size-4" />
            </a>
          </div>
        ) : log.lines === 0 ? (
          <p className="text-sm text-muted-foreground">There's nothing in it to share yet.</p>
        ) : (
          <ul className="flex list-disc flex-col gap-1 pl-5 text-sm text-muted-foreground">
            <li>
              {log.cut
                ? `The newest ${log.lines.toLocaleString()} lines (the log is longer than mclo.gs takes).`
                : `${log.lines.toLocaleString()} line${log.lines === 1 ? "" : "s"}.`}
            </li>
            <li>
              IP addresses and the node's address are hidden first. Player names, and anything else
              in the log, are sent as they are.
            </li>
            <li>It's recorded in the server's activity.</li>
          </ul>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {url ? "Done" : "Cancel"}
          </Button>
          {!url && (
            <Button disabled={busy || log.lines === 0} onClick={share}>
              {busy ? <Loader2 className="animate-spin" /> : <Share2 />}
              Share
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
