import { createFileRoute, Link, Outlet, redirect, useMatches } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import { NotFound } from "@/components/error-screen";
import { PopoutHeader } from "@/components/popout-header";
import { PowerButtons } from "@/components/power-buttons";
import { StatusBadge } from "@/components/server-status";
import { canonical, resolveServer, shortId, useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";
import { serverStatus } from "@/lib/servers";

export const Route = createFileRoute("/popout/$orgId/servers/$serverId")({
  params: {
    parse: (p) => p,
    stringify: ({ serverId }) => ({ serverId: shortId(serverId) }),
  },
  beforeLoad: async ({ location, params }) => {
    const href = canonical(location.href, params);
    if (href) throw redirect({ href, replace: true });
    await resolveServer(params.orgId, params.serverId);
  },
  component: ServerPopout,
});

const what: Record<string, string> = { console: "Console", files: "Files", stats: "Stats" };

// A server's pop-out: its name, state, and power buttons, then the one
// thing popped out.
function ServerPopout() {
  const { orgId, nodeId, serverId } = useIds();
  const { node, server, loaded } = useServer(orgId, nodeId, serverId);
  const [error, setError] = useState("");
  const leaf = useMatches({ select: (m) => m.at(-1)?.pathname.split("/").at(-1) ?? "" });
  const title = what[leaf] ?? "";
  useEffect(() => {
    document.title = [title, server?.name, "Raptor"].filter(Boolean).join(" · ");
  }, [title, server?.name]);

  if (loaded && !server) return <NotFound full={false} what="server" />;
  const status = server && serverStatus(server, node?.connected ?? false);
  return (
    <>
      <PopoutHeader
        title={
          <>
            <span className="truncate">{server?.name ?? "Server"}</span>
            <span className="text-muted-foreground">{title}</span>
            {status && <StatusBadge status={status} />}
          </>
        }
        error={error}
        back={
          <Link to="/orgs/$orgId/servers/$serverId" params={{ orgId, serverId }} target="_blank">
            Open in Raptor
          </Link>
        }
        actions={
          can(server, "power") && (
            <PowerButtons
              nodeId={nodeId}
              serverId={serverId}
              state={
                ["pending", "installing", "failed"].includes(server?.installState ?? "")
                  ? "installing"
                  : server?.state
              }
              offline={!node?.connected}
              onError={setError}
            />
          )
        }
      />
      <div className="min-h-0 flex-1 overflow-auto">
        <Outlet />
      </div>
    </>
  );
}
