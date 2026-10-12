import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Folder, Loader2, RotateCcw, Trash2 } from "lucide-react";
import { useState } from "react";

import type { useFilesApi } from "@/components/file-manager/data";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { formatBytes } from "@/lib/metrics";

// The trash (Wings' files.trash*, docs/WINGS.md#trash): what was deleted
// from the file manager, for 7 days, to put back or delete for good. It
// counts toward the server's disk limit until it's gone.

type TrashEntry = {
  id: string;
  path: string;
  is_dir: boolean;
  size: number;
  deleted_at: number;
  deleted_by: string;
};

const keepDays = 7;

export function useTrash(api: ReturnType<typeof useFilesApi>, nodeId: string, serverId: string) {
  return useQuery({
    // Under the file manager's key, so its refresh refreshes this too.
    queryKey: ["files", nodeId, serverId, "\u0000trash"],
    queryFn: async () => (await api.run<TrashEntry[]>("files.trash", {})) ?? [],
    staleTime: 5_000,
  });
}

function ago(ms: number): string {
  const s = Math.max(0, (Date.now() - ms) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

function left(ms: number): string {
  const days = Math.ceil((ms + keepDays * 86400_000 - Date.now()) / 86400_000);
  return days <= 1 ? "goes within a day" : `goes in ${days} days`;
}

export function TrashDialog({
  api,
  nodeId,
  serverId,
  orgId,
  userId,
  canWrite,
  onClose,
}: {
  api: ReturnType<typeof useFilesApi>;
  nodeId: string;
  serverId: string;
  orgId: string;
  userId: string;
  canWrite: boolean;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const trash = useTrash(api, nodeId, serverId);
  const members = useConnectQuery(OrgService.method.listMembers, { orgId }, { retry: false });
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [emptying, setEmptying] = useState(false);
  const entries = trash.data ?? [];
  const total = entries.reduce((n, e) => n + e.size, 0);

  const who = (id: string) => {
    if (id === userId) return "you";
    const m = members.data?.members.find((x) => x.userId === id);
    return m ? m.name || m.email : "someone";
  };

  async function act(key: string, fn: () => Promise<string | undefined>) {
    setBusy(key);
    setError("");
    setNotice("");
    try {
      const done = await fn();
      if (done) setNotice(done);
      await client.invalidateQueries({ queryKey: ["files", nodeId, serverId] });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  const restore = (e: TrashEntry) =>
    act(e.id, async () => {
      const r = await api.run<{ id: string; path: string }[]>("files.trash.restore", {
        entries: [e.id],
      });
      const to = r?.[0]?.path;
      return to && to !== e.path
        ? `Restored as ${to}: something new is at ${e.path} now.`
        : `Restored ${e.path}.`;
    });

  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Trash</DialogTitle>
          <DialogDescription>
            Deleted files stay here for {keepDays} days, then go for good. They still count toward
            the server's disk limit until then.
            {entries.length > 0 &&
              ` ${entries.length} ${entries.length === 1 ? "item" : "items"}, ${formatBytes(total)}.`}
          </DialogDescription>
        </DialogHeader>
        {trash.error && <p className="text-sm text-destructive">{message(trash.error)}</p>}
        {trash.isPending ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : entries.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">The trash is empty.</p>
        ) : (
          <ul className="-mx-1 flex max-h-[50vh] flex-col divide-y overflow-y-auto">
            {entries.map((e) => (
              <li key={e.id} className="flex items-center gap-3 px-1 py-2 text-sm">
                {e.is_dir ? (
                  <Folder className="size-4 shrink-0 text-muted-foreground" />
                ) : (
                  <Trash2 className="size-4 shrink-0 text-muted-foreground" />
                )}
                <div className="min-w-0 flex-1">
                  <p className="truncate font-mono text-xs">{e.path}</p>
                  <p className="truncate text-xs text-muted-foreground">
                    {formatBytes(e.size)} · deleted {ago(e.deleted_at)} by {who(e.deleted_by)} ·{" "}
                    {left(e.deleted_at)}
                  </p>
                </div>
                {canWrite && (
                  <>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={!!busy}
                      onClick={() => restore(e)}
                    >
                      {busy === e.id ? <Loader2 className="animate-spin" /> : <RotateCcw />}
                      Restore
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      className="text-destructive"
                      aria-label={`Delete ${e.path} for good`}
                      title="Delete for good"
                      disabled={!!busy}
                      onClick={() =>
                        act(`d${e.id}`, async () => {
                          await api.run("files.trash.delete", { entries: [e.id] });
                          return undefined;
                        })
                      }
                    >
                      <Trash2 />
                    </Button>
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
        {notice && <p className="text-sm text-muted-foreground">{notice}</p>}
        {error && <p className="text-sm text-destructive">{error}</p>}
        <DialogFooter className="sm:justify-between">
          {canWrite && entries.length > 0 ? (
            emptying ? (
              <div className="flex items-center gap-2 text-sm">
                <span>Delete all {entries.length} for good?</span>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={!!busy}
                  onClick={() =>
                    act("empty", async () => {
                      await api.run("files.trash.empty", {});
                      setEmptying(false);
                      return "The trash is empty.";
                    })
                  }
                >
                  {busy === "empty" && <Loader2 className="animate-spin" />} Empty trash
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setEmptying(false)}>
                  Keep
                </Button>
              </div>
            ) : (
              <Button
                variant="outline"
                className="text-destructive"
                onClick={() => setEmptying(true)}
              >
                <Trash2 /> Empty trash
              </Button>
            )
          ) : (
            <span />
          )}
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
