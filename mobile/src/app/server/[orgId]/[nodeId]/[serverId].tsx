import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Stack, useLocalSearchParams } from "expo-router";
import * as WebBrowser from "expo-web-browser";
import { Archive, Bell, BellOff, ExternalLink, Play, RotateCw, Square } from "lucide-react-native";
import { useEffect, useRef, useState } from "react";
import { View } from "react-native";

import { Page, PageHeader } from "@/components/screen";
import { Button, Card, CardBody, Notice, Row, Skeleton, StatusChip, Text } from "@/components/ui";
import { AlertService } from "@/gen/raptor/panel/v1/alert_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { appURL } from "@/lib/config";
import { can } from "@/lib/format";
import { useLock } from "@/lib/lock";
import { MUTE_MINUTES } from "@/lib/push";
import { serverStatus } from "@/lib/status";
import { useColors } from "@/lib/theme";
import { alertClient, commandClient, message } from "@/lib/transport";

// A server: its state and address, power, and a backup now. The console,
// files, settings, and anything that needs a passkey are in the web app,
// which this links to.
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
    // A notification's button: "restart" or "mute".
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
  const status = s ? serverStatus(s, connected) : null;
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
      setResult({ ok: true, text: done });
      await client.invalidateQueries();
    } catch (err) {
      setResult({ ok: false, text: message(err) });
    } finally {
      setBusy("");
    }
  }

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
    if (requested === "restart" && can(s.permissions, "power")) {
      run("server.restart", "Restarting.");
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
          description="The console, files, settings, and anything that needs your passkey."
        >
          {[
            ["Console", ""],
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
