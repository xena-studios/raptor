import { HStack, Spacer, Text, VStack } from "@expo/ui/swift-ui";
import {
  containerBackground,
  font,
  foregroundStyle,
  frame,
  lineLimit,
  padding,
} from "@expo/ui/swift-ui/modifiers";
import { createWidget, type WidgetEnvironment } from "expo-widgets";

// The home screen widget (iOS): how many of your servers are running, and
// the ones that aren't, in The Console's colours. The app writes it whenever
// it loads servers (lib/widget.ts); iOS doesn't let a widget fetch on its
// own here, so it says when it was last updated.

export type WidgetServer = {
  name: string;
  label: string;
  tone: "live" | "pending" | "failed" | "stopped";
};

export type ServersWidgetProps = {
  running: number;
  total: number;
  // Servers that need a look first (failed, then pending), then the rest.
  servers: WidgetServer[];
  // When the app last wrote it, ready to show ("3:41 PM").
  updated: string;
  signedOut?: boolean;
};

const ServersWidget = (props: ServersWidgetProps, env: WidgetEnvironment) => {
  "widget";
  // The widget runs in its own runtime: everything it uses is in here.
  const dark = env.colorScheme !== "light";
  const bg = dark ? "#0f0808" : "#f9f6f5";
  const fg = dark ? "#f5f0f0" : "#190f0e";
  const muted = dark ? "#9f9695" : "#5f5554";
  const red = dark ? "#f05653" : "#d01025";
  const tones = {
    live: dark ? "#66da85" : "#25793f",
    pending: dark ? "#f7b83d" : "#915c08",
    failed: dark ? "#fb718c" : "#c31851",
    stopped: muted,
  };
  const small = env.widgetFamily === "systemSmall";
  const down = props.servers.filter((s) => s.tone === "failed").length;

  const kicker = (
    <HStack>
      <Text
        modifiers={[
          font({ size: 11, weight: "semibold", design: "monospaced" }),
          foregroundStyle(red),
        ]}
      >
        RAPTOR
      </Text>
      <Spacer />
      {down > 0 ? (
        <Text modifiers={[font({ size: 11, design: "monospaced" }), foregroundStyle(tones.failed)]}>
          {`${down} DOWN`}
        </Text>
      ) : null}
    </HStack>
  );

  if (props.signedOut || props.total === 0) {
    return (
      <VStack
        alignment="leading"
        spacing={6}
        modifiers={[
          containerBackground(bg, "widget"),
          frame({ maxWidth: Infinity, maxHeight: Infinity, alignment: "topLeading" }),
          padding({ all: 2 }),
        ]}
      >
        {kicker}
        <Spacer />
        <Text modifiers={[font({ size: 13 }), foregroundStyle(muted)]}>
          {props.signedOut ? "Open Raptor to sign in." : "No servers yet."}
        </Text>
      </VStack>
    );
  }

  const readout = (
    <VStack alignment="leading" spacing={0}>
      <Text
        modifiers={[
          font({ size: small ? 34 : 28, weight: "medium", design: "monospaced" }),
          foregroundStyle(fg),
        ]}
      >
        {`${props.running}/${props.total}`}
      </Text>
      <Text modifiers={[font({ size: 12 }), foregroundStyle(muted)]}>running</Text>
    </VStack>
  );

  return (
    <VStack
      alignment="leading"
      spacing={8}
      modifiers={[
        containerBackground(bg, "widget"),
        frame({ maxWidth: Infinity, maxHeight: Infinity, alignment: "topLeading" }),
        padding({ all: 2 }),
      ]}
    >
      {kicker}
      {small ? (
        <VStack alignment="leading" spacing={6}>
          <Spacer />
          {readout}
        </VStack>
      ) : (
        <HStack alignment="top" spacing={16}>
          {readout}
          <VStack
            alignment="leading"
            spacing={4}
            modifiers={[frame({ maxWidth: Infinity, alignment: "leading" })]}
          >
            {props.servers.slice(0, 4).map((s) => (
              <HStack key={s.name} spacing={6}>
                <Text modifiers={[font({ size: 13 }), foregroundStyle(fg), lineLimit(1)]}>
                  {s.name}
                </Text>
                <Spacer />
                <Text
                  modifiers={[
                    font({ size: 10, design: "monospaced" }),
                    foregroundStyle(tones[s.tone]),
                  ]}
                >
                  {`[ ${s.label.toUpperCase()} ]`}
                </Text>
              </HStack>
            ))}
          </VStack>
        </HStack>
      )}
      <Spacer />
      <Text
        modifiers={[font({ size: 10, design: "monospaced" }), foregroundStyle(muted)]}
      >{`UPDATED ${props.updated}`}</Text>
    </VStack>
  );
};

export default createWidget<ServersWidgetProps>("ServersWidget", ServersWidget, {
  running: 0,
  total: 0,
  servers: [],
  updated: "",
  signedOut: true,
});
