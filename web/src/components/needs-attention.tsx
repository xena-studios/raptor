import { useQuery } from "@connectrpc/connect-query";
import { useQuery as useTanQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowUpCircle, PackageCheck, PackageX, X } from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";

import { useFilesApi } from "@/components/file-manager/data";
import { useEggVersions } from "@/components/server-egg-update";
import { Button, buttonVariants } from "@/components/ui/button";
import { AddonService } from "@/gen/raptor/panel/v1/addon_pb";
import { type Node, OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { addonKinds } from "@/lib/eggs";
import { can } from "@/lib/org-data";
import { gameVersion, type ServerConfig } from "@/lib/servers";

// NeedsAttention is the one place on a server's Overview that says what's
// wrong or out of date, each with the one button that fixes it (#435): a
// console problem (passed in: the Java switch, or a known error), add-ons
// missing what they need, add-on updates, and a newer egg. It shows only
// when there's something in it, and each row can be dismissed until it
// changes.
export function NeedsAttention({
  orgId,
  nodeId,
  serverId,
  server,
  node,
  console: consoleItem,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  server: Server;
  node: Node | undefined;
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
  const kinds = can(server, "files.read")
    ? Object.keys(cfg?.egg?.addons ?? {}).filter((k) => k in addonKinds && k !== "umod")
    : [];
  return (
    // Hidden unless a row renders (CSS :has), so each item loads its own
    // data without the card knowing ahead.
    <section aria-label="Needs attention" className="hidden flex-col gap-2 has-[li]:flex">
      <h2 className="text-sm font-medium">Needs attention</h2>
      <ul className="flex flex-col gap-2">
        {consoleItem && <li className="list-none">{consoleItem}</li>}
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
            />
          ))}
        {cfg &&
          can(server, "*") &&
          node?.capabilities.includes("change-egg") &&
          server.eggSource && (
            <EggItem orgId={orgId} serverId={serverId} eggSource={server.eggSource} cfg={cfg} />
          )}
      </ul>
    </section>
  );
}

// useDismissed remembers, in this browser, that a row was dismissed while
// it said the same thing (sig); a change brings it back.
function useDismissed(serverId: string, key: string, sig: string): [boolean, () => void] {
  const storage = `attention:${serverId}:${key}`;
  const [gone, setGone] = useState(() => {
    try {
      return localStorage.getItem(storage) === sig;
    } catch {
      return false;
    }
  });
  const dismiss = () => {
    setGone(true);
    try {
      localStorage.setItem(storage, sig);
    } catch {
      // Private windows: it comes back next time.
    }
  };
  return [gone && readBack(storage) === sig, dismiss];
}

function readBack(storage: string): string | null {
  try {
    return localStorage.getItem(storage);
  } catch {
    return null;
  }
}

function Row({
  serverId,
  id,
  sig,
  icon,
  text,
  action,
}: {
  serverId: string;
  id: string;
  sig: string;
  icon: ReactNode;
  text: ReactNode;
  action?: ReactNode;
}) {
  const [gone, dismiss] = useDismissed(serverId, id, sig);
  if (gone) return null;
  return (
    <li className="flex flex-wrap items-center gap-3 rounded-lg border px-3 py-2 text-sm">
      <span className="shrink-0 text-muted-foreground [&_svg]:size-4">{icon}</span>
      <span className="min-w-0 flex-1 basis-60">{text}</span>
      {action}
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

const action = buttonVariants({ size: "sm", variant: "outline" });

type Listing = { files: { name: string; sha512?: string; sha256?: string }[] };

// AddonItems says, for one add-on kind, which installed add-ons are
// missing one they need and how many have updates. It uses the same
// queries as the Add-ons tab, so opening it costs nothing more.
function AddonItems({
  orgId,
  nodeId,
  serverId,
  kind,
  loaders,
  version,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  kind: string;
  loaders: string[];
  version: string;
}) {
  const api = useFilesApi(nodeId, serverId);
  const list = useTanQuery({
    queryKey: ["addons", nodeId, serverId, kind],
    queryFn: () => api.run<Listing>("addons.list", { kind }),
    retry: false,
  });
  const files = list.data?.files ?? [];
  const hashes = files.flatMap((f) => (f.sha512 ? [f.sha512] : []));
  const sha256 = Object.fromEntries(
    files.flatMap((f) => (f.sha512 && f.sha256 ? [[f.sha512, f.sha256]] : [])),
  );
  const known = useQuery(
    AddonService.method.identifyAddons,
    { orgId, hashes, sha256, loaders, gameVersion: version },
    { enabled: hashes.length > 0 },
  );
  const meta = addonKinds[kind];
  if (!known.data || !meta) return null;
  const addons = known.data.addons;
  const installed = new Set(addons.map((a) => a.project?.id));
  const outdated = addons.filter((a) => a.latest?.file);
  const missing = addons.filter((a) =>
    a.version?.dependencies.some((d) => d.type === "required" && !installed.has(d.projectId)),
  );
  // Files nobody knows may be the dependency, added by hand.
  const handAdded = files.some((f) => f.sha512 && !addons.some((a) => a.sha512 === f.sha512));
  const link = (label: string) => (
    <Link
      to="/orgs/$orgId/servers/$serverId/addons/$kind"
      params={{ orgId, serverId, kind }}
      className={action}
    >
      {label}
    </Link>
  );
  const noun = (n: number) => `${meta.noun}${n === 1 ? "" : "s"}`;
  return (
    <>
      {missing.length > 0 && !handAdded && (
        <Row
          serverId={serverId}
          id={`missing:${kind}`}
          sig={missing.map((a) => a.sha512).join(",")}
          icon={<PackageX />}
          text={
            missing.length === 1
              ? `${missing[0]?.project?.title} is missing another ${meta.noun} it needs, so it may not load.`
              : `${missing.length} ${noun(missing.length)} are missing others they need, so they may not load.`
          }
          action={link("Install what's missing")}
        />
      )}
      {outdated.length > 0 && (
        <Row
          serverId={serverId}
          id={`updates:${kind}`}
          sig={outdated.map((a) => a.latest?.id).join(",")}
          icon={<ArrowUpCircle />}
          text={`${outdated.length} ${noun(outdated.length)} ${outdated.length === 1 ? "has" : "have"} an update.`}
          action={link("Review updates")}
        />
      )}
    </>
  );
}

// EggItem says when the server's library egg has a newer version.
function EggItem({
  orgId,
  serverId,
  eggSource,
  cfg,
}: {
  orgId: string;
  serverId: string;
  eggSource: string;
  cfg: ServerConfig;
}) {
  const v = useEggVersions(orgId, eggSource, cfg);
  if (!v?.outdated) return null;
  return (
    <Row
      serverId={serverId}
      id="egg"
      sig={String(v.latest)}
      icon={<PackageCheck />}
      text={`A newer version of ${v.egg.name} (version ${v.latest}) is available${v.current !== undefined ? `; it runs version ${v.current}` : ""}.`}
      action={
        <Link
          to="/orgs/$orgId/servers/$serverId/settings"
          params={{ orgId, serverId }}
          className={action}
        >
          Review the update
        </Link>
      }
    />
  );
}
