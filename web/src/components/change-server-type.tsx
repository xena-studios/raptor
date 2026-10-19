import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { cn } from "cn";
import { BadgeCheck, Loader2, Search } from "lucide-react";
import { useMemo, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import type { CatalogEgg } from "@/gen/raptor/panel/v1/catalog_pb";
import { EggService } from "@/gen/raptor/panel/v1/egg_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { categoryName } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { changeEggParams, eggsFor, type ServerConfig } from "@/lib/servers";
import { sendSigned } from "@/lib/signed";
import { eggClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

// ChangeServerType changes what a server runs (its egg) and installs it
// again, keeping its files or starting fresh (Wings' server.update with
// reinstall, docs/SERVERS.md). It changes what code runs, so it's signed
// by the user's passkey; starting fresh takes a backup first.
export function ChangeServerType({
  orgId,
  nodeId,
  serverId,
  userId,
  arch,
  current,
  stopped,
  supported,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  arch: string;
  current: string;
  stopped: boolean;
  // The node's Wings can do it (change-egg).
  supported: boolean;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const server = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const catalog = useQuery(EggService.method.listEggs, { orgId }, { enabled: supported });
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<CatalogEgg>();
  const [wipe, setWipe] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);

  const eggs = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = (catalog.data?.eggs ?? []).filter((e) => e.status === "published");
    return [
      ...eggsFor(
        list.filter((e) => e.scope === "org"),
        arch,
      ),
      ...eggsFor(
        list.filter((e) => e.scope !== "org"),
        arch,
      ),
    ].filter((e) => !q || e.name.toLowerCase().includes(q) || e.category.includes(q));
  }, [catalog.data, arch, query]);

  async function change() {
    if (!picked || !server.data?.configJson) return;
    const ok = await ask.confirm(
      wipe
        ? {
            title: `Start fresh as ${picked.name}?`,
            description:
              "Every file of this server is deleted, after a backup you can restore from, and the new type installs clean.",
            typeToConfirm: server.data.server?.name || undefined,
            confirm: "Change with passkey",
            destructive: true,
          }
        : {
            title: `Change this server to ${picked.name}?`,
            description:
              "The new type's install runs over the files it has, so worlds and configs stay.",
            confirm: "Change with passkey",
          },
    );
    if (!ok) return;
    setBusy(true);
    setError("");
    try {
      const cfg = JSON.parse(server.data.configJson) as ServerConfig;
      const file = await eggClient.getEggFile({ orgId, id: picked.id });
      await sendSigned({
        userId,
        nodeId,
        serverId,
        action: "server.update",
        params: changeEggParams(
          cfg,
          server.data.server?.name ?? "",
          { id: picked.id, file: file.egg },
          wipe,
        ),
      });
      setDone(true);
      setPicked(undefined);
      await client.invalidateQueries();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Server type</CardTitle>
        <CardDescription>
          {current ? `It runs ${current}. ` : ""}Change it to another game or server software, like
          Paper to Purpur or Forge to NeoForge, and it's installed again.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {!supported ? (
          <p className="text-sm text-muted-foreground">
            Update Wings on this node to change a server's type here.
          </p>
        ) : (
          <>
            <div className="relative">
              <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
              <Input
                aria-label="Search games"
                placeholder="Search games"
                className="pl-8"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>
            {catalog.error && <p className="text-sm text-destructive">{message(catalog.error)}</p>}
            {catalog.isPending && <p className="text-sm text-muted-foreground">Loading games…</p>}
            <fieldset className="grid max-h-72 gap-2 overflow-y-auto sm:grid-cols-2">
              <legend className="sr-only">New type</legend>
              {eggs.map((e) => (
                <label
                  key={e.id}
                  className={cn(
                    "flex cursor-pointer flex-col items-start gap-1 rounded-lg border p-3 transition-colors hover:bg-muted/50 has-focus-visible:ring-3 has-focus-visible:ring-ring/50 has-disabled:cursor-default has-disabled:opacity-60 has-disabled:hover:bg-transparent",
                    picked?.id === e.id && "border-primary bg-brand-wash hover:bg-brand-wash",
                  )}
                >
                  <input
                    type="radio"
                    name="change-type-egg"
                    className="sr-only"
                    disabled={e.name === current}
                    checked={picked?.id === e.id}
                    onChange={() => setPicked(e)}
                  />
                  <span className="flex items-center gap-1.5 text-sm font-medium">
                    {e.name}
                    {e.name === current && (
                      <span className="font-mono text-[0.7rem] tracking-wide text-muted-foreground uppercase">
                        Current
                      </span>
                    )}
                    {e.certified && (
                      <BadgeCheck className="size-4 text-muted-foreground" aria-label="Certified" />
                    )}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {e.scope === "org" ? "Your org's · " : ""}
                    {categoryName(e.category)}
                  </span>
                </label>
              ))}
              {catalog.data && eggs.length === 0 && (
                <p className="text-sm text-muted-foreground">Nothing matches.</p>
              )}
            </fieldset>

            {picked && (
              <fieldset className="flex flex-col gap-2">
                <legend className="mb-2 text-sm font-medium">Its files</legend>
                {[
                  {
                    value: false,
                    title: "Keep them and reinstall",
                    about:
                      "The new install runs over the files the server has, so worlds and configs stay. Best between versions of the same game.",
                  },
                  {
                    value: true,
                    title: "Start fresh",
                    about:
                      "Every file is deleted, after a backup you can restore from, and the new type installs clean.",
                  },
                ].map((o) => (
                  <label
                    key={String(o.value)}
                    className={cn(
                      "flex cursor-pointer gap-3 rounded-lg border p-3",
                      wipe === o.value && "border-primary bg-brand-wash",
                    )}
                  >
                    <input
                      type="radio"
                      name="change-type-files"
                      className="mt-1 accent-primary"
                      checked={wipe === o.value}
                      onChange={() => setWipe(o.value)}
                    />
                    <span className="flex flex-col gap-0.5">
                      <span className="text-sm font-medium">{o.title}</span>
                      <span className="text-xs text-muted-foreground">{o.about}</span>
                    </span>
                  </label>
                ))}
              </fieldset>
            )}

            {!stopped && <p className="text-sm text-muted-foreground">Stop the server first.</p>}
            {error && <p className="text-sm text-destructive">{error}</p>}
            {done && !picked && (
              <p className="text-sm text-muted-foreground">
                Changed. It's installing; its console shows how it's going.
              </p>
            )}
            <Button
              className="self-start"
              variant={wipe ? "destructive" : "default"}
              disabled={!picked || !stopped || busy || !server.data?.configJson}
              onClick={change}
            >
              {busy && <Loader2 className="animate-spin" />}
              {picked ? `Change to ${picked.name} with passkey` : "Pick a type to change to"}
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  );
}
