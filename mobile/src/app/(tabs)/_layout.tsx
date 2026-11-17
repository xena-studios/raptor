import { useQuery } from "@connectrpc/connect-query";
import { NativeTabs } from "expo-router/native-tabs";

import { AlertService } from "@/gen/raptor/panel/v1/alert_pb";
import { useColors } from "@/lib/theme";

export default function TabsLayout() {
  const c = useColors();
  // The inbox's count of what's new, like the bell in the web app.
  const inbox = useQuery(
    AlertService.method.listNotifications,
    { limit: 1 },
    { refetchInterval: 60_000 },
  );
  const unseen = inbox.data?.unseen ?? 0;
  return (
    <NativeTabs tintColor={c.primary}>
      <NativeTabs.Trigger name="index">
        <NativeTabs.Trigger.Icon
          sf={{ default: "bell", selected: "bell.fill" }}
          md="notifications"
        />
        <NativeTabs.Trigger.Label>Alerts</NativeTabs.Trigger.Label>
        {unseen > 0 ? (
          <NativeTabs.Trigger.Badge>
            {unseen > 99 ? "99+" : String(unseen)}
          </NativeTabs.Trigger.Badge>
        ) : null}
      </NativeTabs.Trigger>
      <NativeTabs.Trigger name="servers">
        <NativeTabs.Trigger.Icon
          sf={{ default: "server.rack", selected: "server.rack" }}
          md="dns"
        />
        <NativeTabs.Trigger.Label>Servers</NativeTabs.Trigger.Label>
      </NativeTabs.Trigger>
      <NativeTabs.Trigger name="settings">
        <NativeTabs.Trigger.Icon
          sf={{ default: "gearshape", selected: "gearshape.fill" }}
          md="settings"
        />
        <NativeTabs.Trigger.Label>Settings</NativeTabs.Trigger.Label>
      </NativeTabs.Trigger>
    </NativeTabs>
  );
}
