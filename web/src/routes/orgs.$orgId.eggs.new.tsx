import { createFileRoute } from "@tanstack/react-router";

import { orgLibrary } from "@/components/eggs/library";
import { NewEggPage } from "@/components/eggs/pages";

export const Route = createFileRoute("/orgs/$orgId/eggs/new")({
  component: () => <NewEggPage lib={orgLibrary(Route.useParams().orgId)} />,
});
