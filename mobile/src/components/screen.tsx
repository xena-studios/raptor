import type { ReactNode } from "react";
import { RefreshControl, ScrollView, View } from "react-native";
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
// with pull to refresh. inset adds the status bar's height (tabs have no
// navigation bar above them).
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
  return (
    <ScrollView
      style={{ flex: 1, backgroundColor: c.background }}
      contentInsetAdjustmentBehavior="automatic"
      contentContainerStyle={{
        paddingTop: (inset ? insets.top : 0) + 16,
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
      {header}
      {children}
    </ScrollView>
  );
}
