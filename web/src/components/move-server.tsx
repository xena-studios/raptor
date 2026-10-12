import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { diskName, diskSpace, serverDisks, useNodeDisks } from "@/lib/disks";
import { message } from "@/lib/errors";
import type { ServerConfig } from "@/lib/servers";
import { commandClient } from "@/lib/transport";

const select =
  "h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// MoveServer moves a server's files to another disk on its node
// (server.move, admins). It shows only when the node has another disk for
// servers.
export function MoveServer({
  orgId,
  nodeId,
  serverId,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
}) {
  const client = useQueryClient();
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const disks = useNodeDisks(nodeId);
  const [to, setTo] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [queued, setQueued] = useState(false);
  const cfg = res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined;
  const all = serverDisks(disks.data);
  if (!cfg || all.length < 2) return null;
  const current = cfg.disk || "main";
  const here = all.find((d) => d.id === current);
  const others = all.filter((d) => d.id !== current);
  const target = to || others[0]?.id || "";
  const dest = others.find((d) => d.id === target);

  async function move() {
    if (!dest) return;
    const name = res.data?.server?.name ?? "this server";
    if (
      !window.confirm(`Move "${name}" to ${diskName(dest)}? It stops while its files are copied.`)
    )
      return;
    setBusy(true);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        serverId,
        action: "server.move",
        paramsJson: JSON.stringify({ disk: target }),
      });
      setQueued(true);
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Disk</CardTitle>
        <CardDescription>
          On {here ? diskName(here) : `disk ${current}`}. Moving copies the server's files to
          another disk and removes the old copy once the new one is in use. The server stops while
          it moves, and starts again afterwards if it was running.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {queued ? (
          <p className="text-sm text-muted-foreground">
            Moving. The server shows as moving until its files are on the new disk.
          </p>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <select
              aria-label="Move to"
              className={select}
              value={target}
              onChange={(e) => setTo(e.target.value)}
            >
              {others.map((d) => (
                <option key={d.id} value={d.id}>
                  {diskName(d)} · {diskSpace(d)}
                </option>
              ))}
            </select>
            <Button variant="outline" disabled={busy || !dest} onClick={move}>
              Move server
            </Button>
          </div>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
