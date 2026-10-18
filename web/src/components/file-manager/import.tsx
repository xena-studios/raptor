import { ArrowLeft, FolderInput, KeyRound, Loader2, X } from "lucide-react";
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

// Importing a server's files from another host over SFTP (Wings'
// files.import, docs/WINGS.md#files-and-sftp): an old panel, a game host.
// The owner checks the host's key first; nothing already here is replaced.

type Api = ReturnType<typeof useFilesApi>;

type Status = {
  job_id: string;
  host: string;
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled";
  files: number;
  bytes: number;
  skipped: number;
  current?: string;
  error?: string;
};

export type Import = { jobId: string; host: string; status?: Status; error?: string };

const finished = (s?: Status) => s && ["succeeded", "failed", "cancelled"].includes(s.status);

export function useImports(api: Api) {
  const [imports, setImports] = useState<Import[]>([]);
  const live = useRef({ imports, api });
  live.current = { imports, api };
  const going = imports.some((p) => !finished(p.status) && !p.error);

  useEffect(() => {
    if (!going) return;
    const t = setInterval(async () => {
      const { imports, api } = live.current;
      for (const p of imports) {
        if (finished(p.status) || p.error) continue;
        try {
          const st = await api.run<Status>("files.import.status", { job_id: p.jobId });
          if (!st) continue;
          setImports((l) => l.map((x) => (x.jobId === p.jobId ? { ...x, status: st } : x)));
          if (finished(st)) void api.refresh();
          if (st.status === "succeeded") {
            toast.success(
              `Imported ${st.files} ${st.files === 1 ? "file" : "files"} from ${p.host}${
                st.skipped ? `; ${st.skipped} left out (already here, or not allowed)` : ""
              }.`,
            );
          }
        } catch (err) {
          setImports((l) =>
            l.map((x) => (x.jobId === p.jobId ? { ...x, error: message(err) } : x)),
          );
        }
      }
    }, 1000);
    return () => clearInterval(t);
  }, [going]);

  return {
    imports,
    add(jobId: string, host: string) {
      setImports((l) => [...l, { jobId, host }]);
    },
    async cancel(p: Import) {
      if (!finished(p.status) && !p.error) {
        try {
          await api.run("files.import.cancel", { job_id: p.jobId });
        } catch {
          // Already over.
        }
      }
      setImports((l) => l.filter((x) => x.jobId !== p.jobId));
    },
  };
}

