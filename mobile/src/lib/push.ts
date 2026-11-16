import * as Application from "expo-application";
import * as Notifications from "expo-notifications";
import { router } from "expo-router";
import { Platform } from "react-native";

import { alertClient } from "@/lib/transport";

// Alerts as push notifications (docs/PANEL.md#alerts): the app gives the
// Panel this phone's push token from Apple or Google, which makes the phone
// one of the person's own alert channels. The Panel sends to Apple and
// Google directly. Critical alerts are time-sensitive on iOS and go to the
// "urgent" channel on Android.

// Shown while the app is open too.
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldShowBanner: true,
    shouldShowList: true,
    shouldPlaySound: true,
    shouldSetBadge: false,
  }),
});

// Android's channels must exist before asking for a token (Android 13+).
async function channels() {
  if (Platform.OS !== "android") return;
  await Notifications.setNotificationChannelAsync("alerts", {
    name: "Alerts",
    importance: Notifications.AndroidImportance.DEFAULT,
  });
  await Notifications.setNotificationChannelAsync("urgent", {
    name: "Urgent alerts",
    description: "A server down, a node offline, a disk running out",
    importance: Notifications.AndroidImportance.HIGH,
  });
}

export type PushStatus = "on" | "denied" | "unavailable";

async function sandbox(): Promise<boolean> {
  if (Platform.OS !== "ios") return false;
  try {
    return (await Application.getIosPushNotificationServiceEnvironmentAsync()) === "development";
  } catch {
    return __DEV__;
  }
}

async function send(token: Notifications.DevicePushToken) {
  if (token.type !== "ios" && token.type !== "android") return;
  await alertClient.registerPushDevice({
    platform: token.type,
    token: String(token.data),
    sandbox: await sandbox(),
  });
}

// register asks for permission (the first time) and gives the Panel this
// phone's token. ask: false only registers if permission was given before.
export async function register(ask: boolean): Promise<PushStatus> {
  await channels();
  let perm = await Notifications.getPermissionsAsync();
  if (!perm.granted && ask && perm.canAskAgain) {
    perm = await Notifications.requestPermissionsAsync({
      ios: { allowAlert: true, allowBadge: false, allowSound: true },
    });
  }
  if (!perm.granted) return "denied";
  let token: Notifications.DevicePushToken;
  try {
    token = await Notifications.getDevicePushTokenAsync();
  } catch {
    // A simulator without push, or no Google Play services.
    return "unavailable";
  }
  await send(token);
  return "on";
}

// unregister stops pushes to this phone; it stays signed in.
export async function unregister() {
  await alertClient.unregisterPushDevice({});
}

// open goes to what a tapped notification was about.
function open(data: Record<string, unknown> | undefined) {
  const s = (k: string) => (typeof data?.[k] === "string" ? (data[k] as string) : "");
  if (s("kind") === "approval") {
    router.push("/approve");
    return;
  }
  if (s("org_id") && s("node_id") && s("server_id")) {
    router.push({
      pathname: "/server/[orgId]/[nodeId]/[serverId]",
      params: { orgId: s("org_id"), nodeId: s("node_id"), serverId: s("server_id") },
    });
    return;
  }
  router.navigate("/");
}

// listen keeps the token current and handles taps, while signed in. It
// returns what stops it.
export function listen(): () => void {
  const subs = [
    Notifications.addPushTokenListener((t) => {
      send(t).catch(() => {});
    }),
    Notifications.addNotificationResponseReceivedListener((r) => {
      if (r.actionIdentifier === Notifications.DEFAULT_ACTION_IDENTIFIER) {
        open(r.notification.request.content.data as Record<string, unknown>);
      }
    }),
  ];
  // A tap that opened the app from closed.
  const last = Notifications.getLastNotificationResponse();
  if (last && last.actionIdentifier === Notifications.DEFAULT_ACTION_IDENTIFIER) {
    open(last.notification.request.content.data as Record<string, unknown>);
    Notifications.clearLastNotificationResponse();
  }
  return () => {
    for (const s of subs) s.remove();
  };
}
