import { useQuery } from "@connectrpc/connect-query";

import { Card, CardContent } from "@/components/ui/card";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { when } from "@/lib/format";

export function Log({ orgId }: { orgId: string }) {
  const log = useQuery(OrgService.method.listAuditLog, { orgId });
  return (
    <Card>
      <CardContent className="flex flex-col divide-y text-sm">
        {log.data?.events.map((e) => {
          const meta = JSON.parse(e.metadataJson || "{}") as Record<string, unknown>;
          const what =
            e.action === "command" ? `${meta.action}${meta.error ? " (failed)" : ""}` : e.action;
          return (
            <div key={e.id} className="flex justify-between gap-4 py-2">
              <span>
                <span className="font-medium">{e.actorEmail || "someone who left"}</span> {what}
                {e.target && <span className="text-muted-foreground"> · {e.target}</span>}
              </span>
              <span className="shrink-0 text-xs text-muted-foreground">{when(e.at)}</span>
            </div>
          );
        })}
        {log.data?.events.length === 0 && (
          <p className="py-2 text-muted-foreground">Nothing yet.</p>
        )}
      </CardContent>
    </Card>
  );
}
