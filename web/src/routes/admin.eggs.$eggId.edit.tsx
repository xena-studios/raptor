import { createFileRoute, redirect } from "@tanstack/react-router";

import { officialLibrary } from "@/components/eggs/library";
import { EditEggPage } from "@/components/eggs/pages";
import { canonical, shortEggId } from "@/lib/ids";

export const Route = createFileRoute("/admin/eggs/$eggId/edit")({
  params: {
    parse: (p) => p,
    stringify: ({ eggId }) => ({ eggId: shortEggId(eggId) }),
  },
  beforeLoad: ({ location, params }) => {
    const href = canonical(location.href, params);
    if (href) throw redirect({ href, replace: true });
  },
  component: function EditEgg() {
    const { eggId } = Route.useParams();
    return <EditEggPage key={eggId} lib={officialLibrary} id={eggId} />;
  },
});
