import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { router } from "expo-router";
import { ShieldCheck } from "lucide-react-native";
import { useEffect, useState } from "react";
import { Pressable, StyleSheet, View } from "react-native";
import Animated from "react-native-reanimated";
import { SafeAreaView } from "react-native-safe-area-context";

import { Button, Card, Empty, Notice, Row, Text, useSink } from "@/components/ui";
import { AuthService, type PhoneApproval } from "@/gen/raptor/panel/v1/auth_pb";
import { decide, LostSecret } from "@/lib/approvals";
import { arrive, haptic } from "@/lib/motion";
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
      if (status === "approved") haptic.success();
      else haptic.error();
      setResult(
        status === "approved"
          ? { ok: true, text: "Approved. The browser is signing in." }
          : { ok: false, text: approve ? "That wasn't the number, so it was denied." : "Denied." },
      );
      await client.invalidateQueries();
    } catch (err) {
      // Face ID cancelled: stay, so they can try again.
      const cancelled = !(err instanceof LostSecret) && /cancel/i.test(String(err));
      if (!cancelled) {
        haptic.error();
        setResult({ ok: false, text: err instanceof LostSecret ? err.message : message(err) });
      }
    } finally {
      setBusy(null);
    }
  }

  const close = () => (router.canGoBack() ? router.back() : router.replace("/"));

  if (result) {
    return (
      <SafeAreaView style={[styles.screen, { backgroundColor: c.background }]}>
        <Animated.View entering={arrive} style={styles.center}>
          <Text variant="headline" accessibilityRole="header" style={{ textAlign: "center" }}>
            {result.ok ? "Approved" : "Not approved"}
          </Text>
          <Text ink={result.ok ? "muted" : "destructive"} style={{ textAlign: "center" }}>
            {result.text}
          </Text>
        </Animated.View>
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
              <Key
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
              </Key>
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

// Key is one of the numbers to pick: it sinks under the finger and taps
// back, as a keypad does.
function Key({
  onPress,
  children,
  ...rest
}: React.ComponentProps<typeof Pressable> & { onPress: () => void; children: React.ReactNode }) {
  const sink = useSink(0.95);
  return (
    <Animated.View style={[{ flex: 1 }, sink.style]}>
      <Pressable
        {...rest}
        onPressIn={sink.onPressIn}
        onPressOut={sink.onPressOut}
        onPress={() => {
          haptic.press();
          onPress();
        }}
      >
        {children}
      </Pressable>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1, paddingHorizontal: 16, paddingBottom: 8, gap: 20 },
  center: { flex: 1, alignItems: "center", justifyContent: "center", gap: 12 },
  numbers: { flexDirection: "row", gap: 10 },
  number: {
    height: 88,
    alignItems: "center",
    justifyContent: "center",
    borderWidth: 1,
    borderRadius: radius.xl,
  },
});
