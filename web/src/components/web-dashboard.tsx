import { useQuery } from "@connectrpc/connect-query";
import { Link } from "@tanstack/react-router";
import { ExternalLink } from "lucide-react";
import type { ReactNode } from "react";
import { useMemo } from "react";

import { buttonVariants } from "@/components/ui/button";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { type ServerConfig, webLink } from "@/lib/servers";

// WebDashboard links to the web page a server serves (txAdmin, Grafana;
// the egg's web, #428), or says which port to add so it can be reached.
// For txAdmin it shows the PIN its first run printed, to create the admin
// account with.
export function WebDashboard({
  orgId,
  nodeId,
  serverId,
  host,
  pin,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  // Where players reach the server: its shown address, or the node's.
  host: string;
  // txAdmin's first-run PIN, from the console.
  pin?: string;
}) {
  const res = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const link = useMemo(() => {
    if (!res.data?.configJson || !host) return undefined;
    return webLink(JSON.parse(res.data.configJson) as ServerConfig, host);
  }, [res.data?.configJson, host]);
  if (!link) return null;
  const pinNote: ReactNode = pin && link.name === "txAdmin" && (
    <p className="mt-2 text-xs text-muted-foreground">
      To set it up, sign in with Cfx.re and enter the PIN{" "}
      <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-sm text-foreground select-all">
        {pin}
      </code>{" "}
      from the console.
    </p>
  );
  if (!link.reachable) {
    return (
      <>
        <p className="mt-3 text-xs text-muted-foreground">
          {link.name} listens on port {link.port}, which isn't one of this server's ports yet.{" "}
          <Link
            to="/orgs/$orgId/servers/$serverId/network"
            params={{ orgId, serverId }}
            className="underline underline-offset-4"
          >
            Add it on Network
          </Link>{" "}
          to open it.
        </p>
        {pinNote}
      </>
    );
  }
  return (
    <>
      <a
        href={link.url}
        target="_blank"
        rel="noreferrer"
        className={buttonVariants({ size: "sm", variant: "outline", className: "mt-3" })}
      >
        Open {link.name} <ExternalLink />
      </a>
      {pinNote}
    </>
  );
}
