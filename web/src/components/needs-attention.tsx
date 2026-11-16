import { useQuery } from "@connectrpc/connect-query";
import { Link } from "@tanstack/react-router";
import {
  ArrowUpCircle,
  HardDrive,
  type LucideIcon,
  PackageCheck,
  PackageX,
  ShieldAlert,
  TriangleAlert,
  X,
} from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";

import { useInstalledAddons } from "@/components/addons-data";
import { useLastScan } from "@/components/resource-scan";
import { useEggVersions } from "@/components/server-egg-update";
import { Button, buttonVariants } from "@/components/ui/button";
import { type Node, OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import {
  type AttentionItem,
  addonItems,
  diskItem,
  eggItem,
  installItem,
  scanItem,
  sortItems,
} from "@/lib/attention";
import { addonKinds } from "@/lib/eggs";
import { counts } from "@/lib/fivem";
import { formatBytes, type Metrics } from "@/lib/metrics";
import { can } from "@/lib/org-data";
import { gameVersion, type ServerConfig } from "@/lib/servers";

// NeedsAttention is the one place on a server's Overview that says what's
// wrong or out of date, each with the one button that fixes it (#435): a
// console problem (passed in: the Java switch, or a known error), a failed
// install, a nearly full disk, add-ons missing what they need, add-on
// updates, and a newer egg. What counts, how bad, and who can fix it is
// lib/attention; this fetches. It shows only when there's something in
// it, worst first, and each row can be dismissed until it changes.
//
// Cheap on every visit: the install and disk come from what the Overview
// already has; add-ons and the egg reuse the Add-ons tab's and Settings'
// queries, kept for minutes, and only for viewers who can see them.
export function NeedsAttention({
  orgId,
  nodeId,
  serverId,
  server,
  node,
  metrics,
  console: consoleItem,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  server: Server;
  node: Node | undefined;
  metrics: Metrics | undefined;
  console?: ReactNode;
}) {
  const res = useQuery(
    OrgService.method.getServer,
    { orgId, nodeId, serverId },
    { enabled: can(server, "files.read") || can(server, "startup") },
  );
  const cfg = useMemo(
    () => (res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined),
    [res.data?.configJson],
  );
  // Add-on kinds the viewer can see, on a node that can list them.
  const kinds =
    can(server, "files.read") && node?.connected
      ? Object.keys(cfg?.egg?.addons ?? {}).filter((k) => {
          const cap = addonKinds[k]?.capability;
          return !!addonKinds[k] && (!cap || node.capabilities.includes(cap));
        })
      : [];
  // The egg: admins and owners only, who change it (Settings).
  const eggSource =
    can(server, "*") && node?.capabilities.includes("change-egg") ? server.eggSource : "";
  const egg = useEggVersions(orgId, eggSource, eggSource ? cfg : undefined);
  // The resource scan run on this page this session (#428), if any.
  const scan = useLastScan(nodeId, serverId);
  const findings =
    server.resourceScan && can(server, "files.read") ? (scan.data?.findings ?? []) : [];
  const worst = counts(findings).high ? "high" : "medium";
  const flagged = new Set(findings.filter((f) => f.severity === worst).map((f) => f.file)).size;
  const items = sortItems(
    [
      installItem(server, can(server, "reinstall")),
      metrics &&
        diskItem(
          metrics.latest.disk,
          metrics.limits.disk_bytes,
          can(server, "files.write"),
          formatBytes,
        ),
      scanItem(counts(findings), flagged),
      egg &&
        eggItem({
          name: egg.egg.name,
          latest: egg.latest,
          current: egg.current,
          outdated: egg.outdated,
        }),
    ].filter((i): i is AttentionItem => !!i),
  );
  return (
    // Hidden unless a row renders (CSS :has), so each add-on kind loads
    // its own data without the card knowing ahead. Rows sit in rank order
    // (CSS order) across those parts.
    <section aria-label="Needs attention" className="hidden flex-col gap-2 has-[li]:flex">
      <h2 className="text-sm font-medium">Needs attention</h2>
      <ul className="flex flex-col gap-2">
        {consoleItem && <li className="order-0 list-none">{consoleItem}</li>}
        {items.map((it) => (
          <Row key={it.id} orgId={orgId} serverId={serverId} item={it} />
        ))}
        {cfg &&
          kinds.map((kind) => (
            <AddonItems
              key={kind}
              orgId={orgId}
              nodeId={nodeId}
              serverId={serverId}
              kind={kind}
              loaders={cfg.egg?.addons?.[kind]?.loaders ?? []}
              version={gameVersion(cfg, cfg.egg?.addons?.[kind]?.version)}
              canWrite={can(server, "files.write")}
            />
          ))}
      </ul>
    </section>
  );
}

// useDismissed says whether a row was dismissed, in this browser, while
// it said the same thing (sig); a change brings it back.
function useDismissed(serverId: string, key: string, sig: string): [boolean, () => void] {
  const storage = `attention:${serverId}:${key}`;
  // Also in memory: private windows can't store it, so it only comes back
  // next visit.
  const [hidden, setHidden] = useState("");
  const dismiss = () => {
    setHidden(sig);
    try {
      localStorage.setItem(storage, sig);
    } catch {
      // As above.
    }
  };
  return [hidden === sig || readBack(storage) === sig, dismiss];
}

function readBack(storage: string): string | null {
  try {
    return localStorage.getItem(storage);
  } catch {
    return null;
  }
}

const icons: Record<string, LucideIcon> = {
  install: TriangleAlert,
  disk: HardDrive,
  egg: PackageCheck,
  missing: PackageX,
  updates: ArrowUpCircle,
  scan: ShieldAlert,
};

const action = buttonVariants({ size: "sm", variant: "outline" });

function Row({ orgId, serverId, item }: { orgId: string; serverId: string; item: AttentionItem }) {
  const [gone, dismiss] = useDismissed(serverId, item.id, item.sig);
  if (gone) return null;
  const Icon = icons[item.id.split(":")[0] ?? ""] ?? TriangleAlert;
  const params = { orgId, serverId };
  const a = item.action;
  return (
    <li
      className="flex flex-wrap items-center gap-3 rounded-lg border px-3 py-2 text-sm"
      style={{ order: item.rank }}
    >
      <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
      <span className="min-w-0 flex-1 basis-60">
        {item.text}
        {item.blocked && <span className="block text-muted-foreground">{item.blocked}</span>}
      </span>
      {a?.to === "settings" && (
        <Link to="/orgs/$orgId/servers/$serverId/settings" params={params} className={action}>
          {a.label}
        </Link>
      )}
      {a?.to === "scan" && (
        <a href="#resource-scan" className={action}>
          {a.label}
        </a>
      )}
      {a?.to === "files" && (
        <Link to="/orgs/$orgId/servers/$serverId/files" params={params} className={action}>
          {a.label}
        </Link>
      )}
      {a?.to === "addons" && a.kind && (
        <Link
          to="/orgs/$orgId/servers/$serverId/addons/$kind"
          params={{ ...params, kind: a.kind }}
          className={action}
        >
          {a.label}
        </Link>
      )}
      <Button
        size="icon-xs"
        variant="ghost"
        aria-label="Dismiss"
        className="text-muted-foreground"
        onClick={dismiss}
      >
        <X />
      </Button>
    </li>
  );
}

// AddonItems is one add-on kind's rows: those missing one they need, and
// updates. Kept for 5 minutes, so coming back to Overview doesn't list
// and hash the folder again; the Add-ons tab refreshes it after a change.
function AddonItems({
  orgId,
  nodeId,
  serverId,
  kind,
  loaders,
  version,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  kind: string;
  loaders: string[];
  version: string;
  canWrite: boolean;
}) {
  const { files, known } = useInstalledAddons(orgId, nodeId, serverId, kind, loaders, version, {
    staleTime: 5 * 60_000,
  });
  const meta = addonKinds[kind];
  if (!known.data || !meta) return null;
  const addons = known.data.addons.map((a) => ({
    sha512: a.sha512,
    title: a.project?.title ?? "An add-on",
    projectId: a.project?.id ?? "",
    latestId: a.latest?.file ? a.latest.id || a.latest.versionNumber : undefined,
    required: (a.version?.dependencies ?? [])
      .filter((d) => d.type === "required")
      .map((d) => d.projectId),
  }));
  // Files nobody knows may be the dependency, added by hand.
  const handAdded = files.some(
    (f) => f.sha512 && !known.data.addons.some((a) => a.sha512 === f.sha512),
  );
  return (
    <>
      {addonItems(kind, meta.noun, addons, handAdded, canWrite).map((it) => (
        <Row key={it.id} orgId={orgId} serverId={serverId} item={it} />
      ))}
    </>
  );
}
