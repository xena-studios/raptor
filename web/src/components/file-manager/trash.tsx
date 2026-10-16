import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { File, Folder, Loader2, RotateCcw, Trash2 } from "lucide-react";
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
import { toast } from "@/components/ui/toast";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { ago as since } from "@/lib/format";
import { formatBytes } from "@/lib/metrics";

// The trash (Wings' files.trash*, docs/WINGS.md#trash): everything deleted
// in the file manager goes here, for 7 days, to put back or purge. It
// counts toward the server's disk limit until it's gone. It's a place in
// the file manager (the sidebar's Trash), not a dialog.

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
  return since(new Date(ms));
}

function left(ms: number): string {
  const days = Math.ceil((ms + keepDays * 86400_000 - Date.now()) / 86400_000);
  return days <= 1 ? "goes within a day" : `goes in ${days} days`;
}

export function TrashView({
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
  // What's about to be purged for good: one entry, or everything. Purging
  // is the only way anything is deleted for good, so it always asks.
  const [purging, setPurging] = useState<TrashEntry | "all" | null>(null);
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
    try {
      const done = await fn();
      if (done) toast.success(done);
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
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-wrap items-start justify-between gap-3 border-b px-4 py-3">
        <div className="min-w-0 space-y-1">
          <h3 className="flex items-center gap-2 text-sm font-medium">
            <Trash2 className="size-4 text-muted-foreground" /> Trash
          </h3>
          <p className="text-xs text-muted-foreground">
            What's deleted stays here for {keepDays} days, then goes for good. It still counts
            toward the server's disk limit until then.
            {entries.length > 0 &&
              ` ${entries.length} ${entries.length === 1 ? "item" : "items"}, ${formatBytes(total)}.`}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {canWrite && entries.length > 0 && (
            <Button
              size="sm"
              variant="outline"
              className="text-destructive"
              disabled={!!busy}
              onClick={() => setPurging("all")}
            >
              <Trash2 /> Empty trash
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={onClose}>
            Back to files
          </Button>
        </div>
      </div>
      {(error || trash.error) && (
        <div className="shrink-0 px-4 pt-3 text-sm">
          {error && <p className="text-destructive">{error}</p>}
          {trash.error && <p className="text-destructive">{message(trash.error)}</p>}
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto p-4">
        {trash.isPending ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : entries.length === 0 ? (
          <p className="py-10 text-center text-sm text-muted-foreground">The trash is empty.</p>
        ) : (
          <ul className="flex flex-col divide-y rounded-lg border">
            {entries.map((e) => (
              <li key={e.id} className="flex items-center gap-3 px-3 py-2 text-sm">
                {e.is_dir ? (
                  <Folder className="size-4 shrink-0 text-muted-foreground" />
                ) : (
                  <File className="size-4 shrink-0 text-muted-foreground" />
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
                      aria-label={`Purge ${e.path}`}
                      title="Purge: delete it for good"
                      disabled={!!busy}
                      onClick={() => setPurging(e)}
                    >
                      <Trash2 />
                    </Button>
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
      <Dialog open={purging !== null} onOpenChange={(o) => !o && setPurging(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {purging === "all"
                ? `Delete all ${entries.length} ${entries.length === 1 ? "item" : "items"} in the trash for good?`
                : `Delete ${purging?.path} for good?`}
            </DialogTitle>
            <DialogDescription>
              {purging === "all"
                ? `${formatBytes(total)} goes, and frees that much of the server's disk. None of it can be put back.`
                : `It can't be put back afterwards${purging?.is_dir ? ", nor anything in it" : ""}.`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPurging(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={!!busy}
              onClick={() => {
                const what = purging;
                setPurging(null);
                if (what === "all") {
                  void act("empty", async () => {
                    await api.run("files.trash.empty", {});
                    return "The trash is empty.";
                  });
                } else if (what) {
                  void act(`d${what.id}`, async () => {
                    await api.run("files.trash.delete", { entries: [what.id] });
                    return `Purged ${what.path}.`;
                  });
                }
              }}
            >
              <Trash2 /> Delete for good
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
