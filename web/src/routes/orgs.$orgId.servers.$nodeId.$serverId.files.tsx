import { createFileRoute } from "@tanstack/react-router";

import { Files } from "@/components/files";
import { SFTPCard } from "@/components/sftp-access";
import { can, useServer } from "@/lib/org-data";

type FilesSearch = { path?: string };

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/files")({
  validateSearch: (s: Record<string, unknown>): FilesSearch => ({
    path: typeof s.path === "string" && s.path ? s.path : undefined,
  }),
  component: FilesTab,
});

function FilesTab() {
  const { orgId, nodeId, serverId } = Route.useParams();
  const { path } = Route.useSearch();
  const navigate = Route.useNavigate();
  const { node, server } = useServer(orgId, nodeId, serverId);
  if (!server) return null;
  return (
    <div className="flex flex-col gap-6">
      <Files
        nodeId={nodeId}
        serverId={serverId}
        canWrite={can(server, "files.write")}
        path={path ?? ""}
        onPath={(p) => navigate({ search: { path: p || undefined } })}
      />
      {node && can(server, "sftp") && (
        <SFTPCard orgId={orgId} node={node} serverId={serverId} admin={can(server, "*")} />
      )}
    </div>
  );
}
