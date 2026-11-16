import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { router } from "expo-router";
import { Server } from "lucide-react-native";
import { useState } from "react";
import { View } from "react-native";

import { Page, PageHeader } from "@/components/screen";
import { Card, Empty, Notice, Row, Skeleton, StatusChip, Text } from "@/components/ui";
import { type Node, type Org, OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { ago } from "@/lib/format";
import { nodeStatus, serverStatus } from "@/lib/status";
import { message } from "@/lib/transport";

// Every server the person can see, a card per node, with its state as the
// web app shows it.
export default function Servers() {
  const client = useQueryClient();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const [refreshing, setRefreshing] = useState(false);
  const list = orgs.data?.orgs ?? [];

  async function refresh() {
    setRefreshing(true);
    await client.invalidateQueries().catch(() => {});
    setRefreshing(false);
  }

  return (
    <Page
      refreshing={refreshing}
      onRefresh={refresh}
      header={
        <PageHeader
          title="Servers"
          kicker={list.length === 1 ? list[0].name : undefined}
          description="Tap one to start, stop, or back it up."
        />
      }
    >
      <Notice text={orgs.error ? message(orgs.error) : ""} />
      {orgs.isPending ? <LoadingCard /> : null}
      {!orgs.isPending && !orgs.error && list.length === 0 ? (
        <Empty
          icon={Server}
          title="No servers yet"
          text="Make an org and connect a node in the web app. Its servers show up here."
        />
      ) : null}
      {list.map((o) => (
        <OrgSection key={o.id} org={o} named={list.length > 1} />
      ))}
    </Page>
  );
}

function LoadingCard() {
  return (
    <Card>
      {[0, 1].map((i) => (
        <Row key={i} first={i === 0}>
          <Skeleton width="50%" />
          <Skeleton width="30%" height={11} />
        </Row>
      ))}
    </Card>
  );
}

function OrgSection({ org, named }: { org: Org; named: boolean }) {
  const nodes = useQuery(OrgService.method.listNodes, { orgId: org.id });
  return (
    <View style={{ gap: 12 }}>
      {named ? (
        <Text variant="title" ink="muted">
          {org.name}
        </Text>
      ) : null}
      {org.suspended ? (
        <Notice text="This org is suspended, so it can't be managed for now. Its servers keep running." />
      ) : null}
      <Notice text={nodes.error ? message(nodes.error) : ""} />
      {nodes.isPending ? <LoadingCard /> : null}
      {nodes.data && nodes.data.nodes.length === 0 ? (
        <Empty
          title="No nodes yet"
          text="Connect one from the web app: Nodes, then Connect node."
        />
      ) : null}
      {(nodes.data?.nodes ?? []).map((n) => (
        <NodeCard key={n.id} orgId={org.id} node={n} />
      ))}
    </View>
  );
}

function NodeCard({ orgId, node }: { orgId: string; node: Node }) {
  const servers = useQuery(OrgService.method.listServers, { orgId, nodeId: node.id });
  const list = servers.data?.servers ?? [];
  return (
    <Card
      title={node.name}
      description={
        node.connected ? `${node.shortId}.raptornodes.net` : `Last seen ${ago(node.lastSeenAt)}`
      }
      right={<StatusChip status={nodeStatus(node)} />}
    >
      <Notice text={servers.error ? message(servers.error) : ""} />
      {servers.isPending ? (
        <Row>
          <Skeleton width="45%" />
        </Row>
      ) : null}
      {list.map((s) => (
        <Row
          key={s.id}
          accessibilityLabel={s.name}
          onPress={() =>
            router.push({
              pathname: "/server/[orgId]/[nodeId]/[serverId]",
              params: { orgId, nodeId: node.id, serverId: s.id },
            })
          }
        >
          <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
            <Text weight="medium" numberOfLines={1} style={{ flex: 1 }}>
              {s.name}
            </Text>
            <StatusChip status={serverStatus(s, node.connected)} />
          </View>
          {s.eggName ? (
            <Text variant="meta" ink="muted" numberOfLines={1}>
              {s.eggName}
            </Text>
          ) : null}
        </Row>
      ))}
      {servers.data && list.length === 0 ? (
        <Row>
          <Text variant="meta" ink="muted">
            No servers you can see on this node.
          </Text>
        </Row>
      ) : null}
    </Card>
  );
}
