import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";

import { canonical, resolveOrg, shortId } from "@/lib/ids";

export const Route = createFileRoute("/popout/$orgId")({
  params: {
    parse: (p) => p,
    stringify: ({ orgId }) => ({ orgId: shortId(orgId) }),
  },
  beforeLoad: async ({ location, params }) => {
    const href = canonical(location.href, params);
    if (href) throw redirect({ href, replace: true });
    await resolveOrg(params.orgId);
  },
  component: Outlet,
});
