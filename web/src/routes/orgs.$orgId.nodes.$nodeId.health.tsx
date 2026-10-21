import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { cn } from "cn";
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
import { UsageBar } from "@/components/stats";
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
import { dateTime } from "@/lib/format";
import { useIds } from "@/lib/ids";
import { formatBytes, formatUptime } from "@/lib/metrics";
import { useOrgNodes } from "@/lib/org-data";
import { commandClient } from "@/lib/transport";

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
  details: {
    os: string;
    kernel: string;
    cpu_model: string;
    memory_cache: number;
    swap_total: number;
    swap_free: number;
    interfaces:
      | {
          name: string;
          up: boolean;
          physical: boolean;
          addresses: string[] | null;
          rx_bytes: number;
          tx_bytes: number;
        }[]
      | null;
  };
  disk_total: number;
  disk_free: number;
  disks: { paths: { path: string; label?: string }[]; total: number; free: number }[] | null;
  checks: Check[] | null;
  checked_at?: number;
  checking: boolean;
};

// The node's health, in detail: `raptor doctor`'s checks, run by Wings on
// the node, then the machine: its system, load, memory, disks, and network.
// The overview has the numbers at a glance and over time.
function Health() {
  const { orgId, nodeId } = useIds();
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
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
  const d = r.details;
  const checks = r.checks ?? [];
  const problems = checks.filter((c) => c.status === "fail" || c.status === "warn");
  const fine = checks.filter((c) => c.status === "pass" || c.status === "skip");

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 flex-1 basis-xs space-y-1.5">
            <CardTitle>Checks</CardTitle>
            <CardDescription>
              {r.checking
                ? "Checking the node: Docker, storage, the firewall, the clock, DNS, and the Panel…"
                : r.checked_at
                  ? `Checked ${dateTime(new Date(r.checked_at))}. The same checks as sudo raptor doctor on the node.`
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
              <CheckCircle2 className="size-4 text-ok" /> Everything checks out.
            </p>
          )}
          {problems.map((c) => (
            <Problem key={`${c.id}-${c.title}`} c={c} />
          ))}
          {fine.length > 0 && <Fine checks={fine} />}
        </CardContent>
      </Card>

      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>System</CardTitle>
            <CardDescription>What the machine runs.</CardDescription>
          </CardHeader>
          <CardContent>
            <DetailList
              rows={[
                ["Operating system", d.os || "—"],
                ["Kernel", d.kernel ? <code key="k">{d.kernel}</code> : "—"],
                ...(d.cpu_model ? [["Processor", d.cpu_model] as [string, string]] : []),
                ["Cores", `${h.cpus}${node?.arch ? ` (${node.arch})` : ""}`],
                ["Up for", h.uptime_seconds ? formatUptime(h.uptime_seconds) : "—"],
                [
                  "Started",
                  h.uptime_seconds ? dateTime(new Date(Date.now() - h.uptime_seconds * 1000)) : "—",
                ],
                ["Wings", node?.wingsVersion || "—"],
              ]}
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Load and memory</CardTitle>
            <CardDescription>
              Load is how many processes want a core: up to {h.cpus} on this machine is fine.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-5">
            <div className="grid grid-cols-3 gap-3">
              {(
                [
                  ["1 minute", h.load1],
                  ["5 minutes", h.load5],
                  ["15 minutes", h.load15],
                ] as const
              ).map(([label, v]) => (
                <div key={label} className="flex flex-col gap-1.5">
                  <p className="text-xs text-muted-foreground">{label}</p>
                  <p className="font-mono text-lg font-medium tabular-nums">{v.toFixed(2)}</p>
                  <UsageBar fraction={h.cpus ? v / h.cpus : 0} />
                </div>
              ))}
            </div>
            <Memory
              total={h.memory_total}
              available={h.memory_available}
              cache={d.memory_cache}
              swapTotal={d.swap_total}
              swapFree={d.swap_free}
            />
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Disks</CardTitle>
          <CardDescription>
            Where Raptor keeps servers, backups, and its own state, by filesystem.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {(r.disks ?? []).length === 0 && (
            <p className="text-sm text-muted-foreground">Not reported.</p>
          )}
          {(r.disks ?? []).map((disk) => {
            const used = disk.total - disk.free;
            return (
              <div key={disk.paths[0]?.path} className="flex flex-col gap-1.5">
                <div className="flex flex-wrap items-baseline justify-between gap-x-4 text-sm">
                  <span className="font-medium">
                    {disk.paths
                      .map((p) => p.label)
                      .filter(Boolean)
                      .join(", ") || disk.paths[0]?.path}
                  </span>
                  <span className="tabular-nums text-muted-foreground">
                    {formatBytes(used)} of {formatBytes(disk.total)} · {formatBytes(disk.free)} free
                  </span>
                </div>
                <UsageBar fraction={disk.total ? used / disk.total : 0} />
                <p className="truncate font-mono text-xs text-muted-foreground">
                  {disk.paths.map((p) => p.path).join("  ")}
                </p>
              </div>
            );
          })}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Network</CardTitle>
          <CardDescription>
            The machine's interfaces and their traffic since it started. Servers' own interfaces
            aren't listed: their pages show their traffic.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Interface</TableHead>
                <TableHead>Addresses</TableHead>
                <TableHead className="text-right">In</TableHead>
                <TableHead className="text-right">Out</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(d.interfaces ?? []).map((i) => (
                <TableRow key={i.name}>
                  <TableCell>
                    <span className="flex items-center gap-2">
                      <span
                        className={cn(
                          "size-2 shrink-0 rounded-full",
                          i.up ? "bg-ok" : "bg-muted-foreground/40",
                        )}
                        title={i.up ? "Up" : "Down"}
                      />
                      <span className="font-mono text-xs">{i.name}</span>
                      {i.physical && <Badge variant="outline">Network card</Badge>}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs whitespace-normal text-muted-foreground">
                    {(i.addresses ?? []).join(", ") || "—"}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums">
                    {formatBytes(i.rx_bytes)}
                  </TableCell>
                  <TableCell className="text-right font-mono text-xs tabular-nums">
                    {formatBytes(i.tx_bytes)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}

// Memory is what's used by programs, what the kernel caches (and gives back
// when programs need it), what's free, and swap.
function Memory({
  total,
  available,
  cache,
  swapTotal,
  swapFree,
}: {
  total: number;
  available: number;
  cache: number;
  swapTotal: number;
  swapFree: number;
}) {
  if (!total) return <p className="text-sm text-muted-foreground">Memory isn't reported.</p>;
  const used = total - available;
  const cached = Math.min(cache, available);
  const free = Math.max(0, total - used - cached);
  const parts = [
    { label: "In use", bytes: used, className: "bg-[var(--series-1)]" },
    { label: "Cache", bytes: cached, className: "bg-[var(--series-1)]/35" },
    { label: "Free", bytes: free, className: "bg-muted" },
  ];
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between text-sm">
        <span className="font-medium">Memory</span>
        <span className="tabular-nums text-muted-foreground">
          {formatBytes(used)} of {formatBytes(total)} in use
        </span>
      </div>
      <div className="flex h-2 overflow-hidden rounded-full bg-muted">
        {parts.map((p) => (
          <div
            key={p.label}
            className={p.className}
            style={{ width: `${(p.bytes / total) * 100}%` }}
          />
        ))}
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        {parts.map((p) => (
          <span key={p.label} className="flex items-center gap-1.5">
            <span className={cn("size-2 rounded-sm", p.className)} />
            {p.label} <span className="text-foreground tabular-nums">{formatBytes(p.bytes)}</span>
          </span>
        ))}
        <span>
          Swap{" "}
          <span className="text-foreground tabular-nums">
            {swapTotal
              ? `${formatBytes(swapTotal - swapFree)} of ${formatBytes(swapTotal)}`
              : "none"}
          </span>
        </span>
      </div>
    </div>
  );
}

function Problem({ c }: { c: Check }) {
  const failed = c.status === "fail";
  return (
    <div
      className={cn(
        "flex gap-3 rounded-lg border p-3 text-sm",
        failed ? "border-destructive/40 bg-danger-wash" : "border-warn/40 bg-warn-wash",
      )}
    >
      {failed ? (
        <XCircle className="mt-0.5 size-4 shrink-0 text-destructive" />
      ) : (
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warn" />
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
          <CheckCircle2 className="size-4 text-ok" />
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
                  <CheckCircle2 className="size-3.5 shrink-0 text-ok" />
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
