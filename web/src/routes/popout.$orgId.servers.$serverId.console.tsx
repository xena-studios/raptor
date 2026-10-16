import { createFileRoute } from "@tanstack/react-router";
import { lazy, Suspense } from "react";

import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

const Console = lazy(() => import("@/components/console"));

export const Route = createFileRoute("/popout/$orgId/servers/$serverId/console")({
  component: ConsolePopout,
});

function ConsolePopout() {
  const { orgId, nodeId, serverId } = useIds();
  const { server } = useServer(orgId, nodeId, serverId);
  if (!server) return null;
  if (!can(server, "console.read") && !can(server, "console.write")) {
    return (
      <p className="p-4 text-sm text-muted-foreground">
        You don't have access to this server's console.
      </p>
    );
  }
  return (
    <div className="h-full p-3">
      <Suspense fallback={<p className="text-sm text-muted-foreground">Loading the console…</p>}>
        <Console nodeId={nodeId} serverId={serverId} canWrite={can(server, "console.write")} fill />
      </Suspense>
    </div>
  );
}
