import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  CircleMinus,
  Loader2,
  RefreshCw,
  XCircle,
} from "lucide-react";
import { type ReactNode, useState } from "react";

import { DetailList } from "@/components/page";
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
import { message } from "@/lib/errors";
import { formatBytes, formatPercent } from "@/lib/metrics";
import { useOrgNodes, useOrgServers } from "@/lib/org-data";
import { commandClient } from "@/lib/transport";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/orgs/$orgId/nodes/$nodeId/health")({
  component: Health,
});

// What Wings' node.health returns (internal/wings/actions/health.go).
type Check = {
  id: string;
  title: string;
  status: "pass" | "warn" | "fail" | "skip";
  detail?: string;
  why?: string;
  fix?: string;
};
type Report = {
  host: {
    cpus: number;
    load1: number;
    load5: number;
    load15: number;
    memory_total: number;
    memory_available: number;
    uptime_seconds: number;
  };
  disk_total: number;
  disk_free: number;
  servers: { id: string; state: string; cpu: number; memory: number; disk: number }[] | null;
  checks: Check[] | null;
  checked_at?: number;
  checking: boolean;
};

function uptime(s: number): string {
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  return d > 0 ? `${d}d ${h}h` : h > 0 ? `${h}h ${m}m` : `${m}m`;
}

