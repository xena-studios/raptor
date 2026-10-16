import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";

import { ChartSubject } from "@/components/chart-export";
import { NodeGraphs, NodeStats } from "@/components/node-stats";
import { RangePicker } from "@/components/stats";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { isAdmin } from "@/lib/format";
import { useIds } from "@/lib/ids";
import { type MetricRange, useNodeMetrics } from "@/lib/metrics";
import { useOrgNodes } from "@/lib/org-data";

export const Route = createFileRoute("/popout/$orgId/nodes/$nodeId/stats")({
  component: NodeStatsPopout,
});

// The whole machine's stats and graphs (admins, as on its overview).
function NodeStatsPopout() {
  const { orgId, nodeId } = useIds();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const admin = isAdmin(orgs.data?.orgs.find((o) => o.id === orgId)?.role);
  const node = useOrgNodes(orgId).data?.nodes.find((n) => n.id === nodeId);
  const [range, setRange] = useState<MetricRange>("1h");
  const on = admin && !!node?.connected;
  const live = useNodeMetrics(nodeId, "1h", on);
  const history = useNodeMetrics(nodeId, range, on);
  if (!orgs.data || !node) return null;
  if (!admin) {
    return (
      <p className="p-4 text-sm text-muted-foreground">
        Only the org's admins and owners see the whole machine's stats.
      </p>
    );
  }
  if (!node.connected) {
    return (
      <p className="p-4 text-sm text-muted-foreground">
        {node.name} is offline; its stats come back when it reconnects.
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-4 p-4">
      <NodeStats m={live.data} />
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="space-y-1.5">
            <CardTitle>Resources</CardTitle>
            <CardDescription>The whole machine: Raptor, its servers, and the rest.</CardDescription>
          </div>
          <RangePicker value={range} onChange={setRange} />
        </CardHeader>
        <CardContent>
          {history.error ? (
            <p className="text-sm text-destructive">{message(history.error)}</p>
          ) : (
            <ChartSubject value={node.name}>
              <NodeGraphs m={history.data} range={range} />
            </ChartSubject>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
