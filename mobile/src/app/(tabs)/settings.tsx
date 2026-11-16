import { useQuery } from "@connectrpc/connect-query";
import * as Application from "expo-application";
import * as Notifications from "expo-notifications";
import * as WebBrowser from "expo-web-browser";
import { ExternalLink } from "lucide-react-native";
import { useEffect, useState } from "react";
import { Linking, View } from "react-native";

import { Page, PageHeader } from "@/components/screen";
import { Button, Card, CardBody, Notice, Row, Skeleton, SwitchRow, Text } from "@/components/ui";
import { AlertService } from "@/gen/raptor/panel/v1/alert_pb";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { canHoldSecret, enroll, unenroll } from "@/lib/approvals";
import { signOut } from "@/lib/auth";
import { appURL } from "@/lib/config";
import { available, method, setEnabled, useLock } from "@/lib/lock";
import { type PushStatus, register, unregister } from "@/lib/push";
import { useColors } from "@/lib/theme";
import { authClient, message } from "@/lib/transport";

export default function Settings() {
  const c = useColors();
  const me = useQuery(AuthService.method.getSession, {});
  const channels = useQuery(AlertService.method.listAlertChannels, {});
  const [push, setPush] = useState<PushStatus | "off" | null>(null);
  const [busy, setBusy] = useState(false);
  const [signingOut, setSigningOut] = useState(false);
  const [error, setError] = useState("");
  const lock = useLock();
  const [canLock, setCanLock] = useState(false);
  const [how, setHow] = useState("Face ID");
  const [locking, setLocking] = useState(false);

  useEffect(() => {
    available().then(setCanLock);
    method().then(setHow);
  }, []);

  // Approving sign-ins from this phone instead of typing codes.
  const approver = useQuery(AuthService.method.getPhoneApprover, {});
  const [approving, setApproving] = useState(false);
  async function toggleApprover(on: boolean) {
    setApproving(true);
    setError("");
    try {
      if (on) await enroll();
      else await unenroll();
      await approver.refetch();
    } catch (err) {
      if (!/cancel/i.test(String(err))) setError(message(err));
    } finally {
      setApproving(false);
    }
  }
  const approverNote = approver.data?.enrolled
    ? "Sign-ins and confirming it's you ask this phone, instead of a code."
    : approver.data?.reason ||
      (canHoldSecret()
        ? `Approve with ${how} instead of typing your authenticator app's code.`
        : `Set up ${how} on this phone first.`);

  async function toggleLock(on: boolean) {
    setLocking(true);
    setError("");
    try {
      await setEnabled(on);
    } catch (err) {
      setError(message(err));
    } finally {
      setLocking(false);
    }
  }

  // Whether this phone gets alerts: it's one of the person's channels, and
  // on.
  const phone = channels.data?.channels.find((ch) => ch.thisDevice && ch.enabled);
  useEffect(() => {
    Notifications.getPermissionsAsync().then((p) => {
      if (!p.granted && !p.canAskAgain) setPush("denied");
      else if (channels.data) setPush(phone ? "on" : "off");
    });
  }, [channels.data, phone]);

  async function toggle(on: boolean) {
    setBusy(true);
    setError("");
    try {
      if (on) setPush(await register(true));
      else {
        await unregister();
        setPush("off");
      }
      await channels.refetch();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const user = me.data?.user;
  const pushNote =
    push === "denied"
      ? "Notifications are off for Raptor in this phone's settings."
      : push === "unavailable"
        ? "This phone can't get push notifications."
        : "Which alerts you get is up to your orgs' alert rules.";

  const links: [string, string][] = [
    ["Open Raptor", "/"],
    ["Sign-in and devices", "/settings/security"],
    ["Help", "https://docs.raptorpanel.net"],
  ];

  return (
    <Page header={<PageHeader title="Settings" />}>
      <Notice text={error} />

      <Card title="Account">
        <CardBody>
          {user ? (
            <View style={{ gap: 2 }}>
              <Text weight="medium">{user.name || user.email}</Text>
              {user.name ? (
                <Text variant="meta" ink="muted">
                  {user.email}
                </Text>
              ) : null}
            </View>
          ) : (
            <Skeleton width="55%" />
          )}
        </CardBody>
      </Card>

      <Card title="This phone">
        <SwitchRow
          first
          title="Alerts"
          note={pushNote}
          value={push === "on"}
          disabled={busy || push === null || push === "unavailable" || push === "denied"}
          onChange={toggle}
        />
        {push === "denied" ? (
          <Row
            onPress={() => Linking.openSettings()}
            accessibilityLabel="Open this phone's settings"
          >
            <Text>Open phone settings</Text>
          </Row>
        ) : null}
        {canLock ? (
          <SwitchRow
            title={`Lock with ${how}`}
            note="Asks when Raptor opens, and when you come back after a minute away."
            value={lock.enabled}
            disabled={locking}
            onChange={toggleLock}
          />
        ) : null}
        {approver.data ? (
          <SwitchRow
            title="Approve sign-ins"
            note={approverNote}
            value={approver.data.enrolled}
            disabled={
              approving ||
              (!approver.data.enrolled && (!approver.data.canEnroll || !canHoldSecret()))
            }
            onChange={toggleApprover}
          />
        ) : null}
      </Card>

      <Card title="In the web app">
        {links.map(([label, to]) => (
          <Row
            key={label}
            chevron={false}
            accessibilityLabel={`Open ${label}`}
            onPress={() =>
              WebBrowser.openBrowserAsync(to.startsWith("http") ? to : `${appURL}${to}`)
            }
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

      <View style={{ gap: 12 }}>
        <Button
          label="Sign out"
          variant="destructive"
          busy={signingOut}
          onPress={async () => {
            setSigningOut(true);
            await signOut(() => authClient.signOut({}));
          }}
        />
        <Text variant="label" mono ink="muted" style={{ textAlign: "center" }}>
          Raptor {Application.nativeApplicationVersion ?? ""} (
          {Application.nativeBuildVersion ?? ""})
        </Text>
      </View>
    </Page>
  );
}
