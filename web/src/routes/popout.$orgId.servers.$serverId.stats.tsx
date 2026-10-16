import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";

import { ChartSubject } from "@/components/chart-export";
import { ServerGraphs, ServerLastHour, ServerStats } from "@/components/server-stats";
import { RangePicker } from "@/components/stats";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { message } from "@/lib/errors";
import { useIds } from "@/lib/ids";
import { type MetricRange, useMetrics } from "@/lib/metrics";
import { useServer } from "@/lib/org-data";

export const Route = createFileRoute("/popout/$orgId/servers/$serverId/stats")({
  component: StatsPopout,
});

// A server's stats and graphs, to keep beside the game.
function StatsPopout() {
  const { orgId, nodeId, serverId } = useIds();
  const { node, server } = useServer(orgId, nodeId, serverId);
  const [range, setRange] = useState<MetricRange>("1h");
  const live = useMetrics(nodeId, serverId, "1h");
  const history = useMetrics(nodeId, serverId, range);
  if (!server) return null;
  const running = server.state === "running" || server.state === "starting";
  return (
    <div className="flex flex-col gap-4 p-4">
      <ServerStats metrics={live.data} running={running && !!node?.connected} />
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>Resources</CardTitle>
            <CardDescription>
              Sampled every 10 seconds; minutes for a day, then 15 minutes for a week.
            </CardDescription>
          </div>
          <RangePicker value={range} onChange={setRange} />
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
      <ServerLastHour metrics={live.data} />
    </div>
  );
}
