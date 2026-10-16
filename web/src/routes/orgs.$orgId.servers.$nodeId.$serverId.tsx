import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";

import { shortId } from "@/lib/ids";

// Server pages were /servers/<node>/<server>; they're /servers/<server> now
// (the Panel finds the node). Old links land on the new page.
export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId")({
  beforeLoad: ({ location, params }) => {
    const from = `/servers/${params.nodeId}/${params.serverId}`;
    throw redirect({
      href: location.href.replace(from, `/servers/${shortId(params.serverId)}`),
      replace: true,
    });
  },
  component: Outlet,
});
