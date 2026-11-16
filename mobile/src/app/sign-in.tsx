import * as WebBrowser from "expo-web-browser";
import { useState } from "react";
import { Pressable, StyleSheet, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { FullLogo } from "@/components/logo";
import { Button, Notice, Text } from "@/components/ui";
import { signIn, useAuth } from "@/lib/auth";
import { appURL } from "@/lib/config";
import { register } from "@/lib/push";
import { space, useColors } from "@/lib/theme";

// Signing in, shaped like the web app's sign-in page (AuthLayout): the
// logo, the title and one sentence, the action, and what Raptor promises,
// ruled by hairlines.

const host = appURL.replace(/^https?:\/\//, "");

const promises = [
  "Alerts on this phone the moment a server goes down.",
  "Start, stop, and restart servers, and take backups.",
  "Deleting or wiping anything still needs your passkey, in the web app.",
];

export default function SignIn() {
  const c = useColors();
  const auth = useAuth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const signedOut = auth.status === "signedOut" ? (auth.error ?? "") : "";

  async function go() {
    setBusy(true);
    setError("");
    try {
      if (await signIn()) {
        // Right after signing in is when asking for notifications makes
        // sense: that's what the app is for.
        register(true).catch(() => {});
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <SafeAreaView style={[styles.screen, { backgroundColor: c.background }]}>
      <FullLogo height={20} />
      <View style={styles.main}>
        <View style={{ gap: 6 }}>
          <Text variant="headline" accessibilityRole="header">
            Sign in to Raptor
          </Text>
          <Text ink="muted">
            You sign in on {host} the way you always do, then come straight back here.
          </Text>
        </View>
        <Notice text={error || signedOut} tone={error ? "danger" : "info"} />
        <Button label="Sign in" onPress={go} busy={busy} accessibilityHint={`Opens ${host}`} />
        <View style={[styles.list, { borderTopColor: c.border }]}>
          {promises.map((p) => (
            <View key={p} style={[styles.item, { borderBottomColor: c.border }]}>
              <Text variant="meta" ink="muted">
                {p}
              </Text>
            </View>
          ))}
        </View>
      </View>
      <View style={styles.footer}>
        <Text variant="meta" ink="muted">
          Trouble signing in?
        </Text>
        <Pressable
          accessibilityRole="link"
          hitSlop={12}
          onPress={() => WebBrowser.openBrowserAsync("https://docs.raptorpanel.net/help/get-help/")}
        >
          <Text variant="meta" style={{ textDecorationLine: "underline" }}>
            Get help
          </Text>
        </Pressable>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1, paddingHorizontal: space.page + 8, paddingTop: 12, paddingBottom: 8, gap: 12 },
  main: { flex: 1, justifyContent: "center", gap: 20 },
  list: { borderTopWidth: StyleSheet.hairlineWidth, marginTop: 8 },
  item: { paddingVertical: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  footer: { flexDirection: "row", justifyContent: "center", gap: 4 },
});
