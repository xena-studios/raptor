import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Loader2 } from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { defaultNetworkName, useNodeNetworks } from "@/lib/networks";
import type { ServerConfig } from "@/lib/servers";
import { commandClient } from "@/lib/transport";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// ServerNetworks puts a server on the node's networks (server.networks,
// admins): the one it's on, and, for a proxy, others it also joins. It
// applies at the server's next start.
export function ServerNetworks({
  orgId,
  nodeId,
  serverId,
  running,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  running: boolean;
}) {
  const client = useQueryClient();
  const nets = useNodeNetworks(nodeId);
  const server = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const cfg = useMemo(
    () =>
      server.data?.configJson ? (JSON.parse(server.data.configJson) as ServerConfig) : undefined,
    [server.data?.configJson],
  );
  const current = cfg?.networks ?? [];
  const [primary, setPrimary] = useState<string>();
  const [extra, setExtra] = useState<string[]>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  const list = nets.data?.networks ?? [];
  const shownPrimary = primary ?? current[0] ?? "";
  const shownExtra = (extra ?? current.slice(1)).filter((id) => id !== shownPrimary);
  const next = shownPrimary ? [shownPrimary, ...shownExtra] : [];
  const changed = next.join() !== current.join();

  // Nothing to choose until the node has a network.
  if (!nets.data || list.length === 0 || !cfg) return null;

  async function save() {
    setBusy(true);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        serverId,
        action: "server.networks",
        paramsJson: JSON.stringify({ networks: next }),
      });
      setSaved(true);
      setPrimary(undefined);
      setExtra(undefined);
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const aliases = cfg.aliases ?? [];
  return (
    <Card>
      <CardHeader>
        <CardTitle>Networks</CardTitle>
        <CardDescription>
          Servers reach the others on their network, and nothing on the rest.{" "}
          <Link
            to="/orgs/$orgId/nodes/$nodeId/networks"
            params={{ orgId, nodeId }}
            className="underline underline-offset-2"
          >
            Manage the node's networks
          </Link>
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {cfg.host_network ? (
          <p className="text-sm text-muted-foreground">
            It's on the machine's own network, so it isn't on any other.
          </p>
        ) : (
          <>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="server-network">Network</Label>
              <select
                id="server-network"
                className={select}
                value={shownPrimary}
                onChange={(e) => {
                  setPrimary(e.target.value);
                  setSaved(false);
                }}
              >
                <option value="">{defaultNetworkName}</option>
                {list.map((n) => (
                  <option key={n.id} value={n.id}>
                    {n.name}
                  </option>
                ))}
              </select>
            </div>
            {shownPrimary && list.length > 1 && (
              <details className="rounded-lg border p-3" open={shownExtra.length > 0}>
                <summary className="cursor-pointer text-sm font-medium">Also on</summary>
                <p className="mt-1 mb-3 text-xs text-muted-foreground">
                  For a proxy: it also reaches the servers on these, and they reach it.
                </p>
                <div className="flex flex-col gap-2">
                  {list
                    .filter((n) => n.id !== shownPrimary)
                    .map((n) => (
                      <label key={n.id} className="flex items-center gap-2 text-sm">
                        <Checkbox
                          checked={shownExtra.includes(n.id)}
                          onCheckedChange={(on) => {
                            setExtra(
                              on ? [...shownExtra, n.id] : shownExtra.filter((x) => x !== n.id),
                            );
                            setSaved(false);
                          }}
                        />
                        {n.name}
                      </label>
                    ))}
                </div>
              </details>
            )}
            {current.some((id) => list.find((n) => n.id === id)?.addresses?.[serverId]) && (
              <p className="text-xs text-muted-foreground">
                Its fixed addresses:{" "}
                {current
                  .map((id) => list.find((n) => n.id === id))
                  .filter((n) => n?.addresses?.[serverId])
                  .map((n, i) => (
                    <span key={n?.id}>
                      {i > 0 && ", "}
                      <code className="font-mono">{n?.addresses?.[serverId]}</code> on {n?.name}
                    </span>
                  ))}
                .
              </p>
            )}
            {aliases.length > 0 && current.length > 0 && (
              <p className="text-xs text-muted-foreground">
                Other servers on its networks reach it at{" "}
                {aliases.map((a, i) => (
                  <span key={a}>
                    {i > 0 && " or "}
                    <code className="font-mono">{a}</code>
                  </span>
                ))}
                .
              </p>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            {saved && !changed && (
              <p className="text-sm text-muted-foreground">
                Saved.{" "}
                {running ? "It moves at its next restart." : "It's on them from its next start."}
              </p>
            )}
            <Button className="self-start" disabled={!changed || busy} onClick={save}>
              {busy && <Loader2 className="animate-spin" />} Save networks
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  );
}
