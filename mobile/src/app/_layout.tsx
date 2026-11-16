import { TransportProvider } from "@connectrpc/connect-query";
import {
  HankenGrotesk_400Regular,
  HankenGrotesk_500Medium,
  HankenGrotesk_600SemiBold,
  HankenGrotesk_700Bold,
} from "@expo-google-fonts/hanken-grotesk";
import {
  JetBrainsMono_400Regular,
  JetBrainsMono_500Medium,
} from "@expo-google-fonts/jetbrains-mono";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useFonts } from "expo-font";
import { DarkTheme, DefaultTheme, Stack, ThemeProvider } from "expo-router";
import * as SplashScreen from "expo-splash-screen";
import { useEffect } from "react";
import { useColorScheme } from "react-native";

import { LockScreen } from "@/components/lock-screen";
import { start, useAuth } from "@/lib/auth";
import { forgetLock, startLock, useLock } from "@/lib/lock";
import { listen, register } from "@/lib/push";
import { fonts, useColors } from "@/lib/theme";
import { transport } from "@/lib/transport";

SplashScreen.preventAutoHideAsync();

export const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, staleTime: 10_000 } },
});

start();
startLock();

export default function RootLayout() {
  const scheme = useColorScheme();
  const c = useColors();
  const auth = useAuth();
  const lock = useLock();
  const [loaded] = useFonts({
    HankenGrotesk_400Regular,
    HankenGrotesk_500Medium,
    HankenGrotesk_600SemiBold,
    HankenGrotesk_700Bold,
    JetBrainsMono_400Regular,
    JetBrainsMono_500Medium,
  });
  const ready = loaded && lock.loaded && auth.status !== "loading";
  const signedIn = auth.status === "signedIn";

  useEffect(() => {
    if (ready) SplashScreen.hideAsync();
  }, [ready]);

  // Signed in: keep the push token current and open tapped alerts. The
  // phone registers again on every start in case its token changed; it
  // only asks for permission right after signing in.
  useEffect(() => {
    if (!signedIn) {
      queryClient.clear();
      // The lock belongs to the signed-in phone; signing out ends it.
      if (auth.status === "signedOut") forgetLock();
      return;
    }
    register(false).catch(() => {});
    return listen();
  }, [signedIn, auth.status]);

  // Nothing renders until the fonts are in: text laid out before they
  // load keeps the system font. The splash screen covers it, and then the
  // session check.
  if (!loaded) return null;

  const base = scheme === "dark" ? DarkTheme : DefaultTheme;
  const theme = {
    ...base,
    colors: {
      ...base.colors,
      primary: c.primary,
      background: c.background,
      card: c.background,
      text: c.foreground,
      border: c.border,
    },
  };
  return (
    <ThemeProvider value={theme}>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={queryClient}>
          <Stack
            screenOptions={{
              headerShown: false,
              headerTitleStyle: { fontFamily: fonts.sansMedium, color: c.foreground },
              headerTintColor: c.foreground,
              headerStyle: { backgroundColor: c.background },
              headerShadowVisible: false,
              headerBackButtonDisplayMode: "minimal",
              contentStyle: { backgroundColor: c.background },
            }}
          >
            <Stack.Protected guard={signedIn}>
              <Stack.Screen name="(tabs)" />
              <Stack.Screen
                name="server/[orgId]/[nodeId]/[serverId]"
                options={{ headerShown: true, title: "" }}
              />
              <Stack.Screen name="approve" options={{ presentation: "modal" }} />
              <Stack.Screen
                name="console/[orgId]/[nodeId]/[serverId]"
                options={{ headerShown: true, title: "Console" }}
              />
            </Stack.Protected>
            <Stack.Protected guard={!signedIn}>
              <Stack.Screen name="sign-in" />
            </Stack.Protected>
          </Stack>
          {signedIn ? <LockScreen /> : null}
        </QueryClientProvider>
      </TransportProvider>
    </ThemeProvider>
  );
}
