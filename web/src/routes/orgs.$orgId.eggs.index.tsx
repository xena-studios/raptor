import { createFileRoute } from "@tanstack/react-router";

import { orgLibrary } from "@/components/eggs/library";
import { LibraryPage } from "@/components/eggs/pages";

export const Route = createFileRoute("/orgs/$orgId/eggs/")({
  component: () => <LibraryPage lib={orgLibrary(Route.useParams().orgId)} />,
});
