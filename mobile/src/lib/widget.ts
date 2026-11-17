import { Platform } from "react-native";

import { Role } from "@/gen/raptor/panel/v1/org_pb";
import { serverStatus } from "@/lib/status";
import { orgClient } from "@/lib/transport";
import ServersWidget, { type WidgetServer } from "@/widgets/servers-widget";

// The home screen widget and Siri's list of servers come from here: every
// server the person can see, with its state. The app writes them when it
// starts, when it comes back to the front, and after a power action; a
// widget can't fetch on its own, so it says when it was last written.

export type KnownServer = {
  orgId: string;
  nodeId: string;
  serverId: string;
  name: string;
  node: string;
  canPower: boolean;
};

let known: KnownServer[] = [];
let listeners: ((s: KnownServer[]) => void)[] = [];

// onServers is told the servers each time they're loaded (for Siri).
export function onServers(f: (s: KnownServer[]) => void) {
  listeners.push(f);
  if (known.length) f(known);
  return () => {
    listeners = listeners.filter((l) => l !== f);
  };
}

export function knownServers(): KnownServer[] {
  return known;
}

const order = { failed: 0, pending: 1, stopped: 2, live: 3, stale: 2 } as const;

export async function syncWidget() {
  const { orgs } = await orgClient.listOrgs({});
  const servers: (WidgetServer & { order: number })[] = [];
  const all: KnownServer[] = [];
  for (const org of orgs) {
    const { nodes } = await orgClient.listNodes({ orgId: org.id });
    await Promise.all(
      nodes.map(async (node) => {
        const res = await orgClient
          .listServers({ orgId: org.id, nodeId: node.id })
          .catch(() => null);
        for (const s of res?.servers ?? []) {
          const st = serverStatus(s, node.connected);
          const tone = st.tone === "stale" ? "stopped" : st.tone;
          servers.push({
            name: s.name,
            label: node.connected ? st.label : "Offline",
            tone,
            order: order[st.tone],
          });
          all.push({
            orgId: org.id,
            nodeId: node.id,
            serverId: s.id,
            name: s.name,
            node: node.name,
            canPower:
              org.role >= Role.ADMIN ||
              s.permissions.includes("*") ||
              s.permissions.includes("power"),
          });
        }
      }),
    );
  }
  known = all;
  for (const l of listeners) l(all);
  if (Platform.OS !== "ios") return;
  servers.sort((a, b) => a.order - b.order || a.name.localeCompare(b.name));
  ServersWidget.updateSnapshot({
    running: servers.filter((s) => s.tone === "live").length,
    total: servers.length,
    servers: servers.slice(0, 6).map(({ order: _, ...s }) => s),
    updated: new Date().toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }),
  });
}

// clearWidget empties the widget when the person signs out, so it doesn't
// keep showing their servers.
export function clearWidget() {
  known = [];
  for (const l of listeners) l([]);
  if (Platform.OS !== "ios") return;
  ServersWidget.updateSnapshot({ running: 0, total: 0, servers: [], updated: "", signedOut: true });
}
