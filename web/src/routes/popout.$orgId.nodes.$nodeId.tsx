import { createFileRoute, Link, Outlet, redirect } from "@tanstack/react-router";
import { useEffect } from "react";

import { NotFound } from "@/components/error-screen";
import { PopoutHeader } from "@/components/popout-header";
import { StatusBadge } from "@/components/server-status";
import { canonical, resolveNode, shortId, useIds } from "@/lib/ids";
import { useOrgNodes } from "@/lib/org-data";
import { nodeStatus } from "@/lib/servers";

export const Route = createFileRoute("/popout/$orgId/nodes/$nodeId")({
  params: {
    parse: (p) => p,
    stringify: ({ nodeId }) => ({ nodeId: shortId(nodeId) }),
  },
  beforeLoad: async ({ location, params }) => {
    const href = canonical(location.href, params);
    if (href) throw redirect({ href, replace: true });
    await resolveNode(params.orgId, params.nodeId);
  },
  component: NodePopout,
});

function NodePopout() {
  const { orgId, nodeId } = useIds();
  const nodes = useOrgNodes(orgId);
  const node = nodes.data?.nodes.find((n) => n.id === nodeId);
  useEffect(() => {
    document.title = ["Stats", node?.name, "Raptor"].filter(Boolean).join(" · ");
  }, [node?.name]);
  if (nodes.data && !node) return <NotFound full={false} what="node" />;
  return (
    <>
      <PopoutHeader
        title={
          <>
            <span className="truncate">{node?.name ?? "Node"}</span>
            <span className="text-muted-foreground">Stats</span>
            {node && <StatusBadge status={nodeStatus(node)} />}
          </>
        }
        back={
          <Link to="/orgs/$orgId/nodes/$nodeId" params={{ orgId, nodeId }} target="_blank">
            Open in Raptor
          </Link>
        }
      />
      <div className="min-h-0 flex-1 overflow-auto">
        <Outlet />
      </div>
    </>
  );
}
