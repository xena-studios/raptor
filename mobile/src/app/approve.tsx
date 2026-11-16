import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { router } from "expo-router";
import { ShieldCheck } from "lucide-react-native";
import { useEffect, useState } from "react";
import { Pressable, StyleSheet, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { Button, Card, Empty, Notice, Row, Text } from "@/components/ui";
import { AuthService, type PhoneApproval } from "@/gen/raptor/panel/v1/auth_pb";
import { decide, LostSecret } from "@/lib/approvals";
import { radius, type as typeScale, useColors } from "@/lib/theme";
import { message } from "@/lib/transport";

// Approving a sign-in (or a "confirm it's you") from this phone: what's
// asking, and the number the browser shows, picked from three. Face ID
// unlocks the answer. The wrong number, or Deny, ends the request and the
// account is emailed.

function secondsLeft(a: PhoneApproval): number {
  return a.expiresAt
    ? Math.max(0, Math.round((timestampDate(a.expiresAt).getTime() - Date.now()) / 1000))
    : 0;
}

export default function Approve() {
  const c = useColors();
  const client = useQueryClient();
  const list = useQuery(AuthService.method.listPhoneApprovals, {}, { refetchInterval: 5_000 });
  const a = list.data?.approvals[0];
  const [busy, setBusy] = useState<number | "deny" | null>(null);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [left, setLeft] = useState(0);

  useEffect(() => {
    if (!a) return;
    setLeft(secondsLeft(a));
    const t = setInterval(() => setLeft(secondsLeft(a)), 1000);
    return () => clearInterval(t);
  }, [a]);

  async function answer(approve: boolean, n: number) {
    if (!a) return;
    setBusy(approve ? n : "deny");
    try {
      const status = await decide(a, approve, n);
      setResult(
        status === "approved"
          ? { ok: true, text: "Approved. The browser is signing in." }
          : { ok: false, text: approve ? "That wasn't the number, so it was denied." : "Denied." },
      );
      await client.invalidateQueries();
    } catch (err) {
      if (err instanceof LostSecret) setResult({ ok: false, text: err.message });
      // Face ID cancelled: stay, so they can try again.
      else if (!/cancel/i.test(String(err))) setResult({ ok: false, text: message(err) });
    } finally {
      setBusy(null);
    }
  }

  const close = () => (router.canGoBack() ? router.back() : router.replace("/"));

  if (result) {
    return (
      <SafeAreaView style={[styles.screen, { backgroundColor: c.background }]}>
        <View style={styles.center}>
          <Text variant="headline" accessibilityRole="header" style={{ textAlign: "center" }}>
            {result.ok ? "Approved" : "Not approved"}
          </Text>
          <Text ink={result.ok ? "muted" : "destructive"} style={{ textAlign: "center" }}>
            {result.text}
          </Text>
        </View>
        <Button label="Done" variant="outline" onPress={close} />
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={[styles.screen, { backgroundColor: c.background }]}>
      {!a ? (
        <View style={styles.center}>
          {list.isPending ? null : (
            <Empty
              icon={ShieldCheck}
              title="Nothing to approve"
              text="When you sign in to Raptor somewhere else, the request shows up here."
            />
          )}
          <Button label="Close" variant="ghost" onPress={close} />
        </View>
      ) : (
        <>
          <View style={{ gap: 6, paddingTop: 12 }}>
            <Text variant="headline" accessibilityRole="header">
              {a.purpose === "reauth"
                ? "Is this you confirming it's you?"
                : "Is this you signing in?"}
            </Text>
            <Text ink="muted">Pick the number the browser shows.</Text>
          </View>
          <Card>
            <Row first>
              <Text variant="label" mono ink="muted" style={{ textTransform: "uppercase" }}>
                From
              </Text>
              <Text>{a.userAgent}</Text>
              {a.ip ? (
                <Text mono variant="meta" ink="muted" selectable>
                  {a.ip}
                </Text>
              ) : null}
            </Row>
            <Row>
              <Text variant="label" mono ink="muted" style={{ textTransform: "uppercase" }}>
                Expires in
              </Text>
              <Text mono>{left}s</Text>
            </Row>
          </Card>
          <Notice text={list.error ? message(list.error) : ""} />
          <View
            style={styles.numbers}
            accessibilityRole="radiogroup"
            accessibilityLabel="The number the browser shows"
          >
            {a.choices.map((n) => (
              <Pressable
                key={n}
                accessibilityRole="button"
                accessibilityLabel={`${n}`}
                disabled={busy !== null || left === 0}
                onPress={() => answer(true, n)}
                style={({ pressed }) => [
                  styles.number,
                  {
                    borderColor: c.border,
                    backgroundColor: pressed ? c.muted : c.card,
                    opacity: busy !== null && busy !== n ? 0.5 : 1,
                  },
                ]}
              >
                <Text
                  mono
                  weight="medium"
                  style={[typeScale.headline, { fontSize: 34, lineHeight: 40, letterSpacing: 1 }]}
                >
                  {n}
                </Text>
              </Pressable>
            ))}
          </View>
          <View style={{ flex: 1 }} />
          <Button
            label="It wasn't me: deny"
            variant="destructive"
            busy={busy === "deny"}
            disabled={busy !== null}
            onPress={() => answer(false, 0)}
          />
          <Button label="Not now" variant="ghost" onPress={close} disabled={busy !== null} />
        </>
      )}
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1, paddingHorizontal: 16, paddingBottom: 8, gap: 20 },
  center: { flex: 1, alignItems: "center", justifyContent: "center", gap: 12 },
  numbers: { flexDirection: "row", gap: 10 },
  number: {
    flex: 1,
    height: 88,
    alignItems: "center",
    justifyContent: "center",
    borderWidth: 1,
    borderRadius: radius.xl,
  },
});
