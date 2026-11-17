import { useQuery } from "@connectrpc/connect-query";
import { Stack, useLocalSearchParams } from "expo-router";
import { SendHorizontal } from "lucide-react-native";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  FlatList,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { Text } from "@/components/ui";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { accessToken } from "@/lib/auth";
import { apiURL } from "@/lib/config";
import { can } from "@/lib/format";
import { fonts, radius } from "@/lib/theme";
import { commandClient, message } from "@/lib/transport";

// A server's live console: the web app's live WebSocket (/api/live), with
// the app's token, watching one console; and a box to send commands with
// the console.write permission. Machines talk on terminal ink, the same in
// both themes (docs/DESIGN.md). Colour codes are stripped.

const ink = "#0a0c11";
const inkText = "#e1e4e9";
const inkMuted = "#90959d";
const maxLines = 2000;

// biome-ignore lint/suspicious/noControlCharactersInRegex: ANSI escapes are control characters
const ansi = /\u001b\[[0-9;?]*[ -/]*[@-~]|\u001b\][^\u0007]*\u0007/g;

type Line = { key: number; text: string };
type Out = {
  id: string;
  type: string;
  lines?: string[];
  reset?: boolean;
  error?: string;
  retry?: boolean;
};

export default function Console() {
  const { orgId, nodeId, serverId } = useLocalSearchParams<{
    orgId: string;
    nodeId: string;
    serverId: string;
  }>();
  const server = useQuery(OrgService.method.getServer, { orgId, nodeId, serverId });
  const s = server.data?.server;
  const canWrite = s ? can(s.permissions, "console.write") : false;
  const [lines, setLines] = useState<Line[]>([]);
  const [status, setStatus] = useState("Connecting");
  const [command, setCommand] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  const list = useRef<FlatList<Line>>(null);
  const atBottom = useRef(true);
  const seq = useRef(0);

  const add = useCallback((texts: string[], reset: boolean) => {
    setLines((prev) => {
      const next = (reset ? [] : prev).concat(
        texts.map((t) => ({ key: seq.current++, text: t.replace(ansi, "") })),
      );
      return next.length > maxLines ? next.slice(next.length - maxLines) : next;
    });
  }, []);

  // One socket while the screen is open; it reconnects (with a fresh
  // token) after a drop or a deploy, and stops for good when the console
  // can't be watched.
  useEffect(() => {
    let closed = false;
    let ws: WebSocket | null = null;
    let wait = 1000;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const connect = async () => {
      let token: string;
      try {
        token = await accessToken();
      } catch {
        setStatus("Signed out");
        return;
      }
      if (closed) return;
      const url = `${apiURL.replace(/^http/, "ws")}/api/live`;
      // React Native's WebSocket takes headers as a third argument.
      const Ctor = WebSocket as unknown as new (
        url: string,
        protocols: string[] | undefined,
        options: { headers: Record<string, string> },
      ) => WebSocket;
      ws = new Ctor(url, undefined, { headers: { Authorization: `Bearer ${token}` } });
      ws.onopen = () => {
        wait = 1000;
        setStatus("Live");
        ws?.send(JSON.stringify({ op: "console", id: "c", node: nodeId, server: serverId }));
      };
      ws.onmessage = (e) => {
        let m: Out;
        try {
          m = JSON.parse(String(e.data)) as Out;
        } catch {
          return;
        }
        if (m.type === "lines") add(m.lines ?? [], !!m.reset);
        else if (m.type === "ended") {
          if (m.retry) setStatus("Reconnecting");
          else {
            closed = true;
            setStatus(m.error ? m.error : "Ended");
            ws?.close();
          }
        }
      };
      ws.onclose = () => {
        if (closed) return;
        setStatus("Reconnecting");
        timer = setTimeout(connect, wait);
        wait = Math.min(wait * 2, 30_000);
      };
    };
    connect();
    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      ws?.close();
    };
  }, [nodeId, serverId, add]);

  async function send() {
    const c = command.trim();
    if (!c) return;
    setSending(true);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        serverId,
        action: "server.command",
        paramsJson: JSON.stringify({ command: c }),
      });
      setCommand("");
      atBottom.current = true;
    } catch (err) {
      setError(message(err));
    } finally {
      setSending(false);
    }
  }

  return (
    <SafeAreaView edges={["bottom"]} style={{ flex: 1, backgroundColor: ink }}>
      <Stack.Screen
        options={{
          title: s?.name ?? "Console",
          headerStyle: { backgroundColor: ink },
          headerTintColor: inkText,
          headerTitleStyle: { fontFamily: fonts.sansMedium, color: inkText },
        }}
      />
      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
        keyboardVerticalOffset={Platform.OS === "ios" ? 100 : 0}
      >
        <View style={styles.bar}>
          <View
            style={[styles.dot, { backgroundColor: status === "Live" ? "#66da85" : inkMuted }]}
          />
          <Text
            variant="label"
            mono
            style={{ color: inkMuted, textTransform: "uppercase" }}
            accessibilityLiveRegion="polite"
          >
            {status}
          </Text>
        </View>
        <FlatList
          ref={list}
          data={lines}
          keyExtractor={(l) => String(l.key)}
          style={{ flex: 1 }}
          contentContainerStyle={{ paddingHorizontal: 12, paddingBottom: 8 }}
          onScroll={(e) => {
            const { contentOffset, contentSize, layoutMeasurement } = e.nativeEvent;
            atBottom.current =
              contentOffset.y + layoutMeasurement.height >= contentSize.height - 40;
          }}
          scrollEventThrottle={100}
          onContentSizeChange={() => {
            if (atBottom.current) list.current?.scrollToEnd({ animated: false });
          }}
          renderItem={({ item }) => (
            <Text mono selectable style={styles.line}>
              {item.text || " "}
            </Text>
          )}
        />
        {error ? (
          <Text variant="meta" style={{ color: "#fb718c", paddingHorizontal: 12 }}>
            {error}
          </Text>
        ) : null}
        {canWrite ? (
          <View style={styles.input}>
            <Text mono style={{ color: inkMuted }}>
              {">"}
            </Text>
            <TextInput
              value={command}
              onChangeText={setCommand}
              onSubmitEditing={send}
              placeholder="Send a command"
              placeholderTextColor={inkMuted}
              autoCapitalize="none"
              autoCorrect={false}
              returnKeyType="send"
              editable={!sending}
              accessibilityLabel="Command"
              style={styles.field}
            />
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Send"
              disabled={sending || !command.trim()}
              onPress={send}
              hitSlop={10}
              style={{ opacity: sending || !command.trim() ? 0.4 : 1 }}
            >
              <SendHorizontal size={20} color={inkText} />
            </Pressable>
          </View>
        ) : null}
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  bar: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  dot: { width: 7, height: 7, borderRadius: 4 },
  line: { color: inkText, fontSize: 12, lineHeight: 17 },
  input: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    marginHorizontal: 12,
    marginVertical: 8,
    paddingHorizontal: 12,
    borderWidth: 1,
    borderColor: "#262a31",
    borderRadius: radius.lg,
  },
  field: { flex: 1, minHeight: 44, color: inkText, fontFamily: fonts.mono, fontSize: 14 },
});
