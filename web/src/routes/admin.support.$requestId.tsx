import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { FolderOpen, Loader2, RefreshCw, SquareTerminal, X } from "lucide-react";
import { lazy, Suspense, useState } from "react";

import { Files } from "@/components/file-manager";
import { StatusBadge } from "@/components/server-status";
import { supportLevels } from "@/components/support-access";
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
import { formatBytes, formatPercent, formatUptime } from "@/lib/metrics";
import { serverStatus } from "@/lib/servers";
import { commandClient } from "@/lib/transport";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/admin/support/$requestId")({
  component: SupportSession,
});

const Console = lazy(() => import("@/components/console"));

type Check = { id: string; title: string; status: string; detail?: string; fix?: string };
type Health = {
  host: {
    cpus: number;
    load1: number;
    memory_total: number;
    memory_available: number;
    uptime_seconds: number;
  };
  details: { os: string; kernel: string };
  disk_total: number;
  disk_free: number;
  servers: { id: string; state: string; cpu: number; memory: number }[] | null;
  checks: Check[] | null;
  checking: boolean;
};

// Working on a node under support access: its health and checks, Wings'
// log, (at level 2) its servers' power and consoles, and (at level 3)
// their files. Every command names the request; the node checks it
// against what the owner signed.
function SupportSession() {
  const { requestId } = Route.useParams();
  const session = Route.useRouteContext();
  const list = useConnectQuery(StaffService.method.listSupportRequests, {});
  const r = list.data?.requests.find((x) => x.id === requestId);
  const [lines, setLines] = useState<string>();
  // The server whose console or files are open.
  const [open, setOpen] = useState<{ server: string; view: "console" | "files" }>();
  const [where, setWhere] = useState<{ path: string; file?: string }>({ path: "" });
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  const run = async <T,>(action: string, params: object = {}, serverId = "") => {
    if (!r) throw new Error("No support access.");
    const res = await commandClient.execute({
      nodeId: r.nodeId,
      serverId,
      action,
      paramsJson: JSON.stringify(params),
      support: requestId,
    });
    return (res.resultJson ? JSON.parse(res.resultJson) : undefined) as T;
  };
  const health = useQuery({
    queryKey: ["support-health", requestId],
    queryFn: () => run<Health>("node.health"),
    enabled: !!r && r.status === "approved",
    refetchInterval: (q) => (q.state.data?.checking ? 2_000 : 30_000),
  });

  if (list.isPending) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (r?.status !== "approved" || !r.mine) {
    return (
      <p className="text-sm text-muted-foreground">
        This support access isn't yours, isn't approved, or is over.{" "}
        <Link to="/admin/support" className="underline">
          Back to support
        </Link>
      </p>
    );
  }
  const h = health.data;
  const operate = r.grantedLevel >= 2;
  const manage = r.grantedLevel >= 3;

  async function act(key: string, fn: () => Promise<unknown>) {
    setBusy(key);
    setError("");
    try {
      await fn();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <Card size="sm">
        <CardContent className="flex flex-wrap items-center justify-between gap-3 text-sm">
          <span>
            <span className="font-medium">{r.nodeName}</span> in {r.orgName} ·{" "}
            {supportLevels[r.grantedLevel]?.name} until {when(r.expiresAt)} · ticket {r.ticket}
          </span>
          <Link to="/admin/support" className="text-muted-foreground hover:underline">
            All requests
          </Link>
        </CardContent>
      </Card>
      {error && <p className="text-sm text-destructive">{error}</p>}
      {health.error && <p className="text-sm text-destructive">{message(health.error)}</p>}

      <div className="grid items-start gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
            <div className="space-y-1.5">
              <CardTitle>Health</CardTitle>
              <CardDescription>
                {h
                  ? `${h.details.os} · ${h.details.kernel} · up ${formatUptime(h.host.uptime_seconds)}`
                  : "Asking the node…"}
              </CardDescription>
            </div>
            <Button
              size="sm"
              variant="outline"
              disabled={!!busy || h?.checking}
              onClick={() =>
                act("doctor", async () => {
                  await run("node.doctor");
                  await health.refetch();
                })
              }
            >
              {h?.checking || busy === "doctor" ? (
                <Loader2 className="animate-spin" />
              ) : (
                <RefreshCw />
              )}{" "}
              Run checks
            </Button>
          </CardHeader>
          <CardContent className="flex flex-col gap-2 text-sm">
            {h && (
              <p className="text-muted-foreground">
                Load {h.host.load1.toFixed(2)} on {h.host.cpus} cores · memory{" "}
                {formatBytes(h.host.memory_total - h.host.memory_available)} of{" "}
                {formatBytes(h.host.memory_total)} · disk {formatBytes(h.disk_total - h.disk_free)}{" "}
                of {formatBytes(h.disk_total)}
              </p>
            )}
            {(h?.checks ?? []).map((c) => (
              <div key={`${c.id}-${c.title}`} className="flex items-start gap-2">
                <span
                  className={cn(
                    "mt-1.5 size-2 shrink-0 rounded-full",
                    c.status === "fail"
                      ? "bg-destructive"
                      : c.status === "warn"
                        ? "bg-warn"
                        : c.status === "pass"
                          ? "bg-ok"
                          : "bg-muted-foreground/40",
                  )}
                />
                <span className="min-w-0">
                  <span className="font-medium">{c.title}</span>
                  {c.detail && <span className="text-muted-foreground"> · {c.detail}</span>}
                </span>
              </div>
            ))}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Servers</CardTitle>
            <CardDescription>
              {manage
                ? "You can power them, use their consoles, and work on their files."
                : operate
                  ? "You can power them and use their consoles. Files need level 3."
                  : "Level 1 looks; level 2 can power them and use their consoles."}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Server</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead className="text-right">CPU · Memory</TableHead>
                  {operate && <TableHead />}
                </TableRow>
              </TableHeader>
              <TableBody>
                {(h?.servers ?? []).map((s) => (
                  <TableRow key={s.id}>
                    <TableCell className="font-mono text-xs">{s.id.slice(-8)}</TableCell>
                    <TableCell>
                      <StatusBadge
                        status={serverStatus({ ...s, installState: "", installError: "" }, true)}
                      />
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs tabular-nums">
                      {s.state === "running"
                        ? `${formatPercent(s.cpu)} · ${formatBytes(s.memory)}`
                        : "—"}
                    </TableCell>
                    {operate && (
                      <TableCell className="text-right">
                        <span className="inline-flex gap-1">
                          <Button
                            size="icon-sm"
                            variant={
                              open?.server === s.id && open.view === "console"
                                ? "secondary"
                                : "ghost"
                            }
                            aria-label="Console"
                            title="Console"
                            onClick={() => setOpen({ server: s.id, view: "console" })}
                          >
                            <SquareTerminal />
                          </Button>
                          {manage && (
                            <Button
                              size="icon-sm"
                              variant={
                                open?.server === s.id && open.view === "files"
                                  ? "secondary"
                                  : "ghost"
                              }
                              aria-label="Files"
                              title="Files"
                              onClick={() => {
                                setWhere({ path: "" });
                                setOpen({ server: s.id, view: "files" });
                              }}
                            >
                              <FolderOpen />
                            </Button>
                          )}
                          {(["start", "restart", "stop"] as const).map((a) => (
                            <Button
                              key={a}
                              size="sm"
                              variant="outline"
                              className="capitalize"
                              disabled={!!busy}
                              onClick={() =>
                                act(`${a}${s.id}`, async () => {
                                  await run(`server.${a}`, {}, s.id);
                                  await health.refetch();
                                })
                              }
                            >
                              {a}
                            </Button>
                          ))}
                        </span>
                      </TableCell>
                    )}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      </div>

      {open && operate && (
        <Card>
          <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
            <div className="space-y-1.5">
              <CardTitle>
                {open.view === "console" ? "Console" : "Files"} ·{" "}
                <span className="font-mono">{open.server.slice(-8)}</span>
              </CardTitle>
              <CardDescription>
                {open.view === "console"
                  ? "Commands you send are logged for the owner, like everything here."
                  : "No uploads or downloads under support access; deletes go to the server's trash."}
              </CardDescription>
            </div>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Close"
              onClick={() => setOpen(undefined)}
            >
              <X />
            </Button>
          </CardHeader>
          <CardContent>
            {open.view === "console" ? (
              <Suspense fallback={<p className="text-sm text-muted-foreground">Loading…</p>}>
                <Console
                  key={open.server}
                  nodeId={r.nodeId}
                  serverId={open.server}
                  canWrite
                  support={requestId}
                />
              </Suspense>
            ) : (
              manage && (
                <Files
                  key={open.server}
                  nodeId={r.nodeId}
                  serverId={open.server}
                  orgId={r.orgId}
                  userId={session.user?.id ?? ""}
                  canWrite
                  path={where.path}
                  file={where.file}
                  onNavigate={setWhere}
                  support={requestId}
                />
              )
            )}
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>Wings' log</CardTitle>
            <CardDescription>
              The last 300 lines of what Wings did, from the node's journal.
            </CardDescription>
          </div>
          <Button
            size="sm"
            variant="outline"
            disabled={!!busy}
            onClick={() =>
              act("logs", async () => {
                const res = await run<{ log: string }>("node.logs", { lines: 300 });
                setLines(res?.log ?? "");
              })
            }
          >
            {busy === "logs" ? <Loader2 className="animate-spin" /> : <RefreshCw />}{" "}
            {lines === undefined ? "Load" : "Reload"}
          </Button>
        </CardHeader>
        <CardContent>
          {lines !== undefined && (
            <pre className="max-h-[32rem] overflow-auto terminal rounded-lg p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
              {lines || "Nothing logged."}
            </pre>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
