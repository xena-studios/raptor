import { createFileRoute } from "@tanstack/react-router";
import { Database } from "lucide-react";

import { EmptyState } from "@/components/page";

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/databases")({
  component: DatabasesTab,
});

// Databases for servers (MySQL/MariaDB that a plugin or mod can use) aren't
// built yet.
function DatabasesTab() {
  return (
    <EmptyState
      icon={Database}
      title="Databases are coming"
      description="Soon you'll be able to give this server its own MySQL database, for plugins and mods that need one."
    />
  );
}
