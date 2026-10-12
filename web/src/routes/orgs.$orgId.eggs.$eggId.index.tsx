import { createFileRoute } from "@tanstack/react-router";

import { orgLibrary } from "@/components/eggs/library";
import { EggPage } from "@/components/eggs/pages";

export const Route = createFileRoute("/orgs/$orgId/eggs/$eggId/")({
  component: function Egg() {
    const { orgId, eggId } = Route.useParams();
    return <EggPage key={eggId} lib={orgLibrary(orgId)} id={eggId} />;
  },
});
