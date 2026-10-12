import { createFileRoute } from "@tanstack/react-router";

import { officialLibrary } from "@/components/eggs/library";
import { EggPage } from "@/components/eggs/pages";

export const Route = createFileRoute("/admin/eggs/$eggId/")({
  component: function Egg() {
    const { eggId } = Route.useParams();
    return <EggPage key={eggId} lib={officialLibrary} id={eggId} />;
  },
});