export function ImportRows({
  imports,
  onHide,
}: {
  imports: Import[];
  onHide: (p: Import) => void;
}) {
  if (imports.length === 0) return null;
  return (
    <ul className="flex flex-col gap-1 text-sm">
      {imports.map((p) => {
        const st = p.status;
        const failed = p.error || (st?.status === "failed" && (st.error || "The import failed."));
        const over = finished(st) || !!p.error;
        return (
          <li key={p.jobId} className="flex items-center gap-3">
            <FolderInput className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate">
              From {p.host}
              {st?.current && !over && (
                <span className="text-muted-foreground"> · {st.current}</span>
              )}
            </span>
            {failed ? (
              <span className="min-w-0 truncate text-xs text-destructive" title={failed}>
                {failed}
              </span>
            ) : (
              <span className="flex items-center gap-1.5 text-xs tabular-nums text-muted-foreground">
                {!over && <Loader2 className="size-3 animate-spin" />}
                {st?.status === "cancelled"
                  ? "Cancelled"
                  : `${st?.files ?? 0} ${st?.files === 1 ? "file" : "files"} · ${formatBytes(st?.bytes ?? 0)}`}
                {st?.status === "succeeded" && " · done"}
              </span>
            )}
            <Button
              size="icon-xs"
              variant="ghost"
              aria-label={over ? "Hide the import" : `Stop importing from ${p.host}`}
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

type Source = {
  host: string;
  port: number;
  user: string;
  password: string;
  private_key: string;
  path: string;
};

const blank: Source = { host: "", port: 22, user: "", password: "", private_key: "", path: "" };

// ImportDialog asks where to import from, has the owner check the host's
// key, then starts the import into dir.
export function ImportDialog({
  open,
  dir,
  api,
  onOpenChange,
  onStarted,
}: {
  open: boolean;
  dir: string;
  api: Api;
  onOpenChange: (open: boolean) => void;
  onStarted: (jobId: string, host: string) => void;
}) {
  const [src, setSrc] = useState<Source>(blank);
  const [useKey, setUseKey] = useState(false);
  const [hostKey, setHostKey] = useState<{ key: string; fingerprint: string }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setSrc(blank);
      setUseKey(false);
      setHostKey(undefined);
      setError("");
    }
  }, [open]);

  const set = (patch: Partial<Source>) => {
    setSrc({ ...src, ...patch });
    if ("host" in patch || "port" in patch) setHostKey(undefined);
  };

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (!hostKey) {
        const k = await api.run<{ key: string; fingerprint: string }>("files.import.hostkey", {
          source: { host: src.host.trim(), port: src.port },
        });
        setHostKey(k);
        return;
      }
      const res = await api.run<{ job_id: string }>("files.import", {
        dir,
        source: {
          host: src.host.trim(),
          port: src.port,
          user: src.user.trim(),
          password: useKey ? undefined : src.password,
          private_key: useKey ? src.private_key : undefined,
          host_key: hostKey.key,
          path: src.path.trim() || undefined,
        },
      });
      if (!res?.job_id) throw new Error("The node didn't start the import.");
      onStarted(res.job_id, src.host.trim());
      onOpenChange(false);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Import from another host</DialogTitle>
            <DialogDescription>
              Copy a server's files in over SFTP, from an old panel or a game host, into{" "}
              {dir ? `/${dir}` : "the server's main folder"}. Files already here are kept, not
              replaced.
            </DialogDescription>
          </DialogHeader>

          {!hostKey ? (
            <>
              <div className="grid gap-3 sm:grid-cols-[1fr_6rem]">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="imp-host">Host</Label>
                  <Input
                    id="imp-host"
                    required
                    autoFocus
                    placeholder="node1.oldhost.com"
                    autoComplete="off"
                    spellCheck={false}
                    value={src.host}
                    onChange={(e) => set({ host: e.target.value })}
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="imp-port">Port</Label>
                  <Input
                    id="imp-port"
                    type="number"
                    required
                    min={1}
                    max={65535}
                    value={src.port}
                    onChange={(e) => set({ port: Number(e.target.value) })}
                  />
                </div>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="imp-user">Username</Label>
                <Input
                  id="imp-user"
                  required
                  autoComplete="off"
                  spellCheck={false}
                  value={src.user}
                  onChange={(e) => set({ user: e.target.value })}
                />
                <p className="text-xs text-muted-foreground">
                  Pterodactyl and Pelican show it on the server's Settings page, as SFTP details.
                </p>
              </div>
              <div className="flex flex-col gap-1.5">
                <div className="flex items-center justify-between">
                  <Label htmlFor={useKey ? "imp-key" : "imp-password"}>
                    {useKey ? "Private key" : "Password"}
                  </Label>
                  <button
                    type="button"
                    className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                    onClick={() => setUseKey(!useKey)}
                  >
                    {useKey ? "Use a password instead" : "Use a private key instead"}
                  </button>
                </div>
                {useKey ? (
                  <textarea
                    id="imp-key"
                    required
                    rows={4}
                    spellCheck={false}
                    placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                    className="rounded-lg border border-input bg-transparent px-2.5 py-2 font-mono text-xs outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
                    value={src.private_key}
                    onChange={(e) => set({ private_key: e.target.value })}
                  />
                ) : (
                  <Input
                    id="imp-password"
                    type="password"
                    required
                    autoComplete="off"
                    value={src.password}
                    onChange={(e) => set({ password: e.target.value })}
                  />
                )}
                <p className="text-xs text-muted-foreground">
                  The node uses it for this import only and doesn't keep it.
                </p>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="imp-path">Folder on the host</Label>
                <Input
                  id="imp-path"
                  placeholder="Where the login starts"
                  autoComplete="off"
                  spellCheck={false}
                  value={src.path}
                  onChange={(e) => set({ path: e.target.value })}
                />
              </div>
            </>
          ) : (
            <div className="flex flex-col gap-3">
              <div className="flex gap-3 rounded-lg border p-3">
                <KeyRound className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                <div className="flex min-w-0 flex-col gap-1">
                  <p className="text-sm font-medium">
                    {src.host}:{src.port}'s key
                  </p>
                  <code className="text-xs break-all">{hostKey.fingerprint}</code>
                </div>
              </div>
              <p className="text-sm text-muted-foreground">
                If your old host shows its SFTP key's fingerprint, check it's this one: then nothing
                in between can read the files or the password. Then import.
              </p>
            </div>
          )}

          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            {hostKey ? (
              <Button type="button" variant="outline" onClick={() => setHostKey(undefined)}>
                <ArrowLeft /> Back
              </Button>
            ) : (
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
            )}
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 className="animate-spin" /> : hostKey ? <FolderInput /> : null}
              {hostKey ? "Import" : "Continue"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
