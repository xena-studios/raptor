import { createFileRoute } from "@tanstack/react-router";

import { DatabaseBrowser } from "@/components/db-browser";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/databases/$databaseId")({
  component: Browser,
});

function Browser() {
  const { orgId, nodeId, serverId } = useIds();
  const { databaseId } = Route.useParams();
  const { server } = useServer(orgId, nodeId, serverId);
  if (!server) return null;
  return (
    <DatabaseBrowser
      orgId={orgId}
      nodeId={nodeId}
      serverId={serverId}
      id={databaseId}
      canWrite={can(server, "databases.write")}
    />
  );
}
