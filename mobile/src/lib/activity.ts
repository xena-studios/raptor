import { Platform } from "react-native";

import type { ServerStatus } from "@/lib/status";
import ServerActivity, { type ServerActivityProps } from "@/widgets/server-activity";

// A Live Activity for a power action from the app (iOS): it shows the server
// starting, restarting, or stopping on the lock screen and in the Dynamic
// Island, and the server page updates it until the server settles. The app
// updates it only while it's open; after 5 minutes without one, iOS shows it
// as stale.

type Instance = ReturnType<typeof ServerActivity.start>;

export type Activity = {
  update: (status: ServerStatus) => void;
  end: (status?: ServerStatus) => void;
};

const doing: Record<string, string> = {
  "server.start": "Starting",
  "server.restart": "Restarting",
  "server.stop": "Stopping",
};

// settled is whether a power action is over, by the state it was heading to.
export function settled(action: string, state: string): boolean {
  if (action === "server.stop") return state === "offline" || state === "crashed";
  return state === "running" || state === "crashed" || state === "offline";
}

export function startActivity(
  action: string,
  server: string,
  node: string,
  status: ServerStatus,
): Activity | null {
  if (Platform.OS !== "ios" || !doing[action]) return null;
  let inst: Instance;
  const base = { server, node, doing: doing[action] };
  const props = (s: ServerStatus, done: boolean): ServerActivityProps => ({
    ...base,
    label: s.label,
    tone: s.tone === "stale" ? "stopped" : s.tone,
    done,
  });
  const stale = () => new Date(Date.now() + 5 * 60_000);
  try {
    inst = ServerActivity.start(
      props({ ...status, label: base.doing, tone: "pending" }, false),
      undefined,
      stale(),
    );
  } catch {
    // Live Activities are off for Raptor, or this iPhone can't show them.
    return null;
  }
  return {
    update: (s) => inst.update(props(s, false), stale()),
    end: (s) => {
      if (s) inst.update(props(s, true));
      setTimeout(() => inst.end("default"), 4000);
    },
  };
}
