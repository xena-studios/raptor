import { ChevronRight, type LucideIcon } from "lucide-react-native";
import type { ReactNode } from "react";
import {
  ActivityIndicator,
  Pressable,
  Text as RNText,
  type StyleProp,
  StyleSheet,
  Switch,
  type TextProps,
  type TextStyle,
  View,
  type ViewStyle,
} from "react-native";

import type { ServerStatus, Tone } from "@/lib/status";
import { type Colors, fonts, radius, space, type, useColors } from "@/lib/theme";

// The Console's pieces for the app (docs/DESIGN.md), shaped like the web
// app's (web/src/components/ui and page.tsx): sentences in Hanken Grotesk,
// data, labels, buttons, and states in JetBrains Mono; sharp corners;
// hairlines, never shadows; state as a [ BRACKETED ] readout.

type Ink = "default" | "muted" | "primary" | "destructive";

function ink(c: Colors, i: Ink): string {
  return {
    default: c.foreground,
    muted: c.mutedForeground,
    primary: c.primary,
    destructive: c.destructive,
  }[i];
}

type Variant = keyof typeof type;

// Text is a sentence (sans) or, with mono, a machine readout: tabular
// figures, so numbers line up.
export function Text({
  variant = "body",
  mono,
  weight,
  ink: i = "default",
  style,
  ...rest
}: TextProps & {
  variant?: Variant;
  mono?: boolean;
  weight?: "regular" | "medium" | "semibold";
  ink?: Ink;
}) {
  const c = useColors();
  const face: TextStyle = mono
    ? {
        fontFamily: weight === "medium" || weight === "semibold" ? fonts.monoMedium : fonts.mono,
        fontVariant: ["tabular-nums"],
      }
    : weight
      ? {
          fontFamily: {
            regular: fonts.sans,
            medium: fonts.sansMedium,
            semibold: fonts.sansSemibold,
          }[weight],
        }
      : {};
  return <RNText {...rest} style={[type[variant], face, { color: ink(c, i) }, style]} />;
}

// Kicker names the context above a title, `// like this`: once a page.
export function Kicker({ children }: { children: string }) {
  const c = useColors();
  return (
    <RNText style={[type.kicker, { color: c.mutedForeground, textTransform: "uppercase" }]}>
      {`// ${children}`}
    </RNText>
  );
}

const toneColor = (c: Colors, t: Tone) =>
  ({
    live: c.ok,
    pending: c.warn,
    failed: c.destructive,
    stopped: c.mutedForeground,
    stale: c.mutedForeground,
  })[t];

// StatusChip shows a state as a readout: [ RUNNING ]. A stale one (its
// node is offline) is dimmed, since it's only the node's last report.
export function StatusChip({ status }: { status: ServerStatus }) {
  const c = useColors();
  return (
    <Text
      variant="label"
      mono
      accessibilityLabel={status.label}
      style={{
        color: toneColor(c, status.tone),
        opacity: status.tone === "stale" ? 0.6 : 1,
        textTransform: "uppercase",
      }}
    >
      [ {status.label} ]
    </Text>
  );
}

// Dot is an alert's level: round, as in the web app's bell.
export function Dot({ color }: { color: string }) {
  return <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: color }} />;
}

export function Button({
  label,
  onPress,
  variant = "primary",
  busy,
  disabled,
  icon: Icon,
  style,
  accessibilityHint,
}: {
  label: string;
  onPress: () => void;
  variant?: "primary" | "outline" | "ghost" | "destructive";
  busy?: boolean;
  disabled?: boolean;
  icon?: LucideIcon;
  style?: StyleProp<ViewStyle>;
  accessibilityHint?: string;
}) {
  const c = useColors();
  const look = {
    primary: { bg: c.primary, fg: c.primaryForeground, border: c.primary, pressed: c.primary },
    outline: { bg: c.background, fg: c.foreground, border: c.border, pressed: c.muted },
    ghost: { bg: "transparent", fg: c.foreground, border: "transparent", pressed: c.muted },
    destructive: {
      bg: c.dangerWash,
      fg: c.destructive,
      border: c.dangerWash,
      pressed: c.dangerWash,
    },
  }[variant];
  const off = disabled || busy;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityHint={accessibilityHint}
      accessibilityState={{ disabled: !!off, busy: !!busy }}
      disabled={off}
      onPress={onPress}
      style={({ pressed }) => [
        styles.button,
        {
          backgroundColor: pressed ? look.pressed : look.bg,
          borderColor: look.border,
          opacity: off ? 0.5 : pressed && variant === "primary" ? 0.82 : 1,
          transform: [{ translateY: pressed ? 1 : 0 }],
        },
        style,
      ]}
    >
      {variant === "primary" ? (
        <View pointerEvents="none" style={[styles.keyLight, { backgroundColor: c.keyLight }]} />
      ) : null}
      {busy ? (
        <ActivityIndicator size="small" color={look.fg} />
      ) : Icon ? (
        <Icon size={16} color={look.fg} strokeWidth={2} />
      ) : null}
      <RNText style={[type.button, { color: look.fg, textTransform: "uppercase" }]}>{label}</RNText>
    </Pressable>
  );
}

