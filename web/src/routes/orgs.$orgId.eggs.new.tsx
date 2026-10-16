import { createFileRoute } from "@tanstack/react-router";

import { orgLibrary } from "@/components/eggs/library";
import { NewEggPage } from "@/components/eggs/pages";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/eggs/new")({
  component: () => <NewEggPage lib={orgLibrary(useIds().orgId)} />,
});
