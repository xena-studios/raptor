import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Play, RotateCw, Square } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { message } from "@/lib/errors";
import { commandClient } from "@/lib/transport";

// PowerButtons starts, restarts, and stops a server (the power permission).
// Each is off when it can't do anything: Start while it's running, Stop
// while it's stopped, all of them mid-install or move, or while its node
// is offline. A server stuck starting can still be stopped.
export function PowerButtons({
  nodeId,
  serverId,
  state,
  offline,
  onError,
}: {
  nodeId: string;
  serverId: string;
  state?: string;
  offline?: boolean;
  onError: (message: string) => void;
}) {
  const client = useQueryClient();
  const [busy, setBusy] = useState("");

  async function power(action: string) {
    setBusy(action);
    onError("");
    try {
      await commandClient.execute({ nodeId, action, serverId });
      await client.invalidateQueries();
    } catch (err) {
      onError(message(err));
    } finally {
      setBusy("");
    }
  }

  // What each can act on; anything else (installing, moving, restoring…)
  // leaves them all off until it's done. Unknown states leave them on.
  const known = state !== undefined && state !== "";
  const busyState = known && !["running", "starting", "offline", "crashed"].includes(state);
  const off = offline || !!busy || busyState;
  const buttons = [
    ["server.start", "Start", Play, state === "running" || state === "starting"],
    ["server.restart", "Restart", RotateCw, false],
    ["server.stop", "Stop", Square, state === "offline" || state === "crashed"],
  ] as const;
  return (
    <>
      {buttons.map(([action, label, Icon, pointless]) => (
        <Button
          key={action}
          size="sm"
          variant="outline"
          disabled={off || pointless}
          title={offline ? "The node is offline" : undefined}
          onClick={() => power(action)}
        >
          {busy === action ? <Loader2 className="animate-spin" /> : <Icon />} {label}
        </Button>
      ))}
    </>
  );
}
