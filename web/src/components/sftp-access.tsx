import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Check, Copy, FolderKey } from "lucide-react";
import { useState } from "react";

import { DetailList } from "@/components/page";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { type Node, OrgService, type SFTPAccess } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { commandClient, orgClient } from "@/lib/transport";

const select =
  "h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

const lifetimes: [number, string][] = [
  [3600, "1 hour"],
  [86400, "1 day"],
  [7 * 86400, "7 days"],
  [30 * 86400, "30 days"],
];

function CopyValue({ value, secret }: { value: string; secret?: boolean }) {
  const [copied, setCopied] = useState(false);
  return (
    <span className="inline-flex items-center gap-1">
      <code className={secret ? "font-semibold" : undefined}>{value}</code>
      <Button
        size="icon-xs"
        variant="ghost"
        aria-label="Copy"
        onClick={async () => {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
      >
        {copied ? <Check /> : <Copy />}
      </Button>
    </span>
  );
}

// SFTPButton is the file manager's SFTP button, with a green dot while
// the user's login is on; its dialog turns SFTP on for the user on one server: a generated username
// and password that work until they turn it off or it runs out (a day
// unless they pick otherwise). The password is shown once.
export function SFTPButton({
  orgId,
  node,
  serverId,
  admin,
}: {
  orgId: string;
  node: Node;
  serverId: string;
  admin: boolean;
}) {
  const client = useQueryClient();
  const current = useQuery(OrgService.method.getSFTPAccess, {
    orgId,
    nodeId: node.id,
    serverId,
  });
  const [open, setOpen] = useState(false);
  const [ttl, setTtl] = useState(86400);
  const [created, setCreated] = useState<{ access: SFTPAccess; password: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const access = created?.access ?? current.data?.access;

  async function act(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await fn();
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const turnOn = () =>
    act(async () => {
      const res = await orgClient.createSFTPAccess({
        orgId,
        nodeId: node.id,
        serverId,
        ttlSeconds: BigInt(ttl),
      });
      if (res.access) setCreated({ access: res.access, password: res.password });
    });

  const turnOff = () =>
    act(async () => {
      const res = await orgClient.revokeSFTPAccess({ orgId, nodeId: node.id, serverId });
      setCreated(null);
      // End sessions it opened, too. The password already doesn't work.
      if (res.username) {
        await commandClient
          .execute({
            nodeId: node.id,
            action: "sftp.disconnect",
            serverId,
            paramsJson: JSON.stringify({ username: res.username }),
          })
          .catch(() => {});
      }
    });

  const active = node.sftpAllowed && !!access;

  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        <FolderKey /> SFTP
        {active && (
          <>
            <span aria-hidden className="size-2 rounded-full bg-emerald-500" />
            <span className="sr-only">(on)</span>
          </>
        )}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          // The password is shown once: closing forgets it.
          if (!next) setCreated(null);
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              SFTP
              {active && (
                <span className="flex items-center gap-1.5 text-xs font-normal text-muted-foreground">
                  <span className="size-2 rounded-full bg-emerald-500" /> On
                </span>
              )}
            </DialogTitle>
            <DialogDescription>
              For big uploads and your own file apps (FileZilla, WinSCP, Cyberduck). Turn it on when
              you need it: you get a username and password that stop working when you turn it off or
              when they run out. The node's SFTP port is closed until someone needs it.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-3 text-sm">
            {!node.sftpAllowed ? (
              admin ? (
                <div className="flex flex-wrap items-center gap-3">
                  <span className="text-muted-foreground">SFTP is stopped on {node.name}.</span>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onClick={() =>
                      act(async () => {
                        await orgClient.setNodeSFTP({ orgId, nodeId: node.id, allowed: true });
                      })
                    }
                  >
                    Allow SFTP on this node
                  </Button>
                </div>
              ) : (
                <p className="text-muted-foreground">
                  An admin has stopped SFTP on {node.name}. Ask them to allow it.
                </p>
              )
            ) : access ? (
              <>
                <DetailList
                  rows={[
                    ["Host", <CopyValue key="h" value={access.host} />],
                    [
                      "Port",
                      access.port ? (
                        <CopyValue key="p" value={String(access.port)} />
                      ) : (
                        <span key="p" className="text-muted-foreground">
                          Opens when {node.name} is back online
                        </span>
                      ),
                    ],
                    ["Username", <CopyValue key="u" value={access.username} />],
                    [
                      "Password",
                      created ? (
                        <CopyValue key="pw" value={created.password} secret />
                      ) : (
                        <span key="pw" className="text-muted-foreground">
                          Shown once, when it was made
                        </span>
                      ),
                    ],
                    [
                      "Works until",
                      access.expiresAt ? timestampDate(access.expiresAt).toLocaleString() : "",
                    ],
                  ]}
                />
                {created && (
                  <p className="text-muted-foreground">
                    Save the password now: it won't be shown again.
                  </p>
                )}
                {access.hostKeyFingerprint && (
                  <p className="text-xs text-muted-foreground">
                    The first time you connect, your app shows the server's key: check it's{" "}
                    <code>{access.hostKeyFingerprint}</code>.
                  </p>
                )}
                <div className="flex flex-wrap gap-2">
                  <Button size="sm" variant="outline" disabled={busy} onClick={turnOn}>
                    New password
                  </Button>
                  <Button size="sm" variant="destructive" disabled={busy} onClick={turnOff}>
                    Turn off
                  </Button>
                </div>
              </>
            ) : (
              <div className="flex flex-wrap items-center gap-2">
                <label className="flex items-center gap-2 text-muted-foreground">
                  Works for
                  <select
                    className={select}
                    value={ttl}
                    onChange={(e) => setTtl(Number(e.target.value))}
                  >
                    {lifetimes.map(([s, label]) => (
                      <option key={s} value={s}>
                        {label}
                      </option>
                    ))}
                  </select>
                </label>
                <Button size="sm" disabled={busy || current.isPending} onClick={turnOn}>
                  Turn on SFTP
                </Button>
              </div>
            )}
            {error && <p className="text-destructive">{error}</p>}
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
