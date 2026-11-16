import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { AddonService } from "@/gen/raptor/panel/v1/addon_pb";
import { OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { can } from "@/lib/org-data";
import { type ServerConfig, updateParams } from "@/lib/servers";
import { commandClient } from "@/lib/transport";
import { compareVersions, versionSoftware } from "@/lib/version-switch";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30 sm:max-w-xs";

// VersionSwitch changes the game version a Minecraft server runs (#420):
// a list from the software's own API instead of a version typed into
// Startup, then a reinstall over the files (which backs them up first),
// keeping the world. Going back to an older version is warned about.
export function VersionSwitch({
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
  const client = useQueryClient();
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const cfg = useMemo(
    () => (res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined),
    [res.data?.configJson],
  );
  const sw = cfg
    ? versionSoftware(
        server.eggSource,
        server.eggName,
        (cfg.egg?.variables ?? []).map((v) => v.env),
      )
    : undefined;
  const versions = useQuery(
    AddonService.method.listGameVersions,
    { orgId, source: sw?.source ?? "" },
    { enabled: !!sw },
  );
  const [unstable, setUnstable] = useState(false);
  const [picked, setPicked] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  if (!cfg || !sw) return null;
  const current = cfg.variables?.[sw.variable] ?? "latest";
  const canChange = can(server, "startup");
  const canReinstall = can(server, "reinstall");
  const stopped = ["offline", "crashed", "install_failed", ""].includes(server.state);
  const list = (versions.data?.versions ?? []).filter(
    (v) => unstable || v.stable || v.id === current,
  );
  const target = picked || current;
  const newest = versions.data?.versions.find((v) => v.stable)?.id;
  const older = target !== "latest" && current !== "latest" && compareVersions(target, current) < 0;
  const name = (v: string) => (v === "latest" ? `The newest${newest ? ` (${newest})` : ""}` : v);

  async function change() {
    if (!cfg || !sw) return;
    setBusy(true);
    setError("");
    setDone("");
    try {
      const variables: Record<string, string> = { [sw.variable]: target };
      for (const r of sw.reset) {
        if (cfg.egg?.variables?.some((v) => v.env === r)) variables[r] = "latest";
      }
      const primary = cfg.allocations.find((a) => a.primary) ?? cfg.allocations[0];
      await commandClient.execute({
        nodeId,
        action: "server.update",
        serverId,
        paramsJson: JSON.stringify(
          updateParams(cfg, {
            name: res.data?.server?.name ?? server.name,
            image: cfg.image,
            variables,
            memoryMiB: cfg.limits.memory_mib,
            diskMiB: cfg.limits.disk_mib,
            port: primary?.port ?? 0,
          }),
        ),
      });
      if (canReinstall) {
        await commandClient.execute({ nodeId, action: "server.reinstall", serverId });
        setDone(
          `Installing ${sw.label} ${name(target)}: the files are backed up first. The console shows its progress.`,
        );
      } else {
        setDone(
          `Saved. Someone who can reinstall the server installs ${sw.label} ${name(target)} from Settings.`,
        );
      }
      setPicked("");
      setConfirm(false);
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
        <CardTitle>Version</CardTitle>
        <CardDescription>
          It runs {sw.label} {name(current)}. Changing it backs up the server's files, then
          reinstalls it with the new version; the world and settings stay.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {versions.error ? (
          <p className="text-sm text-destructive">{message(versions.error)}</p>
        ) : !versions.data ? (
          <p className="text-sm text-muted-foreground">Finding {sw.label}'s versions…</p>
        ) : (
          <>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="game-version">Version</Label>
              <select
                id="game-version"
                className={select}
                value={target}
                disabled={!canChange || busy}
                onChange={(e) => setPicked(e.target.value)}
              >
                <option value="latest">{name("latest")}, updated at each reinstall</option>
                {list.map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.id}
                    {v.stable ? "" : " (not a release)"}
                  </option>
                ))}
              </select>
            </div>
            <label className="flex items-center gap-2 text-sm text-muted-foreground">
              <Checkbox checked={unstable} onCheckedChange={(on) => setUnstable(!!on)} />
              Show snapshots and pre-releases
            </label>
            {canChange && (
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  disabled={busy || target === current || !stopped}
                  onClick={() => setConfirm(true)}
                >
                  Change to {name(target)}
                </Button>
                {!stopped && target !== current && (
                  <span className="text-sm text-muted-foreground">Stop the server first.</span>
                )}
              </div>
            )}
          </>
        )}
        {done && <p className="text-sm text-muted-foreground">{done}</p>}
        {error && !confirm && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
      {confirm && (
        <Dialog open onOpenChange={(o) => !o && !busy && setConfirm(false)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                Change to {sw.label} {name(target)}?
              </DialogTitle>
              <DialogDescription>
                {canReinstall
                  ? "Raptor backs up the server's files, then reinstalls it with this version. The world, plugins or mods, and settings stay."
                  : "This saves the version; someone who can reinstall the server installs it from Settings."}
              </DialogDescription>
            </DialogHeader>
            <ul className="flex list-disc flex-col gap-1 pl-5 text-sm text-muted-foreground">
              {older && (
                <li className="text-destructive">
                  {name(target)} is older than {current}. A world opened in a newer version may not
                  load in an older one, or may lose blocks and items. The backup lets you go back.
                </li>
              )}
              <li>
                Plugins and mods made for the old version may need updating (Add-ons shows them).
              </li>
              <li>A newer version may need a newer Java: if so, Overview offers to switch it.</li>
            </ul>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button variant="outline" disabled={busy} onClick={() => setConfirm(false)}>
                Cancel
              </Button>
              <Button
                variant={older ? "destructive" : "default"}
                disabled={busy}
                onClick={() => void change()}
              >
                {busy && <Loader2 className="animate-spin" />}
                {canReinstall ? "Back up and change" : "Save"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </Card>
  );
}
