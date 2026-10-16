import { createFileRoute } from "@tanstack/react-router";

import { orgLibrary } from "@/components/eggs/library";
import { EggPage } from "@/components/eggs/pages";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/eggs/$eggId/")({
  component: function Egg() {
    const { eggId } = Route.useParams();
    const { orgId } = useIds();
    return <EggPage key={eggId} lib={orgLibrary(orgId)} id={eggId} />;
  },
});
