import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Plus, Server } from "lucide-react";
import { useState } from "react";

import { EmptyState, ListToolbar, type ListView, PageHeader } from "@/components/page";
import { StatusBadge } from "@/components/server-status";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import { type OrgServer, useOrgServers } from "@/lib/org-data";
import { serverStatus } from "@/lib/servers";

export const Route = createFileRoute("/orgs/$orgId/servers/")({
  component: Servers,
});

function connect(s: OrgServer): string {
  return s.ports[0] ? `n-${s.node.shortId}.raptornodes.net:${s.ports[0]}` : "—";
}

function Servers() {
  const { orgId } = Route.useParams();
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
                <Card key={`${s.node.id}/${s.id}`}>
                  <CardHeader>
                    <CardTitle className="flex items-center justify-between gap-2">
                      <Link
                        to="/orgs/$orgId/servers/$nodeId/$serverId"
                        params={link(s)}
                        className="truncate hover:underline"
                      >
                        {s.name}
                      </Link>
                      <StatusBadge status={serverStatus(s, s.node.connected)} />
                    </CardTitle>
                    <CardDescription className="truncate">{s.eggName || "—"}</CardDescription>
                  </CardHeader>
                  <CardContent className="flex flex-col gap-2 text-sm">
                    <div className="flex justify-between gap-2">
                      <span className="text-muted-foreground">Node</span>
                      <span className="truncate">{s.node.name}</span>
                    </div>
                    <div className="flex justify-between gap-2">
                      <span className="text-muted-foreground">Connect</span>
                      <span className="truncate font-mono text-xs">{connect(s)}</span>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          ) : (
            <Card className="py-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Server</TableHead>
                    <TableHead>Game</TableHead>
                    <TableHead>Node</TableHead>
                    <TableHead>Connect</TableHead>
                    <TableHead className="text-right">Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {list.map((s) => (
                    <TableRow key={`${s.node.id}/${s.id}`}>
                      <TableCell>
                        <Link
                          to="/orgs/$orgId/servers/$nodeId/$serverId"
                          params={link(s)}
                          className="font-medium hover:underline"
                        >
                          {s.name}
                        </Link>
                      </TableCell>
                      <TableCell className="text-muted-foreground">{s.eggName || "—"}</TableCell>
                      <TableCell>{s.node.name}</TableCell>
                      <TableCell className="font-mono text-xs">{connect(s)}</TableCell>
                      <TableCell className="text-right">
                        <StatusBadge status={serverStatus(s, s.node.connected)} />
                      </TableCell>
                    </TableRow>
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
