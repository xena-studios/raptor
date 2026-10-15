import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import {
  Archive,
  CircleDollarSign,
  CreditCard,
  HardDrive,
  Info,
  TriangleAlert,
  Users,
} from "lucide-react";
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

const planLabel: Record<string, string> = {
  trialing: "Free month",
  active: "Active",
  past_due: "Payment failed",
  canceled: "Ended",
  unpaid: "Unpaid",
  incomplete: "Incomplete",
  incomplete_expired: "Never started",
  paused: "Paused",
};

function planOrgs(plans: { status: string; orgs: number }[], ...statuses: string[]) {
  return plans.filter((p) => statuses.includes(p.status)).reduce((n, p) => n + p.orgs, 0);
}

export const Route = createFileRoute("/admin/overview/billing")({
  component: BillingInsights,
});

// Billing: plans, recurring revenue, who's unpaid, and payments by month
// (docs/PANEL.md#billing-polar); then Raptor Backup Storage, which isn't
// charged yet, and what it would bill.
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
      {d.billingLive ? (
        <>
          <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
            <StatCard
              icon={CircleDollarSign}
              label="Recurring revenue"
              value={`${dollars(d.mrrCents)} / mo`}
              sub={`${d.seatsBilled} nodes on paid plans`}
            />
            <StatCard
              icon={CreditCard}
              label="Paying orgs"
              value={String(planOrgs(d.plans, "active", "past_due"))}
              sub={`${planOrgs(d.plans, "past_due")} with a failed payment`}
            />
            <StatCard
              icon={Users}
              label="On the free month"
              value={String(planOrgs(d.plans, "trialing"))}
              sub="first node, once per org"
            />
            <StatCard
              icon={TriangleAlert}
              label="Unpaid"
              value={String(d.unpaidOrgs)}
              sub={`${d.readOnlyOrgs} read-only`}
            />
          </div>
          <div className="grid items-start gap-6 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>Paid by month</CardTitle>
                <CardDescription>
                  Orders paid, less refunds, from the billing ledger.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Month</TableHead>
                      <TableHead className="text-right">Paid</TableHead>
                      <TableHead className="text-right">Refunded</TableHead>
                      <TableHead className="text-right">Net</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {d.revenueByMonth.length === 0 && (
                      <TableRow>
                        <TableCell colSpan={4} className="text-muted-foreground">
                          Nothing paid yet.
                        </TableCell>
                      </TableRow>
                    )}
                    {[...d.revenueByMonth].reverse().map((m) => (
                      <TableRow key={m.month}>
                        <TableCell className="font-mono text-xs">{m.month}</TableCell>
                        <TableCell className="text-right font-mono text-xs tabular-nums">
                          {dollars(m.paidCents)}
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs tabular-nums">
                          {dollars(m.refundedCents)}
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs tabular-nums">
                          {dollars(m.paidCents - m.refundedCents)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>Plans</CardTitle>
                <CardDescription>Orgs by their subscription's status in Polar.</CardDescription>
              </CardHeader>
              <CardContent>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Status</TableHead>
                      <TableHead className="text-right">Orgs</TableHead>
                      <TableHead className="text-right">Nodes</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {d.plans.length === 0 && (
                      <TableRow>
                        <TableCell colSpan={3} className="text-muted-foreground">
                          No plans yet.
                        </TableCell>
                      </TableRow>
                    )}
                    {d.plans.map((p) => (
                      <TableRow key={p.status}>
                        <TableCell>{planLabel[p.status] ?? p.status}</TableCell>
                        <TableCell className="text-right font-mono text-xs tabular-nums">
                          {p.orgs}
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs tabular-nums">
                          {p.seats}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>
          </div>
        </>
      ) : (
        <Card size="sm">
          <CardContent className="flex items-start gap-2 text-sm text-muted-foreground">
            <Info className="mt-0.5 size-4 shrink-0" />
            Billing is off on this Panel (no Polar token or product), so nothing is charged.
          </CardContent>
        </Card>
      )}
      <Card size="sm">
        <CardContent className="flex items-start gap-2 text-sm text-muted-foreground">
          <Info className="mt-0.5 size-4 shrink-0" />
          Raptor Backup Storage isn't charged yet. Below is what it would bill a month at today's
          usage ({formatBytes(Number(d.includedBytes))} included per node, then{" "}
          {dollars(d.centsPerTb)} a TB).
        </CardContent>
      </Card>
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-3">
        <StatCard
          icon={CircleDollarSign}
          label="Storage would bill"
          value={dollars(d.storageCentsMonth)}
          sub="a month, Raptor Backup Storage"
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
