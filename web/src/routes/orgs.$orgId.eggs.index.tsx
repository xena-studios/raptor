import { createFileRoute } from "@tanstack/react-router";

import { orgLibrary } from "@/components/eggs/library";
import { LibraryPage } from "@/components/eggs/pages";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/eggs/")({
  component: () => <LibraryPage lib={orgLibrary(useIds().orgId)} />,
});
