import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link, Outlet } from "@tanstack/react-router";
import { Plus } from "lucide-react";

import { Hidden } from "@/components/address";
import { NotFound } from "@/components/error-screen";
import { PageHeader, RouteTabs, tabClass } from "@/components/page";
import { StatusBadge } from "@/components/server-status";
import { buttonVariants } from "@/components/ui/button";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { nodeHostname } from "@/lib/addresses";
import { isAdmin } from "@/lib/format";
import { useOrgNodes } from "@/lib/org-data";
import { nodeStatus } from "@/lib/servers";

// A node's pages: its header, its tabs, and the tab below.
export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId")({
  component: NodeLayout,
});

function NodeLayout() {
  const { orgId, nodeId } = Route.useParams();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const admin = isAdmin(orgs.data?.orgs.find((o) => o.id === orgId)?.role);
  const nodes = useOrgNodes(orgId);
  const node = nodes.data?.nodes.find((n) => n.id === nodeId);

  if (nodes.data && !node) return <NotFound full={false} what="node" />;
  return (
    <>
      <div className="space-y-4">
        <PageHeader
          back={{ label: "Nodes", to: "/orgs/$orgId/nodes", params: { orgId } }}
          title={
            <span className="flex items-center gap-2.5">
              {node?.name ?? "Node"}
              {node && <StatusBadge status={nodeStatus(node)} />}
            </span>
          }
          description={
            node &&
            (node.displayAddress ? (
              <span className="font-mono">{node.displayAddress}</span>
            ) : (
              <Hidden value={nodeHostname(node)} label="hostname" />
            ))
          }
          actions={
            admin && (
              <Link
                to="/orgs/$orgId/servers/new"
                params={{ orgId }}
                search={{ node: nodeId }}
                className={buttonVariants({ size: "sm" })}
              >
                <Plus /> Deploy server
              </Link>
            )
          }
        />
        <RouteTabs label="Node sections">
          <Link
            to="/orgs/$orgId/nodes/$nodeId"
            params={{ orgId, nodeId }}
            activeOptions={{ exact: true }}
            className={tabClass}
          >
            Overview
          </Link>
          {admin && (
            <Link
              to="/orgs/$orgId/nodes/$nodeId/health"
              params={{ orgId, nodeId }}
              className={tabClass}
            >
              Health
            </Link>
          )}
          {admin && (
            <Link
              to="/orgs/$orgId/nodes/$nodeId/backups"
              params={{ orgId, nodeId }}
              className={tabClass}
            >
              Backups
            </Link>
          )}
          {admin && (
            <Link
              to="/orgs/$orgId/nodes/$nodeId/disks"
              params={{ orgId, nodeId }}
              className={tabClass}
            >
              Disks
            </Link>
          )}
          {admin && (
            <Link
              to="/orgs/$orgId/nodes/$nodeId/system"
              params={{ orgId, nodeId }}
              className={tabClass}
            >
              System
            </Link>
          )}
          {admin && (
            <Link
              to="/orgs/$orgId/nodes/$nodeId/settings"
              params={{ orgId, nodeId }}
              className={tabClass}
            >
              Settings
            </Link>
          )}
        </RouteTabs>
      </div>
      <Outlet />
    </>
  );
}
