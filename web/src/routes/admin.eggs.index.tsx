import { createFileRoute } from "@tanstack/react-router";

import { officialLibrary } from "@/components/eggs/library";
import { LibraryPage } from "@/components/eggs/pages";

export const Route = createFileRoute("/admin/eggs/")({
  component: () => <LibraryPage lib={officialLibrary} />,
});
