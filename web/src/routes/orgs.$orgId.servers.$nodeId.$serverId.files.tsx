import { createFileRoute } from "@tanstack/react-router";

import { Files } from "@/components/file-manager";
import { SFTPCard } from "@/components/sftp-access";
import { can, useServer } from "@/lib/org-data";

type FilesSearch = { path?: string; file?: string };

export const Route = createFileRoute("/orgs/$orgId/servers/$nodeId/$serverId/files")({
  validateSearch: (s: Record<string, unknown>): FilesSearch => ({
    path: typeof s.path === "string" && s.path ? s.path : undefined,
    file: typeof s.file === "string" && s.file ? s.file : undefined,
  }),
  component: FilesTab,
});

function FilesTab() {
  const { orgId, nodeId, serverId } = Route.useParams();
  const { path, file } = Route.useSearch();
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
        file={file}
        onNavigate={(to) => navigate({ search: { path: to.path || undefined, file: to.file } })}
      />
      {node && can(server, "sftp") && (
        <SFTPCard orgId={orgId} node={node} serverId={serverId} admin={can(server, "*")} />
      )}
    </div>
  );
}
