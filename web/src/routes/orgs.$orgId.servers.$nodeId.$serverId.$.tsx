import { createFileRoute } from "@tanstack/react-router";

// An old server page's tab (/servers/<node>/<server>/files): its parent
// redirects it.
export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/$")({
  component: () => null,
});
