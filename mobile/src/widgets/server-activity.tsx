import { HStack, Image, Spacer, Text, VStack } from "@expo/ui/swift-ui";
import {
  containerBackground,
  font,
  foregroundStyle,
  frame,
  lineLimit,
  padding,
} from "@expo/ui/swift-ui/modifiers";
import { createLiveActivity, type LiveActivityEnvironment } from "expo-widgets";

// A Live Activity (iOS) while a server starts, restarts, or stops from the
// app: its name and state on the lock screen and in the Dynamic Island,
// until it settles. The app updates it while it's open (lib/activity.ts).

export type ServerActivityProps = {
  server: string;
  node: string;
  // What's happening and how it is now: "Restarting", "Running".
  doing: string;
  label: string;
  tone: "live" | "pending" | "failed" | "stopped";
  done: boolean;
};

const ServerActivity = (props: ServerActivityProps, _env: LiveActivityEnvironment) => {
  "widget";
  const fg = "#f5f0f0";
  const muted = "#9f9695";
  const red = "#f05653";
  const tones = { live: "#66da85", pending: "#f7b83d", failed: "#fb718c", stopped: muted };
  const color = tones[props.tone];
  const chip = `[ ${props.label.toUpperCase()} ]`;
  const icon = props.done
    ? props.tone === "live"
      ? "checkmark.circle.fill"
      : "exclamationmark.circle.fill"
    : "arrow.clockwise";

  return {
    banner: (
      <VStack
        alignment="leading"
        spacing={6}
        modifiers={[
          containerBackground("#0f0808", "widget"),
          frame({ maxWidth: Infinity, alignment: "leading" }),
          padding({ all: 16 }),
        ]}
      >
        <HStack>
          <Text
            modifiers={[
              font({ size: 11, weight: "semibold", design: "monospaced" }),
              foregroundStyle(red),
            ]}
          >
            {`// ${props.node.toUpperCase()}`}
          </Text>
          <Spacer />
          <Text modifiers={[font({ size: 11, design: "monospaced" }), foregroundStyle(color)]}>
            {chip}
          </Text>
        </HStack>
        <Text modifiers={[font({ size: 18, weight: "bold" }), foregroundStyle(fg), lineLimit(1)]}>
          {props.server}
        </Text>
        <Text modifiers={[font({ size: 13 }), foregroundStyle(muted)]}>
          {props.done ? `${props.doing}: done.` : `${props.doing}…`}
        </Text>
      </VStack>
    ),
    compactLeading: <Image systemName={icon} size={14} color={color} />,
    compactTrailing: (
      <Text
        modifiers={[font({ size: 12, design: "monospaced" }), foregroundStyle(color), lineLimit(1)]}
      >
        {props.label.toUpperCase()}
      </Text>
    ),
    minimal: <Image systemName={icon} size={14} color={color} />,
    expandedLeading: (
      <Text
        modifiers={[
          font({ size: 14, weight: "semibold" }),
          foregroundStyle(fg),
          lineLimit(1),
          padding({ leading: 8 }),
        ]}
      >
        {props.server}
      </Text>
    ),
    expandedTrailing: (
      <Text
        modifiers={[
          font({ size: 12, design: "monospaced" }),
          foregroundStyle(color),
          padding({ trailing: 8 }),
        ]}
      >
        {chip}
      </Text>
    ),
    expandedBottom: (
      <Text modifiers={[font({ size: 13 }), foregroundStyle(muted), padding({ horizontal: 8 })]}>
        {props.done ? `${props.doing} on ${props.node}: done.` : `${props.doing} on ${props.node}…`}
      </Text>
    ),
  };
};

export default createLiveActivity<ServerActivityProps>("ServerActivity", ServerActivity);
