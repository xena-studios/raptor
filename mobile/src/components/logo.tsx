import { View } from "react-native";
import Svg, { Path } from "react-native-svg";

import { clawPath, raptorPath, wordmarkPath } from "@/lib/brand";
import { useColors } from "@/lib/theme";

// Raptor's marks (docs/DESIGN.md#7-brand), as web/src/components/logo.tsx
// draws them: the running raptor in Signal Red beside the wordmark in the
// text colour, or the claw R alone where there's no room for both.

export function FullLogo({ height = 22 }: { height?: number }) {
  const c = useColors();
  return (
    <View
      accessible
      accessibilityRole="image"
      accessibilityLabel="Raptor"
      style={{ flexDirection: "row", alignItems: "center", gap: height * 0.45 }}
    >
      <Svg width={(height * 96) / 46} height={height} viewBox="0 3 96 46">
        <Path d={raptorPath} fillRule="evenodd" fill={c.primary} />
      </Svg>
      <Svg width={(height * 0.5 * 5356) / 711} height={height * 0.5} viewBox="82 -699 5356 711">
        <Path d={wordmarkPath} fill={c.foreground} />
      </Svg>
    </View>
  );
}

export function Logo({ size = 32 }: { size?: number }) {
  const c = useColors();
  return (
    <Svg width={size} height={size} viewBox="0 0 32 32" accessibilityLabel="Raptor">
      <Path
        d={clawPath}
        transform="translate(-0.9 28.3) scale(0.0335)"
        fillRule="evenodd"
        fill={c.primary}
      />
    </Svg>
  );
}
