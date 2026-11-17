import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { router, Stack, useLocalSearchParams } from "expo-router";
import * as WebBrowser from "expo-web-browser";
import {
  Archive,
  Bell,
  BellOff,
  ExternalLink,
  Play,
  RotateCw,
  Square,
  Terminal,
} from "lucide-react-native";
import { useEffect, useMemo, useRef, useState } from "react";
import { View } from "react-native";

import { type Readout, Readouts } from "@/components/readouts";
import { Page, PageHeader } from "@/components/screen";
import { Button, Card, CardBody, Notice, Row, Skeleton, StatusChip, Text } from "@/components/ui";
import { AlertService } from "@/gen/raptor/panel/v1/alert_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { type Activity, settled as actionSettled, startActivity } from "@/lib/activity";
import { appURL } from "@/lib/config";
import { can } from "@/lib/format";
import { useLock } from "@/lib/lock";
import { formatBytes, formatPercent, type Metrics, useServerMetrics } from "@/lib/metrics";
import { haptic } from "@/lib/motion";
import { MUTE_MINUTES } from "@/lib/push";
import { serverStatus } from "@/lib/status";
import { useColors } from "@/lib/theme";
import { alertClient, commandClient, message } from "@/lib/transport";
import { syncWidget } from "@/lib/widget";

// Now is what the server uses now, with the last hour behind CPU.
function Now({ metrics, running }: { metrics?: Metrics; running: boolean }) {
  const p = metrics?.latest;
  const l = metrics?.limits;
  const live = running && p?.running;
  const recent = (metrics?.points ?? []).slice(-60).map((x) => (x.running ? x.cpu : null));
  const items: Readout[] = [
    {
      label: "CPU",
      value: live ? formatPercent(p.cpu) : "—",
      sub: l?.cpu_percent ? `of ${l.cpu_percent}% limit` : "100% is one core",
      spark: recent,
    },
    {
      label: "Memory",
      value: live ? formatBytes(p.memory) : "—",
      sub: l?.memory_bytes ? `of ${formatBytes(l.memory_bytes)}` : undefined,
    },
    {
      label: "Disk",
      value: p?.disk ? formatBytes(p.disk) : "—",
      sub: l?.disk_bytes ? `of ${formatBytes(l.disk_bytes)}` : "No limit",
    },
    {
      label: "Players",
      value: live && p.players !== undefined ? String(Math.round(p.players)) : "—",
      sub: live && p.players === undefined ? "This game doesn't report them" : "Online now",
    },
  ];
  return <Readouts items={items} />;
}

