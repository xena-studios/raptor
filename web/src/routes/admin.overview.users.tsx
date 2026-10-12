import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { KeyRound, ShieldOff, UserCheck, Users } from "lucide-react";
import { useState } from "react";

import { Breakdown, DaysPicker, dayPoints, methodNames, pct } from "@/components/admin/insights";
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

export const Route = createFileRoute("/admin/overview/users")({
  component: UserInsights,
});

const count = (v: number) => String(Math.round(v));

// Users: who's here, who comes back, how they sign in, and the biggest orgs.
function UserInsights() {
  const [days, setDays] = useState(30);
  const q = useQuery(
    StaffService.method.getUserInsights,
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
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard
          icon={Users}
          label="Accounts"
          value={String(d.users)}
          sub={`${d.withoutOrg} in no org · ${d.openInvitations} invitations open`}
        />
        <StatCard
          icon={UserCheck}
          label="Active"
          value={String(d.active30d)}
          sub={`in 30 days · ${d.active7d} in 7 · ${d.active1d} today`}
        />
        <StatCard
          icon={KeyRound}
          label="With a passkey"
          value={pct(d.withPasskey, d.users)}
          sub={`${d.withPasskey} accounts · ${d.withTotp} with an authenticator app`}
          fraction={d.users ? d.withPasskey / d.users : 0}
        />
        <StatCard
          icon={ShieldOff}
          label="Suspended"
          value={String(d.suspended)}
          sub="Can't sign in"
        />
      </div>
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>Over time</CardTitle>
            <CardDescription>Accounts, sign-ups, and accounts signing in each day.</CardDescription>
          </div>
          <DaysPicker value={days} onChange={setDays} />
        </CardHeader>
        <CardContent className="grid gap-8 md:grid-cols-3">
          <Graph title="Accounts">
            <TimeChart
              data={d.history.map((p) => ({
                t: p.at ? timestampDate(p.at).getTime() : 0,
                values: [p.users],
              }))}
              series={[{ name: "Accounts", color: "var(--series-1)" }]}
              format={count}
              minMax={4}
              from={from}
              to={to}
              gap={3 * 3600_000}
              dots
            />
          </Graph>
          <Graph title="Sign-ups" note="Per day">
            <TimeChart
              data={dayPoints(d.signupsByDay)}
              series={[{ name: "Sign-ups", color: "var(--series-1)" }]}
              format={count}
              minMax={4}
              from={from}
              to={to}
              gap={2 * 86400_000}
              dots
            />
          </Graph>
          <Graph title="Signed in" note="Accounts per day">
            <TimeChart
              data={dayPoints(d.activeByDay)}
              series={[{ name: "Accounts", color: "var(--series-2)" }]}
              format={count}
              minMax={4}
              from={from}
              to={to}
              gap={2 * 86400_000}
              dots
            />
          </Graph>
        </CardContent>
      </Card>
      <div className="grid items-start gap-6 lg:grid-cols-3">
        <Breakdown
          title="How people sign in (30 days)"
          buckets={d.signinMethods}
          names={methodNames}
        />
        <Breakdown title="Linked accounts" buckets={d.oauthProviders} names={methodNames} />
        <Card size="sm">
          <CardHeader>
            <CardTitle>Largest orgs</CardTitle>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Org</TableHead>
                  <TableHead className="text-right">Members</TableHead>
                  <TableHead className="text-right">Servers</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.largestOrgs.map((o) => (
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
                    <TableCell className="text-right tabular-nums">{o.members}</TableCell>
                    <TableCell className="text-right tabular-nums">{o.servers}</TableCell>
                  </LinkRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
