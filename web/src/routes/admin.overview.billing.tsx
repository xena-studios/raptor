import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Archive, CircleDollarSign, HardDrive, Info } from "lucide-react";
import { useState } from "react";

import { DaysPicker, dollars } from "@/components/admin/insights";
import { LinkRow } from "@/components/entity-list";
import { Graph, StatCard } from "@/components/stats";
import { TimeChart } from "@/components/time-chart";
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
import { formatBytes } from "@/lib/metrics";

export const Route = createFileRoute("/admin/overview/billing")({
  component: BillingInsights,
});

// Billing: until billing exists, Raptor Backup Storage and what it would
// bill, said as such.
function BillingInsights() {
  const [days, setDays] = useState(30);
  const q = useQuery(
    StaffService.method.getBillingInsights,
    { days },
    { refetchInterval: 5 * 60_000 },
  );
  if (q.error) return <p className="text-sm text-destructive">{message(q.error)}</p>;
  const d = q.data;
  if (!d) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const to = Date.now();
  const from = to - days * 86400_000;
  return (
    <div className="flex flex-col gap-6">
      {!d.billingLive && (
        <Card size="sm">
          <CardContent className="flex items-start gap-2 text-sm text-muted-foreground">
            <Info className="mt-0.5 size-4 shrink-0" />
            Billing isn't live yet, so nothing is charged. These are what Raptor Backup Storage
            would bill a month at today's usage ({formatBytes(Number(d.includedBytes))} included per
            node, then {dollars(d.centsPerTb)} a TB). Plans and invoices arrive with billing.
          </CardContent>
        </Card>
      )}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-3">
        <StatCard
          icon={CircleDollarSign}
          label={d.billingLive ? "Monthly revenue" : "Would bill a month"}
          value={dollars(d.storageCentsMonth)}
          sub="Raptor Backup Storage"
        />
        <StatCard
          icon={Archive}
          label="Stored"
          value={formatBytes(Number(d.storageBytes))}
          sub={`${d.storageOrgs.length} orgs`}
        />
        <StatCard
          icon={HardDrive}
          label="Nodes on Backup Storage"
          value={String(d.storageNodes)}
          sub={`${formatBytes(d.storageNodes * Number(d.includedBytes))} included`}
          className="col-span-2 lg:col-span-1"
        />
      </div>
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>Stored over time</CardTitle>
            <CardDescription>
              Measured once a day, current versions only (what's billed).
            </CardDescription>
          </div>
          <DaysPicker value={days} onChange={setDays} />
        </CardHeader>
        <CardContent>
          <Graph title="Backup Storage">
            <TimeChart
              data={d.storageByDay.map((p) => ({
                t: new Date(`${p.day}T12:00:00Z`).getTime(),
                values: [Number(p.bytes)],
              }))}
              series={[{ name: "Stored", color: "var(--series-1)" }]}
              format={formatBytes}
              minMax={1 << 30}
              from={from}
              to={to}
              gap={2 * 86400_000}
              dots
            />
          </Graph>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>By org</CardTitle>
          <CardDescription>The 50 storing the most.</CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Org</TableHead>
                <TableHead className="text-right">Nodes</TableHead>
                <TableHead className="text-right">Stored</TableHead>
                <TableHead className="text-right">A month</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {d.storageOrgs.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="text-muted-foreground">
                    No org uses Backup Storage yet.
                  </TableCell>
                </TableRow>
              )}
              {d.storageOrgs.map((o) => (
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
                  <TableCell className="text-right font-mono text-xs tabular-nums">
                    {o.nodes}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums">
                    {formatBytes(Number(o.bytes))}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums">
                    {dollars(o.centsMonth)}
                  </TableCell>
                </LinkRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
