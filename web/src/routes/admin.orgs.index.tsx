import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useDeferredValue, useState } from "react";

import { LinkRow } from "@/components/entity-list";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";
import { when } from "@/lib/format";

export const Route = createFileRoute("/admin/orgs/")({
  component: OrgsPage,
});

function OrgsPage() {
  const [query, setQuery] = useState("");
  const q = useDeferredValue(query.trim());
  const orgs = useQuery(StaffService.method.searchOrgs, { query: q });
  return (
    <div className="flex flex-col gap-4">
      <div className="relative w-full sm:max-w-sm">
        <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
        <Input
          type="search"
          aria-label="Search orgs"
          placeholder="Name or ID"
          className="pl-8"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>
      {orgs.error && <p className="text-sm text-destructive">{message(orgs.error)}</p>}
      <Card className="py-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Org</TableHead>
              <TableHead className="hidden sm:table-cell">Members</TableHead>
              <TableHead>Nodes</TableHead>
              <TableHead className="hidden sm:table-cell">Servers</TableHead>
              <TableHead className="hidden md:table-cell">Created</TableHead>
              <TableHead className="text-right">Status</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {orgs.data?.orgs.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-muted-foreground">
                  No orgs match.
                </TableCell>
              </TableRow>
            )}
            {orgs.data?.orgs.map((o) => (
              <LinkRow key={o.id} link={{ to: "/admin/orgs/$orgId", params: { orgId: o.id } }}>
                <TableCell>
                  <Link
                    to="/admin/orgs/$orgId"
                    params={{ orgId: o.id }}
                    className="font-medium hover:underline"
                  >
                    {o.name}
                  </Link>
                </TableCell>
                <TableCell className="hidden tabular-nums sm:table-cell">{o.members}</TableCell>
                <TableCell className="tabular-nums">{o.nodes}</TableCell>
                <TableCell className="hidden tabular-nums sm:table-cell">{o.servers}</TableCell>
                <TableCell className="hidden text-muted-foreground md:table-cell">
                  {when(o.createdAt)}
                </TableCell>
                <TableCell className="text-right">
                  {o.suspendedAt ? (
                    <Badge variant="destructive">Suspended</Badge>
                  ) : (
                    <Badge variant="secondary">Active</Badge>
                  )}
                </TableCell>
              </LinkRow>
            ))}
          </TableBody>
        </Table>
      </Card>
      <p className="text-xs text-muted-foreground">
        The newest 50 that match. Opening an org is logged.
      </p>
    </div>
  );
}
