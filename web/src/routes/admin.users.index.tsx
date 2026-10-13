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

export const Route = createFileRoute("/admin/users/")({
  component: UsersPage,
});

function UsersPage() {
  const [query, setQuery] = useState("");
  const q = useDeferredValue(query.trim());
  const users = useQuery(StaffService.method.searchUsers, { query: q });
  return (
    <div className="flex flex-col gap-4">
      <div className="relative w-full sm:max-w-sm">
        <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
        <Input
          type="search"
          aria-label="Search accounts"
          placeholder="Email, name, or ID"
          className="pl-8"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>
      {users.error && <p className="text-sm text-destructive">{message(users.error)}</p>}
      <Card className="py-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Account</TableHead>
              <TableHead className="hidden sm:table-cell">Orgs</TableHead>
              <TableHead className="hidden md:table-cell">Passkeys</TableHead>
              <TableHead className="hidden md:table-cell">Joined</TableHead>
              <TableHead className="hidden sm:table-cell">Last seen</TableHead>
              <TableHead className="text-right">Status</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.data?.users.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-muted-foreground">
                  No accounts match.
                </TableCell>
              </TableRow>
            )}
            {users.data?.users.map((u) => (
              <LinkRow key={u.id} link={{ to: "/admin/users/$userId", params: { userId: u.id } }}>
                <TableCell>
                  <Link
                    to="/admin/users/$userId"
                    params={{ userId: u.id }}
                    className="flex flex-col hover:underline"
                  >
                    <span className="font-medium">{u.name || u.email}</span>
                    {u.name && <span className="text-xs text-muted-foreground">{u.email}</span>}
                  </Link>
                </TableCell>
                <TableCell className="hidden font-mono text-xs tabular-nums sm:table-cell">
                  {u.orgs}
                </TableCell>
                <TableCell className="hidden font-mono text-xs tabular-nums md:table-cell">
                  {u.passkeys}
                </TableCell>
                <TableCell className="hidden text-muted-foreground md:table-cell">
                  {when(u.createdAt)}
                </TableCell>
                <TableCell className="hidden text-muted-foreground sm:table-cell">
                  {u.lastSeenAt ? when(u.lastSeenAt) : "Never"}
                </TableCell>
                <TableCell className="text-right">
                  <span className="inline-flex gap-1.5">
                    {u.staffRole && (
                      <Badge variant="outline" className="capitalize">
                        {u.staffRole}
                      </Badge>
                    )}
                    {u.suspendedAt ? (
                      <Badge variant="destructive">Suspended</Badge>
                    ) : (
                      <Badge variant="secondary">Active</Badge>
                    )}
                  </span>
                </TableCell>
              </LinkRow>
            ))}
          </TableBody>
        </Table>
      </Card>
      <p className="text-xs text-muted-foreground">
        The newest 50 that match. Opening an account is logged.
      </p>
    </div>
  );
}
