import { useQueryClient } from "@tanstack/react-query";
import { Play, RotateCw, Square } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { message } from "@/lib/errors";
import { commandClient } from "@/lib/transport";

// PowerButtons starts, restarts, and stops a server (the power permission).
export function PowerButtons({
  nodeId,
  serverId,
  onError,
}: {
  nodeId: string;
  serverId: string;
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

  return (
    <>
      <Button size="sm" variant="outline" disabled={!!busy} onClick={() => power("server.start")}>
        <Play /> Start
      </Button>
      <Button size="sm" variant="outline" disabled={!!busy} onClick={() => power("server.restart")}>
        <RotateCw /> Restart
      </Button>
      <Button size="sm" variant="outline" disabled={!!busy} onClick={() => power("server.stop")}>
        <Square /> Stop
      </Button>
    </>
  );
}
