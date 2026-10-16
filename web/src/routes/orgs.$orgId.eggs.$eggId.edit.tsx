import { createFileRoute } from "@tanstack/react-router";

import { orgLibrary } from "@/components/eggs/library";
import { EditEggPage } from "@/components/eggs/pages";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/eggs/$eggId/edit")({
  component: function EditEgg() {
    const { eggId } = Route.useParams();
    const { orgId } = useIds();
    return <EditEggPage key={eggId} lib={orgLibrary(orgId)} id={eggId} />;
  },
});
