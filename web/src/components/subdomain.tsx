import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Trash2 } from "lucide-react";
import { type FormEvent, useDeferredValue, useState } from "react";

import { CopyButton } from "@/components/address";
import { StatusBadge } from "@/components/server-status";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { SubdomainService } from "@/gen/raptor/panel/v1/subdomain_pb";
import { message } from "@/lib/errors";

// A server's player subdomain (docs/PANEL.md#subdomains): smp.raptornodes.net
// pointing at its node, with the port built in for Minecraft. Shown only
// while the Panel offers subdomains; admins and owners add and remove it.
export function SubdomainCard({
  orgId,
  nodeId,
  serverId,
  port,
  minecraft,
  admin,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  port?: number;
  minecraft: boolean;
  admin: boolean;
}) {
  const client = useQueryClient();
  const list = useQuery(SubdomainService.method.listSubdomains, { orgId });
  const remove = useMutation(SubdomainService.method.deleteSubdomain);
  const [error, setError] = useState("");
  const domain = list.data?.domain ?? "";
  if (!domain) return null;
  const mine = list.data?.subdomains.find((s) => s.nodeId === nodeId && s.serverId === serverId);
  const full = mine ? `${mine.name}.${domain}` : "";
  const address = full && !minecraft && port ? `${full}:${port}` : full;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Subdomain</CardTitle>
        <CardDescription>
          A free name on {domain} that points at this server's node.
          {minecraft ? " Minecraft players don't need the port." : " Players still add the port."}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
        {mine ? (
          <div className="flex flex-wrap items-center gap-3">
            <span className="inline-flex min-w-0 items-center gap-0.5">
              <code className="truncate text-sm">{address}</code>
              <CopyButton value={address} />
            </span>
            <StatusBadge
              status={
                mine.error
                  ? { label: "DNS failing", tone: "failed" }
                  : mine.live
                    ? { label: "Live", tone: "live" }
                    : { label: "Setting up", tone: "pending" }
              }
            />
            {admin && (
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label={`Remove ${full}`}
                className="ml-auto text-muted-foreground hover:text-destructive"
                disabled={remove.isPending}
                onClick={async () => {
                  setError("");
                  try {
                    await remove.mutateAsync({ orgId, id: mine.id });
                    await client.invalidateQueries();
                  } catch (err) {
                    setError(message(err));
                  }
                }}
              >
                <Trash2 />
              </Button>
            )}
          </div>
        ) : admin ? (
          <ClaimForm orgId={orgId} nodeId={nodeId} serverId={serverId} domain={domain} />
        ) : (
          <p className="text-sm text-muted-foreground">No subdomain. An admin can add one.</p>
        )}
        {mine && !mine.live && !mine.error && (
          <p className="text-xs text-muted-foreground">
            Its records are written within a minute; some networks take a few more to see them.
          </p>
        )}
        {mine?.error && <p className="text-xs text-destructive">{mine.error}</p>}
        {mine && admin && (
          <p className="text-xs text-muted-foreground">
            Removing it keeps the name for your org for 30 days.
          </p>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}

function ClaimForm({
  orgId,
  nodeId,
  serverId,
  domain,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  domain: string;
}) {
  const client = useQueryClient();
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const typed = useDeferredValue(name.trim().toLowerCase());
  const check = useQuery(
    SubdomainService.method.checkSubdomain,
    { orgId, name: typed },
    { enabled: typed.length >= 3 },
  );
  const create = useMutation(SubdomainService.method.createSubdomain);
  const ok = typed.length >= 3 && check.data?.available === true;

  async function claim(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await create.mutateAsync({ orgId, nodeId, serverId, name: typed });
      setName("");
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <form onSubmit={claim} className="flex flex-col gap-1.5">
      <Label htmlFor="subdomain">Name</Label>
      <div className="flex gap-2">
        <div className="flex min-w-0 flex-1 items-center rounded-lg border border-input focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 dark:bg-input/30">
          <Input
            id="subdomain"
            className="border-0 focus-visible:ring-0 dark:bg-transparent"
            placeholder="smp"
            maxLength={32}
            autoComplete="off"
            spellCheck={false}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <span className="shrink-0 pr-2.5 font-mono text-xs text-muted-foreground">.{domain}</span>
        </div>
        <Button type="submit" disabled={!ok || create.isPending}>
          {create.isPending && <Loader2 className="animate-spin" />}
          Add
        </Button>
      </div>
      {typed.length >= 3 && check.data && !check.data.available && (
        <p className="text-xs text-destructive">{check.data.reason}</p>
      )}
      {ok && <p className="text-xs text-ok">Available.</p>}
      {error && <p className="text-sm text-destructive">{error}</p>}
    </form>
  );
}
