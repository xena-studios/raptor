import { useQueryClient } from "@tanstack/react-query";
import { Download, Globe, Loader2 } from "lucide-react";
import { type FormEvent, useState } from "react";

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
import { type DBInstance, downloadCA, engineName, instancesKey } from "@/lib/databases";
import { message } from "@/lib/errors";
import { sendSigned } from "@/lib/signed";
import { passkeyCancelled } from "@/lib/webauthn";

// ExternalDialog turns an instance's external access on, changes it, or
// turns it off (docs/DATABASES.md#external-access): a port on the node,
// reachable only from the addresses listed, with TLS required. It's signed,
// since it opens a port.
export function ExternalDialog({
  nodeId,
  userId,
  instance: i,
  onClose,
}: {
  nodeId: string;
  userId: string;
  instance: DBInstance;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const on = !!i.external;
  const [port, setPort] = useState(String(i.external?.port ?? suggestedPort(i)));
  const [allow, setAllow] = useState((i.external?.allow ?? []).join("\n"));
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const list = allow
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
  const anywhere = list.some((a) => a === "0.0.0.0/0" || a === "::/0");

  async function save(params: { port: number; allow: string[] }, what: string) {
    setBusy(what);
    setError("");
    try {
      await sendSigned({
        userId,
        nodeId,
        action: "node.db.instance.external",
        params: { id: i.id, ...params },
      });
      await client.invalidateQueries({ queryKey: instancesKey(nodeId) });
      onClose();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy("");
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    void save({ port: Number(port), allow: list }, "save");
  }

  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>External access to {i.name}</DialogTitle>
            <DialogDescription>
              For a desktop tool or an app somewhere else. The instance gets a port on this node
              that only the addresses you list can reach, and connections from outside must use TLS.
              Servers on its network keep connecting as they do now.
            </DialogDescription>
          </DialogHeader>

          {on && i.external && (
            <div className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-3 text-sm">
              <p className="font-medium">Connect with</p>
              <code className="font-mono text-xs break-all">
                {i.external.host
                  ? `${i.external.host}:${i.external.port}`
                  : `Port ${i.external.port} on the node's public address`}
              </code>
              <p className="text-xs text-muted-foreground">
                As the database's own user, with TLS.{" "}
                {i.external.host
                  ? `To check you're talking to it, give your tool the certificate below (in ${i.engine === "postgres" ? "psql, sslmode=verify-full and sslrootcert" : "the mysql client, --ssl-ca and --ssl-mode=VERIFY_IDENTITY"}).`
                  : "Give your tool the certificate below to check you're talking to it."}
              </p>
              {i.external.ca && (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="self-start"
                  onClick={() => downloadCA(i)}
                >
                  <Download /> Certificate
                </Button>
              )}
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-[8rem_1fr]">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="external-port">Port</Label>
              <Input
                id="external-port"
                type="number"
                min={1024}
                max={65535}
                required
                value={port}
                onChange={(e) => setPort(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="external-allow">Who may connect</Label>
              <textarea
                id="external-allow"
                rows={3}
                spellCheck={false}
                placeholder={"203.0.113.7\n198.51.100.0/24"}
                className="min-h-16 rounded-lg border border-input bg-transparent px-2.5 py-1.5 font-mono text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
                value={allow}
                onChange={(e) => setAllow(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                Addresses or ranges, one per line: your home or office's public address, or your
                app's server.
              </p>
            </div>
          </div>
          {anywhere && (
            <p className="text-sm text-destructive">
              0.0.0.0/0 lets the whole internet try to log in. Its users' passwords and TLS still
              stand between them and the data, but listing your own addresses is much safer.
            </p>
          )}

          {error && <p className="text-sm text-destructive">{error}</p>}
          <p className="text-xs text-muted-foreground">
            {engineName(i.engine)} restarts for it, so servers using it reconnect within seconds.
          </p>
          <DialogFooter className="sm:justify-between">
            {on ? (
              <Button
                type="button"
                variant="outline"
                disabled={!!busy}
                onClick={() => void save({ port: 0, allow: [] }, "off")}
              >
                {busy === "off" && <Loader2 className="animate-spin" />} Turn off
              </Button>
            ) : (
              <span />
            )}
            <div className="flex gap-2">
              <Button type="button" variant="outline" disabled={!!busy} onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" disabled={!!busy || list.length === 0}>
                {busy === "save" ? <Loader2 className="animate-spin" /> : <Globe />}
                {on ? "Save with passkey" : "Turn on with passkey"}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// suggestedPort is a port unlikely to be taken: the engine's own, moved
// up out of the way of anything else on the machine using it.
function suggestedPort(i: DBInstance): number {
  return i.engine === "postgres" ? 15432 : 13306;
}
