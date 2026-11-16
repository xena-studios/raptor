import { createFileRoute } from "@tanstack/react-router";

import { BedrockPacks } from "@/components/bedrock-packs";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

// A Bedrock server's behaviour and resource packs (#416).
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/addons/bedrock")({
  component: function Bedrock() {
    const { orgId, nodeId, serverId } = useIds();
    const { server } = useServer(orgId, nodeId, serverId);
    if (!server) return null;
    return (
      <BedrockPacks nodeId={nodeId} serverId={serverId} canWrite={can(server, "files.write")} />
    );
  },
});
