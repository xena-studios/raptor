import { createFileRoute } from "@tanstack/react-router";

import { orgLibrary } from "@/components/eggs/library";
import { EditEggPage } from "@/components/eggs/pages";

export const Route = createFileRoute("/orgs/$orgId/eggs/$eggId/edit")({
  component: function EditEgg() {
    const { orgId, eggId } = Route.useParams();
    return <EditEggPage key={eggId} lib={orgLibrary(orgId)} id={eggId} />;
  },
});
