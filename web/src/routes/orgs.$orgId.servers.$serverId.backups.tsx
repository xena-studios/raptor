import { createFileRoute } from "@tanstack/react-router";

import { type BrowseAt, ServerBackups } from "@/components/server-backups";
import { useIds } from "@/lib/ids";

// backup: the open backup's short ID; path and file: where in it.
type BackupsSearch = Partial<BrowseAt>;

const text = (v: unknown) => (typeof v === "string" && v ? v : undefined);

export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/backups")({
  validateSearch: (s: Record<string, unknown>): BackupsSearch => ({
    backup: text(s.backup),
    path: text(s.path),
    file: text(s.file),
  }),
  component: BackupsTab,
});

function BackupsTab() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = useIds();
  const { backup, path, file } = Route.useSearch();
  const navigate = Route.useNavigate();
  return (
    <ServerBackups
      orgId={orgId}
      nodeId={nodeId}
      serverId={serverId}
      userId={session.user?.id ?? ""}
      browse={backup ? { backup, path, file } : undefined}
      onBrowse={(to) =>
        navigate({
          search: to ? { backup: to.backup, path: to.path || undefined, file: to.file } : {},
        })
      }
    />
  );
}
