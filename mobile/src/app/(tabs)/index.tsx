import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { router, useFocusEffect } from "expo-router";
import { BellOff } from "lucide-react-native";
import { useCallback, useRef, useState } from "react";
import { Pressable, View } from "react-native";

import { Page, PageHeader } from "@/components/screen";
import { Card, Dot, Empty, Notice, Row, Skeleton, Text } from "@/components/ui";
import { AlertService, type Notification } from "@/gen/raptor/panel/v1/alert_pb";
import { ago } from "@/lib/format";
import { type Colors, useColors } from "@/lib/theme";
import { alertClient, message } from "@/lib/transport";

// The inbox: every alert sent to the person, from every org they're in,
// as the bell has it in the web app (web/src/components/alerts-bell.tsx).
// Opening it marks them seen; what came since the last look is tinted.

function levelColor(c: Colors, n: Notification): string {
  if (n.resolved) return c.ok;
  return { critical: c.destructive, warning: c.warn }[n.level] ?? c.mutedForeground;
}

export default function Inbox() {
  const c = useColors();
  const client = useQueryClient();
  const list = useQuery(AlertService.method.listNotifications, { limit: 100 });
  const [error, setError] = useState("");
  const [clearing, setClearing] = useState(false);
  // What was new when the inbox was opened stays tinted while it's open.
  const fresh = useRef(new Set<string>());
  const items = list.data?.notifications ?? [];
  for (const n of items) if (!n.seen) fresh.current.add(n.id);

  useFocusEffect(
    useCallback(() => {
      alertClient
        .seeNotifications({})
        .then(() => client.invalidateQueries())
        .catch(() => {});
      return () => fresh.current.clear();
    }, [client]),
  );

  async function clear() {
    setError("");
    setClearing(true);
    try {
      await alertClient.dismissNotifications({ ids: [] });
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setClearing(false);
    }
  }

  function open(n: Notification) {
    if (n.serverId && n.nodeId && n.orgId) {
      router.push({
        pathname: "/server/[orgId]/[nodeId]/[serverId]",
        params: { orgId: n.orgId, nodeId: n.nodeId, serverId: n.serverId },
      });
    }
  }

  return (
    <Page
      refreshing={list.isRefetching}
      onRefresh={() => void list.refetch()}
      header={
        <PageHeader
          title="Alerts"
          description="What your orgs' alert rules send you, newest first."
          action={
            items.length > 0 ? (
              <Pressable
                accessibilityRole="button"
                disabled={clearing}
                onPress={clear}
                hitSlop={12}
                style={({ pressed }) => ({
                  opacity: pressed || clearing ? 0.5 : 1,
                  paddingBottom: 4,
                })}
              >
                <Text variant="button" ink="muted" style={{ textTransform: "uppercase" }}>
                  Clear all
                </Text>
              </Pressable>
            ) : null
          }
        />
      }
    >
      <Notice text={error || (list.error ? message(list.error) : "")} />
      {list.isPending ? (
        <Card>
          {[0, 1, 2].map((i) => (
            <Row key={i} first={i === 0}>
              <Skeleton width="60%" />
              <Skeleton width="40%" height={11} />
            </Row>
          ))}
        </Card>
      ) : items.length === 0 ? (
        <Empty
          icon={BellOff}
          title="Nothing new"
          text="When a server goes down, a node goes offline, or a disk fills up, it shows up here and on this phone."
        />
      ) : (
        <Card>
          {items.map((n, i) => {
            const where = [[n.serverName, n.nodeName].filter(Boolean).join(" on "), n.orgName]
              .filter(Boolean)
              .join(" · ");
            const tappable = !!(n.serverId && n.nodeId && n.orgId);
            return (
              <View
                key={n.id}
                style={{ backgroundColor: fresh.current.has(n.id) ? c.brandWash : "transparent" }}
              >
                <Row
                  first={i === 0}
                  onPress={tappable ? () => open(n) : undefined}
                  accessibilityLabel={n.title}
                >
                  <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                    <Dot color={levelColor(c, n)} />
                    <Text weight="medium" numberOfLines={1} style={{ flex: 1 }}>
                      {n.title}
                    </Text>
                    <Text variant="label" mono ink="muted">
                      {ago(n.at)}
                    </Text>
                  </View>
                  {where ? (
                    <Text variant="meta" ink="muted" numberOfLines={1} style={{ paddingLeft: 16 }}>
                      {where}
                    </Text>
                  ) : null}
                  {n.text ? (
                    <Text variant="meta" ink="muted" numberOfLines={2} style={{ paddingLeft: 16 }}>
                      {n.text}
                    </Text>
                  ) : null}
                </Row>
              </View>
            );
          })}
        </Card>
      )}
    </Page>
  );
}
