import { createFileRoute } from "@tanstack/react-router";

import { orgLibrary } from "@/components/eggs/library";
import { EditEggPage } from "@/components/eggs/pages";
import { shortEggId, useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/eggs/$eggId/edit")({
  params: {
    parse: (p) => p,
    stringify: ({ eggId }) => ({ eggId: shortEggId(eggId) }),
  },
  component: function EditEgg() {
    const { eggId } = Route.useParams();
    const { orgId } = useIds();
    return <EditEggPage key={eggId} lib={orgLibrary(orgId)} id={eggId} />;
  },
});