// Card is a group on a page: raised ink framed by a hairline, with an
// optional title and description, and rows divided by rules.
export function Card({
  title,
  description,
  right,
  children,
  style,
}: {
  title?: string;
  description?: string;
  right?: ReactNode;
  children?: ReactNode;
  style?: StyleProp<ViewStyle>;
}) {
  const c = useColors();
  return (
    <View style={[styles.card, { backgroundColor: c.card, borderColor: c.ring }, style]}>
      {title ? (
        <View style={styles.cardHeader}>
          <View style={{ flex: 1, gap: 2 }}>
            <Text variant="title">{title}</Text>
            {description ? (
              <Text variant="meta" ink="muted">
                {description}
              </Text>
            ) : null}
          </View>
          {right}
        </View>
      ) : null}
      {children}
    </View>
  );
}

// CardBody pads content inside a card.
export function CardBody({
  children,
  style,
}: {
  children: ReactNode;
  style?: StyleProp<ViewStyle>;
}) {
  return (
    <View style={[{ paddingHorizontal: space.card, paddingBottom: space.card, gap: 12 }, style]}>
      {children}
    </View>
  );
}

// Row is a line in a card's list, divided by a rule; tappable ones get a
// chevron and a pressed tint.
export function Row({
  children,
  onPress,
  accessibilityLabel,
  chevron = !!onPress,
  first,
}: {
  children: ReactNode;
  onPress?: () => void;
  accessibilityLabel?: string;
  chevron?: boolean;
  first?: boolean;
}) {
  const c = useColors();
  const inner = (pressed: boolean) => (
    <View
      style={[
        styles.row,
        {
          borderTopColor: c.border,
          borderTopWidth: first ? 0 : StyleSheet.hairlineWidth,
          backgroundColor: pressed ? c.muted : "transparent",
        },
      ]}
    >
      <View style={{ flex: 1, gap: 3 }}>{children}</View>
      {chevron ? <ChevronRight size={18} color={c.mutedForeground} /> : null}
    </View>
  );
  if (!onPress) return inner(false);
  return (
    <Pressable accessibilityRole="button" accessibilityLabel={accessibilityLabel} onPress={onPress}>
      {({ pressed }) => inner(pressed)}
    </Pressable>
  );
}

// SwitchRow is a setting that's on or off: a sentence for a title (sans,
// medium, as DESIGN.md has switch rows), a line under it, and the switch.
export function SwitchRow({
  title,
  note,
  value,
  disabled,
  onChange,
  first,
}: {
  title: string;
  note?: string;
  value: boolean;
  disabled?: boolean;
  onChange: (on: boolean) => void;
  first?: boolean;
}) {
  const c = useColors();
  return (
    <Row first={first}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 12 }}>
        <View style={{ flex: 1, gap: 2 }}>
          <Text weight="medium">{title}</Text>
          {note ? (
            <Text variant="meta" ink="muted">
              {note}
            </Text>
          ) : null}
        </View>
        <Switch
          value={value}
          disabled={disabled}
          onValueChange={onChange}
          trackColor={{ true: c.primary, false: c.muted }}
          accessibilityLabel={title}
        />
      </View>
    </Row>
  );
}

// Notice is a banner: a wash of its state's colour, as the web's alerts.
export function Notice({ text, tone = "danger" }: { text: string; tone?: "danger" | "info" }) {
  const c = useColors();
  if (!text) return null;
  const danger = tone === "danger";
  return (
    <View
      accessibilityRole="alert"
      style={[
        styles.notice,
        {
          backgroundColor: danger ? c.dangerWash : c.card,
          borderColor: danger ? c.dangerWash : c.ring,
        },
      ]}
    >
      <Text variant="meta" ink={danger ? "destructive" : "muted"}>
        {text}
      </Text>
    </View>
  );
}

// Empty is an empty state that says what goes here and how it gets there.
export function Empty({
  icon: Icon,
  title,
  text,
}: {
  icon?: LucideIcon;
  title: string;
  text?: string;
}) {
  const c = useColors();
  return (
    <View style={[styles.empty, { borderColor: c.border }]}>
      {Icon ? <Icon size={26} color={c.mutedForeground} strokeWidth={1.75} /> : null}
      <Text weight="medium" style={{ textAlign: "center" }}>
        {title}
      </Text>
      {text ? (
        <Text variant="meta" ink="muted" style={{ textAlign: "center", maxWidth: 300 }}>
          {text}
        </Text>
      ) : null}
    </View>
  );
}

// Skeleton is a placeholder line while something loads.
export function Skeleton({
  width,
  height = 14,
}: {
  width: number | `${number}%`;
  height?: number;
}) {
  const c = useColors();
  return <View style={{ width, height, borderRadius: radius.sm, backgroundColor: c.muted }} />;
}

const styles = StyleSheet.create({
  button: {
    minHeight: 44,
    paddingHorizontal: 14,
    borderRadius: radius.lg,
    borderWidth: 1,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 8,
    overflow: "hidden",
  },
  keyLight: { position: "absolute", top: 0, left: 0, right: 0, height: 1 },
  card: { borderRadius: radius.xl, borderWidth: 1, overflow: "hidden" },
  cardHeader: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: 12,
    paddingHorizontal: space.card,
    paddingTop: space.card,
    paddingBottom: 12,
  },
  row: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    minHeight: 52,
    paddingHorizontal: space.card,
    paddingVertical: 12,
  },
  notice: { borderWidth: 1, borderRadius: radius.lg, paddingHorizontal: 12, paddingVertical: 10 },
  empty: {
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 24,
    paddingVertical: 40,
    borderRadius: radius.xl,
    borderWidth: 1,
    borderStyle: "dashed",
  },
});
