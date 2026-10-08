import { createFileRoute } from "@tanstack/react-router";

import { ConnectNode } from "@/components/org/connect-node";
import { PageHeader } from "@/components/page";

export const Route = createFileRoute("/orgs/$orgId/nodes/new")({
  component: NewNode,
});

function NewNode() {
  const session = Route.useRouteContext();
  const { orgId } = Route.useParams();
  return (
    <>
      <PageHeader
        back={{ label: "Nodes", to: "/orgs/$orgId/nodes", params: { orgId } }}
        title="Connect a node"
        description="Install Raptor on a machine of yours and link it to this org."
      />
      <ConnectNode orgId={orgId} userId={session.user?.id ?? ""} />
    </>
  );
}
