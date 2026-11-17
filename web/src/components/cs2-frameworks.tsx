import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Puzzle } from "lucide-react";
import { useMemo, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { EmptyState } from "@/components/page";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import {
  change,
  type Frameworks,
  frameworks,
  hasFrameworks,
  metamodLoads,
  versionError,
} from "@/lib/cs2-frameworks";
import { message } from "@/lib/errors";
import { can } from "@/lib/org-data";
import { type ServerConfig, updateParams } from "@/lib/servers";
import { commandClient } from "@/lib/transport";

// Counter-Strike 2's Frameworks section (#414, docs/EGGS.md#cs2-frameworks):
// switches for Metamod:Source and CounterStrikeSharp over the egg's
// variables. The egg's install downloads them, so turning one on (or a new
// version) saves and reinstalls over the files, which backs them up first.

export function FrameworksPage({
  orgId,
  nodeId,
  serverId,
  server,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  server: Server;
}) {
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const cfg = useMemo(
    () => (res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined),
    [res.data?.configJson],
  );
  if (res.error) return <p className="text-sm text-destructive">{message(res.error)}</p>;
  if (!cfg) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const vars = cfg.egg?.variables ?? [];
  if (!hasFrameworks(vars.map((v) => v.env))) {
    return (
      <EmptyState
        icon={Puzzle}
        title="This server's egg doesn't install plugin frameworks"
        description="Counter-Strike 2 servers on the egg library's egg have this page. Moving it to the egg's new version adds it."
      />
    );
  }
  const defaults = Object.fromEntries(vars.map((v) => [v.env, v.default]));
  const saved = frameworks(cfg.variables ?? undefined, defaults);
  return (
    <FrameworksForm
      key={JSON.stringify(saved)}
      nodeId={nodeId}
      serverId={serverId}
      server={server}
      cfg={cfg}
      name={res.data?.server?.name ?? server.name}
      saved={saved}
    />
  );
}

function FrameworksForm({
  nodeId,
  serverId,
  server,
  cfg,
  name,
  saved,
}: {
  nodeId: string;
  serverId: string;
  server: Server;
  cfg: ServerConfig;
  name: string;
  saved: Frameworks;
}) {
  const client = useQueryClient();
  const { confirm } = useConfirm();
  const [f, setF] = useState(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const canChange = can(server, "startup");
  const canReinstall = can(server, "reinstall");
  const stopped = ["offline", "crashed", "install_failed", ""].includes(server.state);
  const { variables, reinstall } = change(saved, f);
  const changed = Object.keys(variables).length > 0;
  const invalid =
    (metamodLoads(f) && versionError("metamod", f.metamodVersion)) ||
    (f.css && versionError("css", f.cssVersion));

  async function save() {
    if (reinstall && canReinstall) {
      const ok = await confirm({
        title: "Install the frameworks?",
        description:
          "Raptor backs up the server's files, then reinstalls it: the game is checked for updates and the frameworks are downloaded into game/csgo/addons. Maps, configs, and plugins stay.",
        confirm: "Save and reinstall",
      });
      if (!ok) return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      const primary = cfg.allocations.find((a) => a.primary) ?? cfg.allocations[0];
      await commandClient.execute({
        nodeId,
        action: "server.update",
        serverId,
        paramsJson: JSON.stringify(
          updateParams(cfg, {
            name,
            image: cfg.image,
            variables,
            memoryMiB: cfg.limits.memory_mib,
            diskMiB: cfg.limits.disk_mib,
            port: primary?.port ?? 0,
          }),
        ),
      });
      if (!reinstall) {
        setDone("Saved. It takes effect the next time the server starts.");
      } else if (canReinstall) {
        await commandClient.execute({ nodeId, action: "server.reinstall", serverId });
        setDone("Installing: the files are backed up first. The console shows its progress.");
      } else {
        setDone("Saved. Someone who can reinstall the server installs them from Settings.");
      }
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const row = (
    id: string,
    title: string,
    description: string,
    checked: boolean,
    onChecked: (on: boolean) => void,
    version: string,
    onVersion: (v: string) => void,
    which: "metamod" | "css",
    disabled = false,
  ) => {
    const err = checked ? versionError(which, version) : "";
    return (
      <div className="flex flex-col gap-3 py-3 first:pt-0 last:pb-0">
        <div className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-0.5">
            <Label htmlFor={id}>{title}</Label>
            <p className="text-sm text-muted-foreground">{description}</p>
          </div>
          <Switch
            id={id}
            checked={checked}
            disabled={!canChange || busy || disabled}
            onCheckedChange={onChecked}
          />
        </div>
        {checked && (
          <div className="flex flex-col gap-1.5 sm:max-w-xs">
            <Label htmlFor={`${id}-version`} className="text-muted-foreground">
              Version
            </Label>
            <Input
              id={`${id}-version`}
              value={version}
              disabled={!canChange || busy}
              aria-invalid={!!err}
              onChange={(e) => onVersion(e.target.value.trim())}
            />
            <p className={err ? "text-sm text-destructive" : "text-xs text-muted-foreground"}>
              {err || "latest is the newest, updated at each reinstall."}
            </p>
          </div>
        )}
      </div>
    );
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Frameworks</CardTitle>
        <CardDescription>
          Server plugins for Counter-Strike 2 run on Metamod:Source, and most on CounterStrikeSharp
          too. Raptor installs them into game/csgo/addons and loads Metamod at every start, so a
          game update doesn't undo it. After a game update breaks them, reinstall to get their
          newest builds.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-col divide-y">
          {row(
            "fw-metamod",
            "Metamod:Source",
            f.css && !f.metamod
              ? "Loaded anyway, because CounterStrikeSharp runs on it."
              : "The plugin loader every CS2 server plugin needs.",
            f.metamod || f.css,
            (on) => setF({ ...f, metamod: on }),
            f.metamodVersion,
            (v) => setF({ ...f, metamodVersion: v }),
            "metamod",
            f.css,
          )}
          {row(
            "fw-css",
            "CounterStrikeSharp",
            "Runs C# plugins put in game/csgo/addons/counterstrikesharp/plugins.",
            f.css,
            (on) => setF({ ...f, css: on }),
            f.cssVersion,
            (v) => setF({ ...f, cssVersion: v }),
            "css",
          )}
        </div>
        {canChange ? (
          <div className="flex flex-wrap items-center gap-3">
            <Button
              disabled={busy || !changed || !!invalid || (reinstall && canReinstall && !stopped)}
              onClick={save}
            >
              {busy && <Loader2 className="animate-spin" />}
              {reinstall && canReinstall ? "Save and reinstall" : "Save"}
            </Button>
            {reinstall && canReinstall && !stopped && (
              <span className="text-sm text-muted-foreground">Stop the server first.</span>
            )}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            Changing them needs the Startup permission.
          </p>
        )}
        {done && <p className="text-sm text-muted-foreground">{done}</p>}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
