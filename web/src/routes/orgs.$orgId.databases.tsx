import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useState } from "react";

import { NodeDatabases } from "@/components/node-databases";
import { EmptyState, PageHeader } from "@/components/page";
import { buttonVariants } from "@/components/ui/button";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import {
  databasesCapability,
  dbExternalCapability,
  dbRedisExternalCapability,
  dbSettingsCapability,
  dbSharedCapability,
  dbUsageCapability,
  dbVersionsCapability,
} from "@/lib/databases";
import { dbInstanceBrowserCapability } from "@/lib/db-browser";
import { isAdmin } from "@/lib/format";
import { useIds } from "@/lib/ids";
import { networksCapability } from "@/lib/networks";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/databases")({
  component: Databases,
});

const select =
  "h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// Databases is every database instance on the org's nodes, with their
// databases and the servers linked to each (docs/DATABASES.md): the same
// list as each node's Databases tab, for all of them at once. Admins and
// owners only; members see databases through their servers.
function Databases() {
  const session = Route.useRouteContext();
  const { orgId } = useIds();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const role = orgs.data?.orgs.find((o) => o.id === orgId)?.role;
  const nodes = useOrgNodes(orgId);
  const [only, setOnly] = useState("");
  const all = [...(nodes.data?.nodes ?? [])].sort(
    (a, b) => Number(b.connected) - Number(a.connected) || a.name.localeCompare(b.name),
  );
  const list = all.filter((n) => !only || n.id === only);

  return (
    <>
      <PageHeader
        title="Databases"
        description="Database instances on your nodes, and their databases. Databases belong to their instance, so deleting a server keeps them."
        actions={
          all.length > 1 && (
            <select
              aria-label="Node"
              className={select}
              value={only}
              onChange={(e) => setOnly(e.target.value)}
            >
              <option value="">Every node</option>
              {all.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name}
                </option>
              ))}
            </select>
          )
        }
      />
      {orgs.data && !isAdmin(role) ? (
        <p className="text-sm text-muted-foreground">
          Only the org's admins and owners manage databases. You see yours on your servers'
          Databases tabs.
        </p>
      ) : nodes.data && all.length === 0 ? (
        <EmptyState
          title="No nodes yet"
          description="Database instances run on your nodes. Connect one first."
          action={
            <Link
              to="/orgs/$orgId/nodes/new"
              params={{ orgId }}
              className={buttonVariants({ size: "sm" })}
            >
              Connect node
            </Link>
          }
        />
      ) : (
        <div className="flex flex-col gap-6">
          {list.map((node) => (
            <NodeDatabases
              key={node.id}
              orgId={orgId}
              nodeId={node.id}
              nodeName={node.name}
              userId={session.user?.id ?? ""}
              connected={node.connected}
              supported={node.capabilities.includes(databasesCapability)}
              networksSupported={node.capabilities.includes(networksCapability)}
              versionsSupported={node.capabilities.includes(dbVersionsCapability)}
              externalSupported={node.capabilities.includes(dbExternalCapability)}
              settingsSupported={node.capabilities.includes(dbSettingsCapability)}
              browserSupported={node.capabilities.includes(dbInstanceBrowserCapability)}
              redisExternalSupported={node.capabilities.includes(dbRedisExternalCapability)}
              usageSupported={node.capabilities.includes(dbUsageCapability)}
              sharedSupported={node.capabilities.includes(dbSharedCapability)}
            />
          ))}
        </div>
      )}
    </>
  );
}
