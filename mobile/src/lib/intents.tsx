import * as AppIntents from "expo-app-intents";
import { router } from "expo-router";
import * as SecureStore from "expo-secure-store";
import { useEffect } from "react";
import { Platform } from "react-native";

import { knownServers, onServers } from "@/lib/widget";

// Siri and Shortcuts (iOS): "Restart Survival in Raptor". The intents are
// Swift (mobile/app-intents); they open the app and hand the action here,
// which does it on the server's page, the same way as an alert's button.
// Siri's list of servers is the one the widget is written from.

const handledKey = "raptor.intents.handled";

async function handled(): Promise<string[]> {
  try {
    return JSON.parse((await SecureStore.getItemAsync(handledKey)) ?? "[]") as string[];
  } catch {
    return [];
  }
}

async function markHandled(id: string) {
  const ids = [id, ...(await handled())].slice(0, 30);
  await SecureStore.setItemAsync(handledKey, JSON.stringify(ids)).catch(() => {});
}

// IntentHandler runs the actions Siri and Shortcuts asked for. It's mounted
// only while signed in and unlocked, so they wait for both.
export function IntentHandler() {
  // Publish the servers Siri can name, each time they're loaded.
  useEffect(() => {
    if (Platform.OS !== "ios" || !AppIntents.isAvailable()) return;
    return onServers((servers) => {
      AppIntents.setEntityCatalogAsync(
        "server",
        servers.map((s) => ({
          id: `${s.orgId}|${s.nodeId}|${s.serverId}`,
          title: s.name,
          subtitle: s.node,
        })),
      ).catch(() => {});
    });
  }, []);

  AppIntents.useAppIntents(async (pending) => {
    const done = await handled();
    for (const inv of pending) {
      if (inv.name !== "power") continue;
      // Delivered at least once: an action already done isn't done again.
      if (done.includes(inv.id)) {
        await AppIntents.removePendingInvocationAsync(inv.id).catch(() => {});
        continue;
      }
      const ref = typeof inv.params.server === "string" ? inv.params.server : "";
      const action = typeof inv.params.action === "string" ? inv.params.action : "";
      const [orgId, nodeId, serverId] = ref.split("|");
      const known =
        knownServers().length === 0 || knownServers().some((s) => s.serverId === serverId);
      if (orgId && nodeId && serverId && ["start", "restart", "stop"].includes(action) && known) {
        await markHandled(inv.id);
        router.push({
          pathname: "/server/[orgId]/[nodeId]/[serverId]",
          params: { orgId, nodeId, serverId, do: action },
        });
      }
      // Anything else (a server that's gone, an old shortcut) is dropped.
      await AppIntents.removePendingInvocationAsync(inv.id).catch(() => {});
    }
  });
  return null;
}
