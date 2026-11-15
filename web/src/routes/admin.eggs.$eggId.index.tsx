import { createFileRoute, redirect } from "@tanstack/react-router";

import { officialLibrary } from "@/components/eggs/library";
import { EggPage } from "@/components/eggs/pages";
import { canonical, shortEggId } from "@/lib/ids";

export const Route = createFileRoute("/admin/eggs/$eggId/")({
  params: {
    parse: (p) => p,
    stringify: ({ eggId }) => ({ eggId: shortEggId(eggId) }),
  },
  beforeLoad: ({ location, params }) => {
    const href = canonical(location.href, params);
    if (href) throw redirect({ href, replace: true });
  },
  component: function Egg() {
    const { eggId } = Route.useParams();
    return <EggPage key={eggId} lib={officialLibrary} id={eggId} />;
  },
});
