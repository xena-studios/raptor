import { Download, Loader2, X } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/toast";
import { message } from "@/lib/errors";
import { formatBytes } from "@/lib/metrics";
import type { useFilesApi } from "./data";

// Downloading a link straight into the server's files (Wings' files.pull,
// docs/WINGS.md#files-and-sftp): the node fetches it, so a big file never
// goes through the owner's computer. Each download is a job on the node;
// this follows it until it's done.

type Api = ReturnType<typeof useFilesApi>;

type Status = {
  job_id: string;
  path: string;
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled";
  received: number;
  size: number;
  error?: string;
};

export type Pull = { jobId: string; name: string; status?: Status; error?: string };

// nameFromURL is the file name a link would be saved as (the node uses
// the same: the last part of its path).
export function nameFromURL(raw: string): string {
  try {
    const u = new URL(raw.trim());
    const last = decodeURIComponent(u.pathname.split("/").pop() ?? "");
    return last || "download";
  } catch {
    return "";
  }
}

const finished = (s?: Status) => s && ["succeeded", "failed", "cancelled"].includes(s.status);

// usePulls starts downloads and follows them, once a second, until they
// end.
export function usePulls(api: Api) {
  const [pulls, setPulls] = useState<Pull[]>([]);
  // The latest of each, for the timer, which runs while any download does.
  const live = useRef({ pulls, api });
  live.current = { pulls, api };
  const going = pulls.some((p) => !finished(p.status) && !p.error);

  useEffect(() => {
    if (!going) return;
    const t = setInterval(async () => {
      const { pulls, api } = live.current;
      for (const p of pulls) {
        if (finished(p.status) || p.error) continue;
        try {
          const st = await api.run<Status>("files.pull.status", { job_id: p.jobId });
          if (!st) continue;
          setPulls((l) => l.map((x) => (x.jobId === p.jobId ? { ...x, status: st } : x)));
          if (st.status === "succeeded") {
            toast.success(`Downloaded ${p.name}.`);
            void api.refresh();
          }
        } catch (err) {
          setPulls((l) => l.map((x) => (x.jobId === p.jobId ? { ...x, error: message(err) } : x)));
        }
      }
    }, 1000);
    return () => clearInterval(t);
  }, [going]);

  return {
    pulls,
    async start(url: string, dir: string, name: string) {
      const res = await api.run<{ job_id: string }>("files.pull", { url: url.trim(), dir, name });
      if (!res?.job_id) throw new Error("The node didn't start the download.");
      setPulls((l) => [...l, { jobId: res.job_id, name }]);
    },
    async cancel(p: Pull) {
      if (!finished(p.status) && !p.error) {
        try {
          await api.run("files.pull.cancel", { job_id: p.jobId });
        } catch {
          // Already over.
        }
      }
      setPulls((l) => l.filter((x) => x.jobId !== p.jobId));
    },
  };
}

// PullRows are the downloads, in the strip above the list with uploads.
export function PullRows({ pulls, onHide }: { pulls: Pull[]; onHide: (p: Pull) => void }) {
  if (pulls.length === 0) return null;
  return (
    <ul className="flex flex-col gap-1 text-sm">
      {pulls.map((p) => {
        const st = p.status;
        const failed = p.error || (st?.status === "failed" && (st.error || "The download failed."));
        const fraction = st?.size ? st.received / st.size : undefined;
        const over = finished(st) || !!p.error;
        return (
          <li key={p.jobId} className="flex items-center gap-3">
            <Download className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">{p.name}</span>
            {failed ? (
              <span className="min-w-0 truncate text-xs text-destructive" title={failed}>
                {failed}
              </span>
            ) : st?.status === "succeeded" ? (
              <span className="text-xs text-muted-foreground">Downloaded</span>
            ) : st?.status === "cancelled" ? (
              <span className="text-xs text-muted-foreground">Cancelled</span>
            ) : fraction !== undefined ? (
              <span className="flex w-40 items-center gap-2">
                <span className="h-1.5 flex-1 overflow-hidden rounded bg-muted">
                  <span
                    className="block h-full bg-primary transition-[width]"
                    style={{ width: `${Math.round(fraction * 100)}%` }}
                  />
                </span>
                <span className="w-9 text-right text-xs tabular-nums text-muted-foreground">
                  {Math.round(fraction * 100)}%
                </span>
              </span>
            ) : (
              <span className="flex items-center gap-1.5 text-xs tabular-nums text-muted-foreground">
                <Loader2 className="size-3 animate-spin" />
                {st?.received ? formatBytes(st.received) : "Starting…"}
              </span>
            )}
            <Button
              size="icon-xs"
              variant="ghost"
              aria-label={over ? `Hide ${p.name}` : `Cancel downloading ${p.name}`}
              onClick={() => onHide(p)}
            >
              <X />
            </Button>
          </li>
        );
      })}
    </ul>
  );
}

// PullDialog asks for a link, and the name to save it as in dir.
export function PullDialog({
  open,
  dir,
  onOpenChange,
  onStart,
}: {
  open: boolean;
  dir: string;
  onOpenChange: (open: boolean) => void;
  onStart: (url: string, name: string) => Promise<void>;
}) {
  const [url, setUrl] = useState("");
  const [name, setName] = useState("");
  const [named, setNamed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setUrl("");
      setName("");
      setNamed(false);
      setError("");
    }
  }, [open]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onStart(url, name.trim() || nameFromURL(url));
      onOpenChange(false);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Download from a link</DialogTitle>
            <DialogDescription>
              The node downloads it straight into {dir ? `/${dir}` : "the server's main folder"}, so
              it doesn't go through your computer. Links to private networks aren't followed.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pull-url">Link</Label>
            <Input
              id="pull-url"
              type="url"
              required
              autoFocus
              placeholder="https://example.com/plugin.jar"
              maxLength={2048}
              value={url}
              onChange={(e) => {
                setUrl(e.target.value);
                if (!named) setName(nameFromURL(e.target.value));
              }}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pull-name">Save as</Label>
            <Input
              id="pull-name"
              required
              maxLength={255}
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                setNamed(true);
              }}
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !url.trim() || !name.trim()}>
              {busy ? <Loader2 className="animate-spin" /> : <Download />} Download
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
