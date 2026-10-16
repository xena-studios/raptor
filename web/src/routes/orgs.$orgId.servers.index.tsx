import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Plus, Server } from "lucide-react";
import { useState } from "react";

import { ServerAddress } from "@/components/address";
import { EntityCard, LinkRow } from "@/components/entity-list";
import { EmptyState, ListToolbar, type ListView, PageHeader } from "@/components/page";
import { StatusBadge } from "@/components/server-status";
import { buttonVariants } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { isAdmin } from "@/lib/format";
import { useIds } from "@/lib/ids";
import { type OrgServer, useOrgServers } from "@/lib/org-data";
import { serverStatus } from "@/lib/servers";

export const Route = createFileRoute("/orgs/$orgId/servers/")({
  component: Servers,
});

function Servers() {
  const { orgId } = useIds();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const admin = isAdmin(orgs.data?.orgs.find((o) => o.id === orgId)?.role);
  const { servers, pending } = useOrgServers(orgId);
  const [query, setQuery] = useState("");
  const [view, setView] = useState<ListView>("grid");
  const q = query.trim().toLowerCase();
  const list = servers.filter(
    (s) =>
      !q ||
      s.name.toLowerCase().includes(q) ||
      s.eggName.toLowerCase().includes(q) ||
      s.node.name.toLowerCase().includes(q),
  );
  const deploy = admin && (
    <Link
      to="/orgs/$orgId/servers/new"
      params={{ orgId }}
      className={buttonVariants({ size: "sm" })}
    >
      <Plus /> Deploy server
    </Link>
  );
  const link = (s: OrgServer) => ({ orgId, nodeId: s.node.id, serverId: s.id });

  return (
    <>
      <PageHeader
        title="Servers"
        description="The game servers and apps you run."
        actions={deploy}
      />
      {!pending && servers.length === 0 ? (
        <EmptyState
          icon={Server}
          title="No servers yet"
          description="Deploy one from the egg catalog onto one of your nodes."
          action={deploy}
        />
      ) : (
        <>
          <ListToolbar
            noun="server"
            count={list.length}
            query={query}
            onQuery={setQuery}
            view={view}
            onView={setView}
          />
          {view === "grid" ? (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {list.map((s) => (
                <EntityCard
                  key={`${s.node.id}/${s.id}`}
                  link={{ to: "/orgs/$orgId/servers/$nodeId/$serverId", params: link(s) }}
                  title={s.name}
                  status={serverStatus(s, s.node.connected)}
                  subtitle={s.eggName || "—"}
                  rows={[
                    [
                      "Address",
                      <ServerAddress key="a" node={s.node} server={s} port={s.ports[0]} />,
                    ],
                    ["Node", s.node.name],
                  ]}
                />
              ))}
            </div>
          ) : (
            <Card className="py-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Server</TableHead>
                    <TableHead>Address</TableHead>
                    <TableHead className="hidden md:table-cell">Game</TableHead>
                    <TableHead className="hidden sm:table-cell">Node</TableHead>
                    <TableHead className="text-right">Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {list.map((s) => (
                    <LinkRow
                      key={`${s.node.id}/${s.id}`}
                      link={{ to: "/orgs/$orgId/servers/$nodeId/$serverId", params: link(s) }}
                    >
                      <TableCell>
                        <Link
                          to="/orgs/$orgId/servers/$nodeId/$serverId"
                          params={link(s)}
                          className="font-medium hover:underline"
                        >
                          {s.name}
                        </Link>
                      </TableCell>
                      <TableCell>
                        <ServerAddress node={s.node} server={s} port={s.ports[0]} />
                      </TableCell>
                      <TableCell className="hidden text-muted-foreground md:table-cell">
                        {s.eggName || "—"}
                      </TableCell>
                      <TableCell className="hidden sm:table-cell">{s.node.name}</TableCell>
                      <TableCell className="text-right">
                        <StatusBadge status={serverStatus(s, s.node.connected)} />
                      </TableCell>
                    </LinkRow>
                  ))}
                </TableBody>
              </Table>
            </Card>
          )}
        </>
      )}
    </>
  );
}
