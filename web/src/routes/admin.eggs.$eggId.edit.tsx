import { createFileRoute } from "@tanstack/react-router";

import { officialLibrary } from "@/components/eggs/library";
import { EditEggPage } from "@/components/eggs/pages";

export const Route = createFileRoute("/admin/eggs/$eggId/edit")({
  component: function EditEgg() {
    const { eggId } = Route.useParams();
    return <EditEggPage key={eggId} lib={officialLibrary} id={eggId} />;
  },
});
