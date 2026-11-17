import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";

import { DemoEggPage } from "@/components/demo-egg";
import { orgLibrary } from "@/components/eggs/library";
import { NewEggPage } from "@/components/eggs/pages";
import { OrgService, Role } from "@/gen/raptor/panel/v1/org_pb";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/eggs/new")({
  component: function NewEgg() {
    const { orgId } = useIds();
    const org = useQuery(OrgService.method.listOrgs, {}).data?.orgs.find((o) => o.id === orgId);
    // The public demo: import and edit your own egg; nothing is saved.
    if (org?.demo && org.role !== Role.OWNER) return <DemoEggPage lib={orgLibrary(orgId)} />;
    return <NewEggPage lib={orgLibrary(orgId)} />;
  },
});
