import { useEffect, useRef, useState } from "react";
import { AppState, StyleSheet, View } from "react-native";
import Animated, { FadeOut } from "react-native-reanimated";
import { SafeAreaView } from "react-native-safe-area-context";

import { FullLogo } from "@/components/logo";
import { Button, Text } from "@/components/ui";
import { method, unlock, useLock } from "@/lib/lock";
import { easeOut, haptic } from "@/lib/motion";
import { useColors } from "@/lib/theme";

// LockScreen covers the app while it's locked, and asks for Face ID as
// soon as it's in front; or, while the app is in the background, just the
// logo, so the app switcher doesn't show what's behind.
export function LockScreen() {
  const c = useColors();
  const lock = useLock();
  const [how, setHow] = useState("Face ID");
  const [failed, setFailed] = useState(false);
  const asked = useRef(false);

  useEffect(() => {
    method().then(setHow);
  }, []);

  // Ask once each time it locks, when the app is in front.
  useEffect(() => {
    if (!lock.locked) {
      asked.current = false;
      return;
    }
    const ask = () => {
      if (asked.current || AppState.currentState !== "active") return;
      asked.current = true;
      unlock().then((ok) => {
        setFailed(!ok);
        if (!ok) haptic.error();
      });
    };
    ask();
    const sub = AppState.addEventListener("change", (s) => s === "active" && ask());
    return () => sub.remove();
  }, [lock.locked]);

  if (!lock.locked && !lock.covered) return null;
  return (
    // It covers at once (so the app switcher never sees the app), and
    // fades away once unlocked.
    <Animated.View
      exiting={FadeOut.duration(260).easing(easeOut)}
      style={[StyleSheet.absoluteFill, { backgroundColor: c.background }]}
    >
      <SafeAreaView style={styles.screen}>
        <View style={styles.center}>
          {/* The full logo, as on the splash screen: 240 points wide. */}
          <FullLogo height={38} />
          {lock.locked ? (
            <View style={{ gap: 6, alignItems: "center" }}>
              <Text variant="headline" accessibilityRole="header">
                Raptor is locked
              </Text>
              <Text ink="muted" style={{ textAlign: "center" }}>
                {failed ? `That didn't work. Try ${how} again.` : `Unlock it with ${how}.`}
              </Text>
            </View>
          ) : null}
        </View>
        {lock.locked ? (
          <Button
            label={`Unlock with ${how}`}
            onPress={() => {
              setFailed(false);
              unlock().then((ok) => {
                setFailed(!ok);
                if (!ok) haptic.error();
              });
            }}
          />
        ) : null}
      </SafeAreaView>
    </Animated.View>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1, paddingHorizontal: 24, paddingBottom: 12 },
  center: { flex: 1, alignItems: "center", justifyContent: "center", gap: 20 },
});
