import { StyleSheet, View } from "react-native";
import Svg, { Polyline } from "react-native-svg";

import { Text } from "@/components/ui";
import { radius, useColors } from "@/lib/theme";

// Readouts are a server's numbers as the web's stat cards show them: a mono
// label, the value in the readout style (mono, tabular), a line under it,
// and for some a sparkline of the last hour.

export type Readout = {
  label: string;
  value: string;
  sub?: string;
  spark?: (number | null)[];
};

function Spark({ data, color }: { data: (number | null)[]; color: string }) {
  const vals = data.filter((v): v is number => v !== null);
  if (vals.length < 2) return null;
  const max = Math.max(...vals, 1);
  const w = 100;
  const h = 24;
  const step = w / Math.max(data.length - 1, 1);
  const points = data
    .map((v, i) =>
      v === null ? null : `${(i * step).toFixed(1)},${(h - (v / max) * (h - 2) - 1).toFixed(1)}`,
    )
    .filter(Boolean)
    .join(" ");
  return (
    <Svg width="100%" height={h} viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none">
      <Polyline
        points={points}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        vectorEffect="non-scaling-stroke"
      />
    </Svg>
  );
}

export function Readouts({ items }: { items: Readout[] }) {
  const c = useColors();
  return (
    <View style={styles.grid}>
      {items.map((r) => (
        <View
          key={r.label}
          style={[styles.cell, { backgroundColor: c.card, borderColor: c.ring }]}
          accessible
          accessibilityLabel={`${r.label}: ${r.value}${r.sub ? `, ${r.sub}` : ""}`}
        >
          <Text variant="label" mono ink="muted" style={{ textTransform: "uppercase" }}>
            {r.label}
          </Text>
          <Text mono weight="medium" style={{ fontSize: 20, lineHeight: 26 }}>
            {r.value}
          </Text>
          {r.sub ? (
            <Text variant="meta" ink="muted" numberOfLines={1}>
              {r.sub}
            </Text>
          ) : null}
          {r.spark ? <Spark data={r.spark} color={c.series} /> : null}
        </View>
      ))}
    </View>
  );
}

const styles = StyleSheet.create({
  grid: { flexDirection: "row", flexWrap: "wrap", gap: 10 },
  cell: {
    flexGrow: 1,
    flexBasis: "45%",
    gap: 2,
    padding: 12,
    borderWidth: 1,
    borderRadius: radius.xl,
  },
});
