import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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

export const Route = createFileRoute("/admin/staff")({
  component: StaffPage,
});

// Raptor's staff, and everything they've done (the log can't be changed).
function StaffPage() {
  const staff = useQuery(StaffService.method.listStaff, {});
  const [pages, setPages] = useState<string[]>([""]);
  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Staff</CardTitle>
          <CardDescription>
            Added and removed only on the Panel's server:{" "}
            <code>panel staff add &lt;email&gt; admin</code> or <code>support</code>.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {staff.error && <p className="text-sm text-destructive">{message(staff.error)}</p>}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Who</TableHead>
                <TableHead>Role</TableHead>
                <TableHead className="text-right">Added</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {staff.data?.staff.map((s) => (
                <TableRow key={s.userId}>
                  <TableCell>{s.name ? `${s.name} (${s.email})` : s.email}</TableCell>
                  <TableCell>
                    <Badge variant="outline" className="capitalize">
                      {s.role}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-right text-muted-foreground">
                    {when(s.addedAt)} by {s.addedBy}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Staff log</CardTitle>
          <CardDescription>
            Everything staff do, including who looked at which account. Nobody can change or delete
            it.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {pages.map((token, i) => (
            <LogPage
              key={token || "first"}
              token={token}
              last={i === pages.length - 1}
              onMore={(t) => setPages([...pages, t])}
            />
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

const actionWords: Record<string, string> = {
  "staff.add": "made staff",
  "staff.remove": "removed from staff",
  "user.view": "opened the account",
  "user.suspend": "suspended the account",
  "user.unsuspend": "lifted the account's suspension",
  "user.signout": "signed the account out everywhere",
  "org.view": "opened the org",
  "org.suspend": "suspended the org",
  "org.unsuspend": "lifted the org's suspension",
  "announcement.create": "published an announcement",
  "announcement.update": "changed an announcement",
  "announcement.end": "ended an announcement",
  "support.request": "asked for support access to node",
  "support.command": "used support access on node",
};

function LogPage({
  token,
  last,
  onMore,
}: {
  token: string;
  last: boolean;
  onMore: (t: string) => void;
}) {
  const log = useQuery(StaffService.method.listStaffAudit, { pageToken: token });
  if (log.error) return <p className="text-sm text-destructive">{message(log.error)}</p>;
  if (!log.data) return <p className="text-sm text-muted-foreground">Loading…</p>;
  return (
    <>
      <ul className="divide-y text-sm">
        {log.data.entries.map((e) => {
          let reason = "";
          try {
            reason = (JSON.parse(e.metaJson) as { reason?: string }).reason ?? "";
          } catch {
            reason = "";
          }
          return (
            <li
              key={e.id}
              className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5 py-2"
            >
              <span>
                <span className="font-medium">{e.staffEmail}</span>{" "}
                {actionWords[e.action] ?? e.action.replace(/[._]/g, " ")}{" "}
                <code className="text-xs text-muted-foreground">{e.targetId.slice(0, 8)}</code>
                {reason && <span className="text-muted-foreground">: {reason}</span>}
              </span>
              <span
                className="text-xs text-muted-foreground"
                title={e.at ? timestampDate(e.at).toLocaleString() : ""}
              >
                {when(e.at)}
                {e.ip && ` · ${e.ip}`}
              </span>
            </li>
          );
        })}
      </ul>
      {log.data.entries.length === 0 && !token && (
        <p className="text-sm text-muted-foreground">Nothing yet.</p>
      )}
      {last && log.data.nextPageToken && (
        <Button
          size="sm"
          variant="outline"
          className="self-start"
          onClick={() => onMore(log.data.nextPageToken)}
        >
          Show more
        </Button>
      )}
    </>
  );
}
