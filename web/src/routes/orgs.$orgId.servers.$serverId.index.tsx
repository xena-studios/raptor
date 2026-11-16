import { createFileRoute } from "@tanstack/react-router";
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";

import { ServerAddress } from "@/components/address";
import { ChartSubject } from "@/components/chart-export";
import { JavaFix } from "@/components/java-fix";
import { DetailList } from "@/components/page";
import { PopoutButton } from "@/components/popout";
import { ServerGraphs, ServerLastHour, ServerStats } from "@/components/server-stats";
import { RangePicker } from "@/components/stats";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { WebDashboard } from "@/components/web-dashboard";
import { nodeHostname } from "@/lib/addresses";
import { message } from "@/lib/errors";
import { useIds } from "@/lib/ids";
import { type MetricRange, useMetrics } from "@/lib/metrics";
import { can, useServer } from "@/lib/org-data";
import { findProblem, type Problem, problemWindow } from "@/lib/problems";

const Console = lazy(() => import("@/components/console"));

export const Route = createFileRoute("/orgs/$orgId/servers/$serverId/")({
  component: Overview,
});

// The server's overview: how it's doing now, its console, where players
// reach it, and its resources over time.
function Overview() {
  const session = Route.useRouteContext();
  const { orgId, nodeId, serverId } = useIds();
  const { node, server } = useServer(orgId, nodeId, serverId);
  const [range, setRange] = useState<MetricRange>("1h");
  const live = useMetrics(nodeId, serverId, "1h");
  const history = useMetrics(nodeId, serverId, range);
  // A problem the console shows that the app can fix (lib/problems), from
  // its newest lines.
  const tail = useRef<string[]>([]);
  const [problem, setProblem] = useState<Problem>();
  const onOutput = useCallback((lines: string[], reset: boolean) => {
    tail.current = (reset ? lines : [...tail.current, ...lines]).slice(-problemWindow);
    setProblem(findProblem(tail.current));
  }, []);
  const state = server?.state;
  // Starting again clears it; if it's still wrong, the console says so again.
  useEffect(() => {
    if (state === "starting" || state === "running") {
      tail.current = [];
      setProblem(undefined);
    }
  }, [state]);
  if (!server) return null;
  const running = server.state === "running" || server.state === "starting";

  return (
    <div className="flex flex-col gap-6">
      {problem?.kind === "java" && !running && (
        <JavaFix
          problem={problem}
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          userId={session.user?.id ?? ""}
          canChange={can(server, "startup")}
          canStart={can(server, "power")}
          onDone={() => setProblem(undefined)}
        />
      )}
      <ServerStats metrics={live.data} running={running && !!node?.connected} />

      <div className="grid items-start gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
            <div className="min-w-0 flex-1 basis-xs space-y-1.5">
              <CardTitle>Console</CardTitle>
              <CardDescription>Live output from your server.</CardDescription>
            </div>
            {(can(server, "console.read") || can(server, "console.write")) && (
              <PopoutButton to={{ kind: "console", orgId, nodeId, serverId }} label="the console" />
            )}
          </CardHeader>
          <CardContent>
            {can(server, "console.read") || can(server, "console.write") ? (
              <Suspense
                fallback={<p className="text-sm text-muted-foreground">Loading the console…</p>}
              >
                <Console
                  nodeId={nodeId}
                  serverId={serverId}
                  canWrite={can(server, "console.write")}
                  onOutput={onOutput}
                  orgId={orgId}
                />
              </Suspense>
            ) : (
              <p className="text-sm text-muted-foreground">
                You don't have access to this server's console.
              </p>
            )}
          </CardContent>
        </Card>
        <div className="flex flex-col gap-6">
          <Card>
            <CardHeader>
              <CardTitle>Details</CardTitle>
              <CardDescription>Where players reach this server.</CardDescription>
            </CardHeader>
            <CardContent>
              <DetailList
                rows={[
                  [
                    "Connect",
                    node ? (
                      <ServerAddress key="c" node={node} server={server} port={server.ports[0]} />
                    ) : (
                      "—"
                    ),
                  ],
                  ["Game", server.eggName || "—"],
                  ["Node", node?.name ?? "—"],
                ]}
              />
              <WebDashboard
                orgId={orgId}
                nodeId={nodeId}
                serverId={serverId}
                host={server.displayAddress || (node ? nodeHostname(node) : "")}
              />
            </CardContent>
          </Card>
          <ServerLastHour metrics={live.data} />
        </div>
      </div>

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 flex-1 basis-xs space-y-1.5">
            <CardTitle>Resources</CardTitle>
            <CardDescription>
              Sampled every 10 seconds; minutes for a day, then 15 minutes for a week.
            </CardDescription>
          </div>
          <div className="flex items-center gap-1">
            <RangePicker value={range} onChange={setRange} />
            <PopoutButton to={{ kind: "stats", orgId, nodeId, serverId }} label="the stats" />
          </div>
        </CardHeader>
        <CardContent>
          {history.error ? (
            <p className="text-sm text-destructive">{message(history.error)}</p>
          ) : (
            <ChartSubject value={node ? `${server.name} on ${node.name}` : server.name}>
              <ServerGraphs metrics={history.data} range={range} />
            </ChartSubject>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