// A server: what it uses now, its state and address, power, a backup now,
// and its console. Files, settings, and anything that needs a passkey are
// in the web app, which this links to.
export default function ServerScreen() {
  const c = useColors();
  const client = useQueryClient();
  const {
    orgId,
    nodeId,
    serverId,
    do: requested,
  } = useLocalSearchParams<{
    orgId: string;
    nodeId: string;
    serverId: string;
    // A notification's button or Siri: "restart", "start", "stop", or "mute".
    do?: string;
  }>();
  const lock = useLock();
  const mutes = useQuery(AlertService.method.listAlertMutes, {});
  const muted = mutes.data?.mutes.find((m) => m.serverId === serverId);
  const server = useQuery(
    OrgService.method.getServer,
    { orgId, nodeId, serverId },
    { refetchInterval: 5_000 },
  );
  const nodes = useQuery(OrgService.method.listNodes, { orgId });
  const [busy, setBusy] = useState("");
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  const s = server.data?.server;
  const node = nodes.data?.nodes.find((n) => n.id === nodeId);
  const connected = node?.connected ?? true;
  const metrics = useServerMetrics(nodeId, serverId, !!s && connected);
  const status = useMemo(() => (s ? serverStatus(s, connected) : null), [s, connected]);
  // As the web app's power buttons: off while the server is busy (an
  // install, a move, a restore) or its node is offline; a server stuck
  // starting can still be stopped.
  const settled = !s || ["running", "starting", "offline", "crashed"].includes(s.state) || !s.state;
  const off = !connected || !!busy || !settled;
  const address =
    s && node
      ? s.displayAddress ||
        `${node.shortId}.raptornodes.net${s.ports.length ? `:${s.ports[0]}` : ""}`
      : "";

  // The Live Activity for the power action under way, if any.
  const activity = useRef<{ action: string; a: Activity; at: number } | null>(null);

  async function run(action: string, done: string, params?: object) {
    setBusy(action);
    setResult(null);
    try {
      await commandClient.execute({
        nodeId,
        serverId,
        action,
        paramsJson: params ? JSON.stringify(params) : "",
      });
      haptic.success();
      setResult({ ok: true, text: done });
      if (s && status) {
        activity.current?.a.end();
        const a = startActivity(action, s.name, node?.name ?? "", status);
        activity.current = a ? { action, a, at: Date.now() } : null;
      }
      await client.invalidateQueries();
      setTimeout(() => syncWidget().catch(() => {}), 3000);
    } catch (err) {
      haptic.error();
      setResult({ ok: false, text: message(err) });
    } finally {
      setBusy("");
    }
  }

  // Follow the Live Activity until the server settles (a few seconds in,
  // so the state before the action doesn't count).
  useEffect(() => {
    const cur = activity.current;
    if (!cur || !s || !status) return;
    if (Date.now() - cur.at > 3000 && actionSettled(cur.action, s.state)) {
      cur.a.end(status);
      activity.current = null;
    } else {
      cur.a.update(status);
    }
  }, [s, status]);
  useEffect(() => () => activity.current?.a.end(), []);

  async function mute(on: boolean) {
    setBusy("mute");
    setResult(null);
    try {
      if (on) {
        await alertClient.muteAlerts({ orgId, serverId, minutes: MUTE_MINUTES });
        setResult({ ok: true, text: "Muted for an hour. Its alerts still show in your inbox." });
      } else {
        await alertClient.unmuteAlerts({ orgId, serverId });
        setResult({ ok: true, text: "Unmuted." });
      }
      await mutes.refetch();
    } catch (err) {
      setResult({ ok: false, text: message(err) });
    } finally {
      setBusy("");
    }
  }

  // A notification's Restart or Mute: done once the server's loaded and
  // the app is unlocked, so the person sees it happen.
  const did = useRef(false);
  useEffect(() => {
    if (did.current || !s || lock.locked || !requested) return;
    did.current = true;
    const power: Record<string, [string, string]> = {
      restart: ["server.restart", "Restarting."],
      start: ["server.start", "Starting."],
      stop: ["server.stop", "Stopping."],
    };
    if (power[requested]) {
      if (can(s.permissions, "power")) run(...power[requested]);
      else
        setResult({ ok: false, text: "You don't have permission to start or stop this server." });
    } else if (requested === "mute") {
      mute(true);
    }
  });

  const web = (path = "") =>
    WebBrowser.openBrowserAsync(`${appURL}/orgs/${orgId}/servers/${nodeId}/${serverId}${path}`);

  return (
    <Page
      inset={false}
      refreshing={server.isRefetching && !server.isFetchedAfterMount}
      onRefresh={() => void server.refetch()}
      header={
        s ? (
          <View style={{ gap: 10 }}>
            <PageHeader title={s.name} kicker={node?.name} description={s.eggName || undefined} />
            {status ? <StatusChip status={status} /> : null}
            {status?.detail ? (
              <Text variant="meta" ink={status.tone === "failed" ? "destructive" : "muted"}>
                {status.detail}
              </Text>
            ) : null}
          </View>
        ) : (
          <View style={{ gap: 8 }}>
            <Skeleton width="30%" height={11} />
            <Skeleton width="60%" height={26} />
          </View>
        )
      }
    >
      <Stack.Screen options={{ title: "" }} />
      <Notice text={server.error ? message(server.error) : ""} />

      {s ? <Now metrics={metrics.data} running={s.state === "running" && connected} /> : null}

      {s && address ? (
        <Card>
          <Row first>
            <Text variant="label" mono ink="muted" style={{ textTransform: "uppercase" }}>
              Address
            </Text>
            <Text mono selectable>
              {address}
            </Text>
          </Row>
          {can(s.permissions, "console.read") ? (
            <Row
              accessibilityLabel="Open the console"
              onPress={() =>
                router.push({
                  pathname: "/console/[orgId]/[nodeId]/[serverId]",
                  params: { orgId, nodeId, serverId },
                })
              }
            >
              <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
                <Terminal size={16} color={c.mutedForeground} />
                <Text>Console</Text>
              </View>
            </Row>
          ) : null}
        </Card>
      ) : null}

      {s && can(s.permissions, "power") ? (
        <Card title="Power" description="Players are disconnected while it stops or restarts.">
          <CardBody>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Button
                label="Start"
                variant="outline"
                icon={Play}
                style={{ flex: 1 }}
                busy={busy === "server.start"}
                disabled={off || s.state === "running" || s.state === "starting"}
                onPress={() => run("server.start", "Starting.")}
              />
              <Button
                label="Restart"
                variant="outline"
                icon={RotateCw}
                style={{ flex: 1 }}
                busy={busy === "server.restart"}
                disabled={off}
                onPress={() => run("server.restart", "Restarting.")}
              />
              <Button
                label="Stop"
                variant="outline"
                icon={Square}
                style={{ flex: 1 }}
                busy={busy === "server.stop"}
                disabled={off || s.state === "offline" || s.state === "crashed"}
                onPress={() => run("server.stop", "Stopping.")}
              />
            </View>
            {!connected ? (
              <Text variant="meta" ink="muted">
                The node is offline, so the server can't be reached.
              </Text>
            ) : null}
          </CardBody>
        </Card>
      ) : null}

      {s ? (
        <Card
          title="Notifications"
          description={
            muted?.until
              ? `Muted until ${timestampDate(muted.until).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}. Its alerts still show in your inbox.`
              : "Alerts about it come to this phone, as your orgs' alert rules say."
          }
        >
          <CardBody>
            <Button
              label={muted ? "Unmute" : "Mute for an hour"}
              variant="outline"
              icon={muted ? Bell : BellOff}
              busy={busy === "mute"}
              disabled={!!busy}
              onPress={() => mute(!muted)}
            />
          </CardBody>
        </Card>
      ) : null}

      {s && can(s.permissions, "backups") ? (
        <Card
          title="Backups"
          description="Backs up the server's files now. Restore backups from the web app."
        >
          <CardBody>
            <Button
              label="Back up now"
              variant="outline"
              icon={Archive}
              busy={busy === "backup.create"}
              disabled={!connected || !!busy}
              onPress={() =>
                run("backup.create", "Backing up. It's listed in the web app once it's done.", {})
              }
            />
          </CardBody>
        </Card>
      ) : null}

      {result ? <Notice text={result.text} tone={result.ok ? "info" : "danger"} /> : null}

      {s ? (
        <Card
          title="In the web app"
          description="Files, settings, and anything that needs your passkey."
        >
          {[
            ["Files", "/files"],
            ["Settings", "/settings"],
          ].map(([label, path]) => (
            <Row
              key={label}
              chevron={false}
              onPress={() => web(path)}
              accessibilityLabel={`Open ${label} in the web app`}
            >
              <View
                style={{
                  flexDirection: "row",
                  alignItems: "center",
                  justifyContent: "space-between",
                }}
              >
                <Text>{label}</Text>
                <ExternalLink size={16} color={c.mutedForeground} />
              </View>
            </Row>
          ))}
        </Card>
      ) : null}
    </Page>
  );
}
