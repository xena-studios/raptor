import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Coffee, Loader2 } from "lucide-react";
import { useMemo, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { javaImage, type Problem } from "@/lib/problems";
import { imageParams, type ServerConfig } from "@/lib/servers";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

// JavaFix offers the Java a server needs when its console says it can't
// start on the one it has (#421): one button switches the image, signed
// with the user's passkey since it changes what runs, and starts it.
export function JavaFix({
  problem,
  orgId,
  nodeId,
  serverId,
  userId,
  canChange,
  canStart,
  onDone,
}: {
  problem: Problem;
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  // The startup permission (changing the image), and power.
  canChange: boolean;
  canStart: boolean;
  onDone: () => void;
}) {
  const client = useQueryClient();
  // Only those who change Startup need the egg's images.
  const res = useQuery(
    OrgService.method.getServer,
    { orgId, nodeId, serverId },
    { enabled: canChange },
  );
  const cfg = useMemo(
    () => (res.data?.configJson ? (JSON.parse(res.data.configJson) as ServerConfig) : undefined),
    [res.data?.configJson],
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const want = `Java ${problem.need}${problem.orNewer ? " or newer" : ""}`;
  const image = javaImage(cfg?.egg?.images ?? [], problem.need, problem.orNewer);
  const already = image && cfg?.image === image.ref;

  async function fix() {
    if (!cfg || !image) return;
    setBusy(true);
    setError("");
    try {
      await sendSigned({
        userId,
        nodeId,
        action: "server.update",
        serverId,
        params: imageParams(cfg, res.data?.server?.name ?? "", image.ref),
      });
      if (canStart) await commandClient.execute({ nodeId, action: "server.start", serverId });
      onDone();
      await client.invalidateQueries();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Alert>
      <Coffee />
      <AlertTitle>This server needs {want}</AlertTitle>
      <AlertDescription className="flex flex-col gap-3">
        <p>
          It stopped because it runs on a Java it can't use.{" "}
          {!canChange
            ? `Someone with access to this server's Startup can switch it to ${want}.`
            : !cfg
              ? ""
              : image && !already
                ? `Switch it to ${image.name}${canStart ? " and start it again" : ""}.`
                : already
                  ? `It's already set to ${image.name}: start it again to use it.`
                  : `This server's game doesn't offer ${want}. Pick a runtime in Startup, or change its server type in Settings.`}
        </p>
        {canChange && image && !already && (
          <div className="flex flex-wrap items-center gap-3">
            <Button size="sm" disabled={busy || !cfg} onClick={fix}>
              {busy && <Loader2 className="animate-spin" />}
              Switch to {image.name}
              {canStart ? " and start" : ""}
            </Button>
            <span className="text-xs text-muted-foreground">
              Your passkey signs this, since it changes what the server runs.
            </span>
          </div>
        )}
        {canChange && cfg && !image && (
          <Link
            to="/orgs/$orgId/servers/$serverId/startup"
            params={{ orgId, serverId }}
            className="text-sm underline underline-offset-4"
          >
            Open Startup
          </Link>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </AlertDescription>
    </Alert>
  );
}
