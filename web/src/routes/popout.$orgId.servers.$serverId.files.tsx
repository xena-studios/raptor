import { createFileRoute } from "@tanstack/react-router";

import { Files } from "@/components/file-manager";
import { SFTPButton } from "@/components/sftp-access";
import { useIds } from "@/lib/ids";
import { can, useServer } from "@/lib/org-data";

type FilesSearch = { path?: string; file?: string };

export const Route = createFileRoute("/popout/$orgId/servers/$serverId/files")({
  validateSearch: (s: Record<string, unknown>): FilesSearch => ({
    path: typeof s.path === "string" && s.path ? s.path : undefined,
    file: typeof s.file === "string" && s.file ? s.file : undefined,
  }),
  component: FilesPopout,
});

function FilesPopout() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = useIds();
  const { path, file } = Route.useSearch();
  const navigate = Route.useNavigate();
  const { node, server } = useServer(orgId, nodeId, serverId);
  if (!server) return null;
  if (!can(server, "files.read")) {
    return (
      <p className="p-4 text-sm text-muted-foreground">
        You don't have access to this server's files.
      </p>
    );
  }
  return (
    <Files
      nodeId={nodeId}
      serverId={serverId}
      orgId={orgId}
      userId={session.user?.id ?? ""}
      canWrite={can(server, "files.write")}
      path={path ?? ""}
      file={file}
      fill
      onNavigate={(to) => navigate({ search: { path: to.path || undefined, file: to.file } })}
      canPull={!!node?.capabilities.includes("files-pull")}
      canImport={!!node?.capabilities.includes("files-import")}
      actions={
        node &&
        can(server, "sftp") && (
          <SFTPButton orgId={orgId} node={node} serverId={serverId} admin={can(server, "*")} />
        )
      }
    />
  );
}
