// A server's state in words, as the web app has it (web/src/lib/servers.ts
// serverStatus): the label is always spelled out, and the tone is the only
// place state gets colour.

export type Tone = "live" | "pending" | "failed" | "stopped" | "stale";

export type ServerStatus = { label: string; tone: Tone; detail?: string };

export function serverStatus(
  s: { state: string; installState: string; installError: string },
  nodeConnected: boolean,
): ServerStatus {
  let st: ServerStatus;
  if (s.installState === "failed" || s.state === "install_failed") {
    st = { label: "Install failed", tone: "failed", detail: s.installError || undefined };
  } else if (
    s.state === "installing" ||
    s.installState === "pending" ||
    s.installState === "installing"
  ) {
    st = { label: "Installing", tone: "pending" };
  } else {
    st = (
      {
        running: { label: "Running", tone: "live" },
        starting: { label: "Starting", tone: "pending" },
        stopping: { label: "Stopping", tone: "pending" },
        crashed: { label: "Crashed", tone: "failed" },
        offline: { label: "Stopped", tone: "stopped" },
        restoring: { label: "Restoring", tone: "pending" },
        deleting: { label: "Deleting", tone: "pending" },
        moving: { label: "Moving", tone: "pending" },
      } as Record<string, ServerStatus>
    )[s.state] ?? { label: s.state || "Unknown", tone: "stopped" };
  }
  if (!nodeConnected) {
    return {
      label: st.label,
      tone: "stale",
      detail: "The node is offline: this is its last report.",
    };
  }
  return st;
}

// nodeStatus is a node's connection as a status, as the web's.
export function nodeStatus(n: { connected: boolean }): ServerStatus {
  return n.connected ? { label: "Connected", tone: "live" } : { label: "Offline", tone: "stopped" };
}
