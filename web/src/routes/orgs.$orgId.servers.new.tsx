import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { BadgeCheck, Search } from "lucide-react";
import { type FormEvent, useMemo, useState } from "react";

import { PageHeader } from "@/components/page";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { type CatalogEgg, CatalogService } from "@/gen/raptor/panel/v1/catalog_pb";
import { type Node, OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { isAdmin } from "@/lib/format";
import {
  createParams,
  eggsFor,
  missingRequired,
  suggestMemoryMiB,
  suggestPort,
} from "@/lib/servers";
import { sendSigned } from "@/lib/signed";
import { catalogClient, orgClient } from "@/lib/transport";
import { cn } from "@/lib/utils";
import { passkeyCancelled } from "@/lib/webauthn";

type NewServerSearch = { node?: string };

export const Route = createFileRoute("/orgs/$orgId/servers/new")({
  validateSearch: (s: Record<string, unknown>): NewServerSearch => ({
    node: typeof s.node === "string" ? s.node : undefined,
  }),
  component: NewServerPage,
});

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

const categoryNames: Record<string, string> = {
  minecraft: "Minecraft",
  steam: "Steam",
  software: "Software",
  standalone: "Games",
  database: "Databases",
  generic: "Generic",
  voice: "Voice",
  imported: "Imported by your org",
};

function NewServerPage() {
  const session = Route.useRouteContext();
  const { orgId } = Route.useParams();
  const search = Route.useSearch();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const org = orgs.data?.orgs.find((o) => o.id === orgId);
  const nodes = useQuery(OrgService.method.listNodes, { orgId });
  const [nodeId, setNodeId] = useState(search.node ?? "");
  const node = nodes.data?.nodes.find((n) => n.id === (nodeId || nodes.data?.nodes[0]?.id));

  return (
    <>
      <PageHeader
        back={{ label: "Servers", to: "/orgs/$orgId/servers", params: { orgId } }}
        title="Deploy a server"
        description="Pick a node and a game; Raptor installs it and starts it."
      />
      {org && !isAdmin(org.role) ? (
        <p className="text-sm text-muted-foreground">
          Only the org's admins and owners can create servers.
        </p>
      ) : nodes.data?.nodes.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          Add a node first: servers run on your own machines.
        </p>
      ) : (
        <div className="flex flex-col gap-4">
          {(nodes.data?.nodes.length ?? 0) > 1 && (
            <Card>
              <CardHeader>
                <CardTitle>Node</CardTitle>
                <CardDescription>The machine the server runs on.</CardDescription>
              </CardHeader>
              <CardContent>
                <select
                  aria-label="Node"
                  className={select}
                  value={node?.id ?? ""}
                  onChange={(e) => setNodeId(e.target.value)}
                >
                  {nodes.data?.nodes.map((n) => (
                    <option key={n.id} value={n.id}>
                      {n.name}
                      {n.connected ? "" : " (offline)"}
                    </option>
                  ))}
                </select>
              </CardContent>
            </Card>
          )}
          {node && (
            <NewServer key={node.id} orgId={orgId} node={node} userId={session.user?.id ?? ""} />
          )}
        </div>
      )}
    </>
  );
}

