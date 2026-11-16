import { type TextStyle, useColorScheme } from "react-native";

// The Console (docs/DESIGN.md), as the web app defines it in
// web/src/index.css: ink tinted toward the brand's red, one Signal Red,
// and state colours that only ever mean state. Hex from the web's OKLCH.
// "The Console" is the dark theme and "Daylight" the light one; the app
// follows the phone's setting.

const daylight = {
  background: "#f9f6f5", // Daylight
  card: "#f4efee", // Daylight raised
  muted: "#eae3e2", // Daylight muted: pressed rows, secondary buttons
  border: "#ded5d4", // Rule
  foreground: "#190f0e", // Graphite
  mutedForeground: "#5f5554", // Slate
  primary: "#d01025", // Signal Red, Daylight
  primaryForeground: "#fff9f8",
  destructive: "#c31851",
  ok: "#25793f",
  warn: "#915c08",
  okWash: "#ccf4d3",
  warnWash: "#ffe4b3",
  dangerWash: "#ffd8df",
  brandWash: "#fce1de",
  // Graphs (the web's series-1).
  series: "#2a78d6",
  // The card's hairline: the foreground at 10%.
  ring: "rgba(25, 15, 14, 0.1)",
  // The key highlight on a primary button's top edge.
  keyLight: "rgba(255, 255, 255, 0.22)",
};

const consoleDark: typeof daylight = {
  background: "#0f0808", // Ink
  card: "#181110", // Raised Ink
  muted: "#221918", // Muted Ink
  border: "#342c2b", // Rule
  foreground: "#f5f0f0", // Bone
  mutedForeground: "#9f9695", // Ash
  primary: "#f05653", // Signal Red
  primaryForeground: "#1b0a09", // On Signal
  destructive: "#fb718c",
  ok: "#66da85",
  warn: "#f7b83d",
  okWash: "#0d371a",
  warnWash: "#402900",
  dangerWash: "#540f22",
  brandWash: "#471715",
  series: "#3987e5",
  ring: "rgba(245, 240, 240, 0.1)",
  keyLight: "rgba(255, 255, 255, 0.22)",
};

export type Colors = typeof daylight;

export function useColors(): Colors {
  return useColorScheme() === "dark" ? consoleDark : daylight;
}

export const fonts = {
  sans: "HankenGrotesk_400Regular",
  sansMedium: "HankenGrotesk_500Medium",
  sansSemibold: "HankenGrotesk_600SemiBold",
  sansBold: "HankenGrotesk_700Bold",
  mono: "JetBrainsMono_400Regular",
  monoMedium: "JetBrainsMono_500Medium",
};

// The type scale, from DESIGN.md's hierarchy (a step up from the web's
// for a phone held at arm's length: body 15 rather than 14).
export const type = {
  headline: { fontFamily: fonts.sansBold, fontSize: 26, lineHeight: 32, letterSpacing: -0.65 },
  title: { fontFamily: fonts.sansMedium, fontSize: 17, lineHeight: 23 },
  body: { fontFamily: fonts.sans, fontSize: 15, lineHeight: 21 },
  meta: { fontFamily: fonts.sans, fontSize: 13, lineHeight: 18 },
  label: { fontFamily: fonts.mono, fontSize: 12, lineHeight: 16, letterSpacing: 0.3 },
  button: { fontFamily: fonts.monoMedium, fontSize: 13, letterSpacing: 0.65 },
  kicker: { fontFamily: fonts.mono, fontSize: 11, lineHeight: 14, letterSpacing: 2 },
} satisfies Record<string, TextStyle>;

// Corners: sharp, as on the web (--radius 0.25rem).
export const radius = { sm: 2.4, md: 3.4, lg: 4, xl: 5.6 };

// The gap between cards on a page, and a page's side padding.
export const space = { page: 16, stack: 20, card: 16 };
