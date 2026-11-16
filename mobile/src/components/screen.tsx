import type { ReactNode } from "react";
import { Platform, RefreshControl, ScrollView, View } from "react-native";
import { LayoutAnimationConfig } from "react-native-reanimated";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { Kicker, Text } from "@/components/ui";
import { space, useColors } from "@/lib/theme";

// PageHeader is a page's title, as the web app's: a // kicker naming the
// context (once a page), the title, a sentence under it, and an action on
// the right.
export function PageHeader({
  title,
  kicker,
  description,
  action,
}: {
  title: string;
  kicker?: string;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <View style={{ flexDirection: "row", alignItems: "flex-end", gap: 12 }}>
      <View style={{ flex: 1, gap: 4 }}>
        {kicker ? <Kicker>{kicker}</Kicker> : null}
        <Text variant="headline" accessibilityRole="header">
          {title}
        </Text>
        {description ? (
          <Text variant="meta" ink="muted">
            {description}
          </Text>
        ) : null}
      </View>
      {action}
    </View>
  );
}

// Page is a tab's scrolling page: its header, then cards a stack apart,
// with pull to refresh. On iOS the system keeps it clear of the status bar
// and any navigation bar (a native tab's content already starts below the
// status bar, so adding its height here doubled it). On Android, edge to
// edge, inset adds the status bar's height (tabs have no bar above them).
export function Page({
  header,
  children,
  refreshing,
  onRefresh,
  inset = true,
}: {
  header?: ReactNode;
  children: ReactNode;
  refreshing?: boolean;
  onRefresh?: () => void;
  inset?: boolean;
}) {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const top = Platform.OS === "android" && inset ? insets.top : 0;
  return (
    <ScrollView
      style={{ flex: 1, backgroundColor: c.background }}
      contentInsetAdjustmentBehavior="automatic"
      contentContainerStyle={{
        paddingTop: top + 16,
        paddingHorizontal: space.page,
        paddingBottom: 40,
        gap: space.stack,
      }}
      refreshControl={
        onRefresh ? (
          <RefreshControl
            refreshing={!!refreshing}
            onRefresh={onRefresh}
            tintColor={c.mutedForeground}
          />
        ) : undefined
      }
    >
      {/* What's there when the page opens is simply there; only what
          arrives later (data replacing a placeholder) fades in. */}
      <LayoutAnimationConfig skipEntering>
        {header}
        {children}
      </LayoutAnimationConfig>
    </ScrollView>
  );
}
