import { createFileRoute, Link, Outlet } from "@tanstack/react-router";

import { useAddonSections } from "@/lib/addon-sections";
import { useIds } from "@/lib/ids";
import { useServer } from "@/lib/org-data";

// A server's Add-ons tab (#436): its plugins, mods, datapacks, and
// Workshop items (and CS2's frameworks), one section each, with a row to switch between them
// when there's more than one.
export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/addons")({
  component: AddonsLayout,
});

const pill =
  "rounded-md px-2.5 py-1 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground data-[status=active]:bg-secondary data-[status=active]:font-medium data-[status=active]:text-foreground";

function AddonsLayout() {
  const { orgId, nodeId, serverId } = useIds();
  const { node, server } = useServer(orgId, nodeId, serverId);
  const { sections } = useAddonSections(orgId, nodeId, serverId, server, node?.capabilities);
  const params = { orgId, serverId };
  return (
    <div className="flex flex-col gap-4">
      {sections.length > 1 && (
        <nav aria-label="Add-on sections" className="flex flex-wrap gap-1">
          {sections.map((s) =>
            s.key === "bedrock" ? (
              <Link
                key={s.key}
                to="/orgs/$orgId/servers/$serverId/addons/bedrock"
                params={params}
                className={pill}
              >
                {s.label}
              </Link>
            ) : s.key === "frameworks" ? (
              <Link
                key={s.key}
                to="/orgs/$orgId/servers/$serverId/addons/frameworks"
                params={params}
                className={pill}
              >
                {s.label}
              </Link>
            ) : s.key === "workshop" ? (
              <Link
                key={s.key}
                to="/orgs/$orgId/servers/$serverId/addons/workshop"
                params={params}
                className={pill}
              >
                {s.label}
              </Link>
            ) : (
              <Link
                key={s.key}
                to="/orgs/$orgId/servers/$serverId/addons/$kind"
                params={{ ...params, kind: s.key }}
                className={pill}
              >
                {s.label}
              </Link>
            ),
          )}
        </nav>
      )}
      <Outlet />
    </div>
  );
}
