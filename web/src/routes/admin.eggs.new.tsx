import { createFileRoute } from "@tanstack/react-router";

import { officialLibrary } from "@/components/eggs/library";
import { NewEggPage } from "@/components/eggs/pages";

export const Route = createFileRoute("/admin/eggs/new")({
  component: () => <NewEggPage lib={officialLibrary} />,
});