// The node's health: the machine's resources now, what each server uses,
// and `raptor doctor`'s checks, run by Wings on the node.
function Health() {
  const { orgId, nodeId } = Route.useParams();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  const { servers } = useOrgServers(orgId);
  const [starting, setStarting] = useState(false);
  const run = async (action: string) => {
    const res = await commandClient.execute({ nodeId, action, paramsJson: "{}" });
    return JSON.parse(res.resultJson) as Report;
  };
  const report = useQuery({
    queryKey: ["node-health", nodeId],
    queryFn: () => run("node.health"),
    enabled: !!node?.connected,
    refetchInterval: (q) => (q.state.data?.checking ? 2_000 : 15_000),
  });

  if (node && !node.connected) {
    return (
      <Card>
        <CardContent className="text-sm text-muted-foreground">
          {node.name} is offline, so its health can't be checked. It reports again when it's back.
        </CardContent>
      </Card>
    );
  }
  if (report.error) return <p className="text-sm text-destructive">{message(report.error)}</p>;
  const r = report.data;
  if (!r) return <p className="text-sm text-muted-foreground">Asking the node…</p>;

  const h = r.host;
  const memUsed = h.memory_total - h.memory_available;
  const diskUsed = r.disk_total - r.disk_free;
  const checks = r.checks ?? [];
  const problems = checks.filter((c) => c.status === "fail" || c.status === "warn");
  const fine = checks.filter((c) => c.status === "pass" || c.status === "skip");
  const names = new Map(servers.filter((s) => s.node.id === nodeId).map((s) => [s.id, s.name]));
  const usage = [...(r.servers ?? [])].sort((a, b) => b.memory - a.memory);

  return (
    <div className="flex flex-col gap-6">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Meter
          label="CPU load"
          value={`${h.load1.toFixed(2)}`}
          sub={`${h.cpus} cores · 5 min ${h.load5.toFixed(2)} · 15 min ${h.load15.toFixed(2)}`}
          fraction={h.cpus ? h.load1 / h.cpus : undefined}
        />
        <Meter
          label="Memory"
          value={h.memory_total ? formatBytes(memUsed) : "—"}
          sub={h.memory_total ? `of ${formatBytes(h.memory_total)}` : "Not reported"}
          fraction={h.memory_total ? memUsed / h.memory_total : undefined}
        />
        <Meter
          label="Server storage"
          value={r.disk_total ? formatBytes(diskUsed) : "—"}
          sub={r.disk_total ? `of ${formatBytes(r.disk_total)}` : "Not reported"}
          fraction={r.disk_total ? diskUsed / r.disk_total : undefined}
        />
        <Meter
          label="Up for"
          value={h.uptime_seconds ? uptime(h.uptime_seconds) : "—"}
          sub={`Wings ${node?.wingsVersion || "?"} · ${node?.arch || ""}`}
        />
      </div>

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>Checks</CardTitle>
            <CardDescription>
              {r.checking
                ? "Checking the node: Docker, storage, the firewall, the clock, DNS, and the Panel…"
                : r.checked_at
                  ? `Checked ${new Date(r.checked_at).toLocaleString()}. The same checks as sudo raptor doctor on the node.`
                  : "The same checks as sudo raptor doctor on the node."}
            </CardDescription>
          </div>
          <Button
            size="sm"
            variant="outline"
            disabled={r.checking || starting}
            onClick={async () => {
              setStarting(true);
              try {
                await run("node.doctor");
                await report.refetch();
              } finally {
                setStarting(false);
              }
            }}
          >
            {r.checking ? <Loader2 className="animate-spin" /> : <RefreshCw />}
            {r.checking ? "Checking…" : "Check again"}
          </Button>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {checks.length === 0 && r.checking && (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> This takes up to a minute.
            </p>
          )}
          {checks.length > 0 && problems.length === 0 && (
            <p className="flex items-center gap-2 text-sm">
              <CheckCircle2 className="size-4 text-emerald-500" /> Everything checks out.
            </p>
          )}
          {problems.map((c) => (
            <Problem key={`${c.id}-${c.title}`} c={c} />
          ))}
          {fine.length > 0 && <Fine checks={fine} />}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Servers on this node</CardTitle>
          <CardDescription>What each uses right now.</CardDescription>
        </CardHeader>
        <CardContent>
          {usage.length === 0 ? (
            <p className="text-sm text-muted-foreground">No servers yet.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Server</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead className="text-right">CPU</TableHead>
                  <TableHead className="text-right">Memory</TableHead>
                  <TableHead className="text-right">Disk</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {usage.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell>
                      {names.get(s.id) ?? <code className="text-xs">{s.id.slice(-8)}</code>}
                    </TableCell>
                    <TableCell className="capitalize">{s.state.replace("_", " ")}</TableCell>
                    <TableCell className="text-right tabular-nums">
                      {s.state === "running" ? formatPercent(s.cpu) : "—"}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {s.state === "running" ? formatBytes(s.memory) : "—"}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {s.disk ? formatBytes(s.disk) : "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function Meter({
  label,
  value,
  sub,
  fraction,
}: {
  label: string;
  value: string;
  sub: string;
  fraction?: number;
}) {
  const f = fraction === undefined ? undefined : Math.min(1, Math.max(0, fraction));
  return (
    <Card size="sm">
      <CardContent className="flex flex-col gap-1.5">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className="text-xl font-semibold tabular-nums">{value}</p>
        {f !== undefined && (
          <div className="h-1 overflow-hidden rounded-full bg-muted">
            <div
              className={cn(
                "h-full rounded-full",
                f > 0.9 ? "bg-destructive" : f > 0.75 ? "bg-amber-500" : "bg-[var(--series-1)]",
              )}
              style={{ width: `${f * 100}%` }}
            />
          </div>
        )}
        <p className="truncate text-xs text-muted-foreground">{sub}</p>
      </CardContent>
    </Card>
  );
}

function Problem({ c }: { c: Check }) {
  const failed = c.status === "fail";
  return (
    <div
      className={cn(
        "flex gap-3 rounded-lg border p-3 text-sm",
        failed ? "border-destructive/40 bg-destructive/5" : "border-amber-500/40 bg-amber-500/5",
      )}
    >
      {failed ? (
        <XCircle className="mt-0.5 size-4 shrink-0 text-destructive" />
      ) : (
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-500" />
      )}
      <div className="min-w-0 space-y-1">
        <p className="font-medium">
          {c.title}{" "}
          <Badge variant={failed ? "destructive" : "outline"} className="ml-1">
            {failed ? "Failing" : "Warning"}
          </Badge>
        </p>
        {c.detail && <p>{c.detail}</p>}
        {c.why && <Line label="Why it matters">{c.why}</Line>}
        {c.fix && <Line label="How to fix it">{c.fix}</Line>}
      </div>
    </div>
  );
}

function Line({ label, children }: { label: string; children: ReactNode }) {
  return (
    <p className="text-muted-foreground">
      <span className="font-medium text-foreground">{label}: </span>
      {children}
    </p>
  );
}

function Fine({ checks }: { checks: Check[] }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded-lg border">
      <button
        type="button"
        className="flex w-full items-center justify-between px-3 py-2 text-sm"
        onClick={() => setOpen(!open)}
      >
        <span className="flex items-center gap-2">
          <CheckCircle2 className="size-4 text-emerald-500" />
          {checks.filter((c) => c.status === "pass").length} passed
          {checks.some((c) => c.status === "skip") &&
            `, ${checks.filter((c) => c.status === "skip").length} didn't apply`}
        </span>
        <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
      </button>
      {open && (
        <div className="border-t px-3 py-2">
          <DetailList
            rows={checks.map((c) => [
              c.title,
              <span key={c.id} className="flex items-center justify-end gap-1.5">
                {c.status === "pass" ? (
                  <CheckCircle2 className="size-3.5 shrink-0 text-emerald-500" />
                ) : (
                  <CircleMinus className="size-3.5 shrink-0 text-muted-foreground" />
                )}
                <span className="truncate text-muted-foreground">{c.detail}</span>
              </span>,
            ])}
          />
        </div>
      )}
    </div>
  );
}
