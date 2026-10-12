import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";

import { durationText, supportLevels } from "@/components/support-access";
import { Badge } from "@/components/ui/badge";
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

export const Route = createFileRoute("/admin/support/")({
  component: SupportList,
});

const statusVariant: Record<string, "default" | "secondary" | "outline" | "destructive"> = {
  pending: "secondary",
  approved: "default",
  denied: "destructive",
  revoked: "outline",
  expired: "outline",
};

// Support requests: asked from an org's page; open an approved one of yours
// to work on the node.
function SupportList() {
  const list = useQuery(StaffService.method.listSupportRequests, {}, { refetchInterval: 30_000 });
  return (
    <Card>
      <CardHeader>
        <CardTitle>Support access</CardTitle>
        <CardDescription>
          Ask from an org's page. The owner approves with their passkey, and every command you send
          is checked by the node against what they signed.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead className="hidden md:table-cell">Asked by</TableHead>
              <TableHead>Access</TableHead>
              <TableHead className="hidden sm:table-cell">Ticket</TableHead>
              <TableHead className="text-right">Status</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.data?.requests.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="text-muted-foreground">
                  No requests yet.
                </TableCell>
              </TableRow>
            )}
            {list.data?.requests.map((r) => (
              <TableRow key={r.id}>
                <TableCell>
                  {r.status === "approved" && r.mine ? (
                    <Link
                      to="/admin/support/$requestId"
                      params={{ requestId: r.id }}
                      className="font-medium hover:underline"
                    >
                      {r.nodeName}
                    </Link>
                  ) : (
                    <span className="font-medium">{r.nodeName}</span>
                  )}
                  <span className="block text-xs text-muted-foreground">{r.orgName}</span>
                </TableCell>
                <TableCell className="hidden text-muted-foreground md:table-cell">
                  {r.staffEmail}
                </TableCell>
                <TableCell>
                  {supportLevels[r.grantedLevel || r.level]?.name}
                  <span className="block text-xs text-muted-foreground">
                    {r.status === "approved"
                      ? `until ${when(r.expiresAt)}`
                      : durationText(r.minutes)}
                  </span>
                </TableCell>
                <TableCell className="hidden sm:table-cell">{r.ticket}</TableCell>
                <TableCell className="text-right">
                  <Badge variant={statusVariant[r.status] ?? "outline"} className="capitalize">
                    {r.status}
                  </Badge>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}
