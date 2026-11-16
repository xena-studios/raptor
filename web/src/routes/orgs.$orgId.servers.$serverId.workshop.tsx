import { createFileRoute, redirect } from "@tanstack/react-router";

// The Workshop moved into the Add-ons tab (#436); old links still work.
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/workshop")({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/orgs/$orgId/servers/$serverId/addons/workshop", params, replace: true });
  },
});