function NewServer({ orgId, node, userId }: { orgId: string; node: Node; userId: string }) {
  const catalog = useQuery(CatalogService.method.listEggs, {});
  const imported = useQuery(OrgService.method.listOrgEggs, { orgId });
  const servers = useQuery(OrgService.method.listServers, { orgId, nodeId: node.id });
  const [query, setQuery] = useState("");
  const [egg, setEgg] = useState<CatalogEgg>();

  const eggs = useMemo(() => {
    const q = query.trim().toLowerCase();
    const own = (imported.data?.eggs ?? []).flatMap((o) => (o.egg ? [o.egg] : []));
    // The org's own eggs first: someone imported them to use them.
    return [...eggsFor(own, node.arch), ...eggsFor(catalog.data?.eggs ?? [], node.arch)].filter(
      (e) => !q || e.name.toLowerCase().includes(q) || e.category.includes(q),
    );
  }, [catalog.data, imported.data, node.arch, query]);
  const usedPorts = (servers.data?.servers ?? []).flatMap((s) => s.ports);

  if (egg) {
    return (
      <Settings
        orgId={orgId}
        node={node}
        userId={userId}
        egg={egg}
        usedPorts={usedPorts}
        onBack={() => setEgg(undefined)}
      />
    );
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>Game</CardTitle>
        <CardDescription>
          {node.arch
            ? `Everything here runs on ${node.name} (${node.arch}).`
            : "Pick what the server runs."}{" "}
          Certified games are tested with every Raptor release.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="relative">
          <Search className="absolute top-2 left-2.5 size-4 text-muted-foreground" />
          <Input
            aria-label="Search games"
            placeholder="Search"
            className="pl-8"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        {catalog.error && <p className="text-sm text-destructive">{message(catalog.error)}</p>}
        {catalog.isPending && <p className="text-sm text-muted-foreground">Loading games…</p>}
        {catalog.data && eggs.length === 0 && (
          <p className="text-sm text-muted-foreground">Nothing matches.</p>
        )}
        <div className="grid gap-2 sm:grid-cols-2">
          {eggs.map((e) => (
            <button
              type="button"
              key={e.id}
              onClick={() => setEgg(e)}
              className="flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition-colors hover:bg-muted/50"
            >
              <span className="flex items-center gap-1.5 font-medium">
                {e.name}
                {e.certified && (
                  <BadgeCheck className="size-4 text-primary" aria-label="Certified" />
                )}
              </span>
              <span className="text-xs text-muted-foreground">
                {categoryNames[e.category] ?? e.category}
              </span>
            </button>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

function Settings({
  orgId,
  node,
  userId,
  egg,
  usedPorts,
  onBack,
}: {
  orgId: string;
  node: Node;
  userId: string;
  egg: CatalogEgg;
  usedPorts: number[];
  onBack: () => void;
}) {
  const navigate = useNavigate();
  const client = useQueryClient();
  const editable = egg.variables.filter((v) => v.userEditable);
  const defaults = Object.fromEntries(editable.map((v) => [v.env, v.default]));
  const [name, setName] = useState(egg.name);
  const [image, setImage] = useState(egg.images[0]?.ref ?? "");
  const [values, setValues] = useState<Record<string, string>>(defaults);
  const [port, setPort] = useState(() => suggestPort(egg.category, usedPorts));
  const [memoryGiB, setMemoryGiB] = useState(
    () => suggestMemoryMiB(egg.category, Number(node.memoryBytes)) / 1024,
  );
  const [diskGiB, setDiskGiB] = useState("");
  const [eula, setEula] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const needsEula = egg.features.includes("eula");
  const missing = missingRequired(editable, values);
  // Variables that must be filled are always shown; the rest (versions,
  // jar names) work as they are, so they wait behind "Game settings".
  const required = editable.filter((v) => v.rules.includes("required") && !v.default);
  const optional = editable.filter((v) => !required.includes(v));
  const portTaken = usedPorts.includes(port);

  async function create(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const file = egg.id.startsWith("org:")
        ? await orgClient.getOrgEgg({ orgId, eggId: egg.id })
        : await catalogClient.getEgg({ id: egg.id });
      await sendSigned({
        userId,
        nodeId: node.id,
        action: "server.create",
        params: createParams(
          {
            name,
            eggId: egg.id,
            egg: file.egg,
            image,
            variables: values,
            port,
            memoryMiB: Math.round(memoryGiB * 1024),
            diskMiB: diskGiB ? Math.round(Number(diskGiB) * 1024) : 0,
            acceptEula: eula,
          },
          defaults,
        ),
      });
      await client.invalidateQueries();
      await navigate({ to: "/orgs/$orgId/nodes/$nodeId", params: { orgId, nodeId: node.id } });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const field = (v: (typeof editable)[number]) => (
    <div key={v.env} className="flex flex-col gap-1.5">
      <Label htmlFor={`var-${v.env}`}>{v.name}</Label>
      <Input
        id={`var-${v.env}`}
        value={values[v.env] ?? ""}
        placeholder={v.default}
        aria-invalid={missing.includes(v) || undefined}
        onChange={(e) => setValues({ ...values, [v.env]: e.target.value })}
      />
      {v.description && <p className="text-xs text-muted-foreground">{v.description}</p>}
    </div>
  );

  return (
    <form onSubmit={create} className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            {egg.name}
            {egg.certified && <Badge variant="secondary">Certified</Badge>}
            {egg.category === "imported" && <Badge variant="outline">Imported</Badge>}
          </CardTitle>
          <CardDescription>
            {egg.description.length > 280 ? `${egg.description.slice(0, 280)}…` : egg.description}{" "}
            {egg.sourceUrl && (
              <a href={egg.sourceUrl} target="_blank" rel="noreferrer" className="underline">
                Egg source
              </a>
            )}
          </CardDescription>
          <CardAction>
            <Button type="button" variant="outline" size="sm" onClick={onBack}>
              Change
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="name">Name</Label>
            <Input
              id="name"
              required
              maxLength={64}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          {egg.images.length > 1 && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="image">Runtime</Label>
              <select
                id="image"
                className={select}
                value={image}
                onChange={(e) => setImage(e.target.value)}
              >
                {egg.images.map((i) => (
                  <option key={i.ref} value={i.ref}>
                    {i.name}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">
                The image the server runs in, such as its Java version. The first is the egg's
                choice.
              </p>
            </div>
          )}
          {required.map(field)}
          <div className="grid gap-4 sm:grid-cols-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="port">Port</Label>
              <Input
                id="port"
                type="number"
                required
                min={1024}
                max={65535}
                value={port}
                aria-invalid={portTaken || undefined}
                onChange={(e) => setPort(Number(e.target.value))}
              />
              <p
                className={cn("text-xs", portTaken ? "text-destructive" : "text-muted-foreground")}
              >
                {portTaken
                  ? "Another server on this node uses it."
                  : "Players connect to this port."}
              </p>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="memory">Memory (GB)</Label>
              <Input
                id="memory"
                type="number"
                required
                min={0.0625}
                step="any"
                value={memoryGiB}
                onChange={(e) => setMemoryGiB(Number(e.target.value))}
              />
              {node.memoryBytes > 0n && (
                <p className="text-xs text-muted-foreground">
                  The node has {(Number(node.memoryBytes) / 2 ** 30).toFixed(1)} GB.
                </p>
              )}
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="disk">Disk limit (GB)</Label>
              <Input
                id="disk"
                type="number"
                min={1}
                placeholder="No limit"
                value={diskGiB}
                onChange={(e) => setDiskGiB(e.target.value)}
              />
            </div>
          </div>
          {optional.length > 0 && (
            <details className="rounded-lg border p-3">
              <summary className="cursor-pointer text-sm font-medium">
                Game settings ({optional.length})
              </summary>
              <p className="mt-1 mb-3 text-xs text-muted-foreground">
                These work as they are. You can change them later.
              </p>
              <div className="flex flex-col gap-4">{optional.map(field)}</div>
            </details>
          )}
        </CardContent>
      </Card>
      {needsEula && (
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-0.5"
            checked={eula}
            onChange={(e) => setEula(e.target.checked)}
          />
          <span>
            I accept the{" "}
            {egg.category === "minecraft" ? (
              <a
                href="https://aka.ms/MinecraftEULA"
                target="_blank"
                rel="noreferrer"
                className="underline"
              >
                Minecraft EULA
              </a>
            ) : (
              "game's EULA"
            )}
            . The server won't start without it.
          </span>
        </label>
      )}
      {!node.connected && (
        <Alert>
          <AlertDescription>
            {node.name} is offline. Creating a server needs it connected.
          </AlertDescription>
        </Alert>
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}
      {missing.length > 0 && (
        <p className="text-sm text-muted-foreground">
          Fill in {missing.map((v) => v.name).join(", ")} first.
        </p>
      )}
      <div className="flex flex-col gap-1.5">
        <Button
          type="submit"
          className="self-start"
          disabled={
            busy || !node.connected || missing.length > 0 || portTaken || (needsEula && !eula)
          }
        >
          {busy ? "Creating…" : "Create with a passkey"}
        </Button>
        <p className="text-xs text-muted-foreground">
          Your passkey signs exactly this server, so the node knows it's you. It installs, then
          starts.
        </p>
      </div>
    </form>
  );
}
