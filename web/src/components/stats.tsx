import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Card, CardContent } from "@/components/ui/card";
import { type MetricRange, metricRanges } from "@/lib/metrics";
import { cn } from "@/lib/utils";

// The overview pages' building blocks: a stat with its trend, a usage bar,
// a time range picker, and a titled graph.

// StatCard is one number now: its label, the value, a line under it, and
// optionally how full it is (a bar) or its last hour (a sparkline).
export function StatCard({
  icon: Icon,
  label,
  value,
  sub,
  fraction,
  spark,
  className,
}: {
  icon?: LucideIcon;
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  fraction?: number;
  spark?: (number | null)[];
  className?: string;
}) {
  return (
    <Card size="sm" className={cn("relative", className)}>
      <CardContent className="flex flex-col gap-1.5">
        <p className="flex items-center gap-1.5 font-mono text-[0.7rem] tracking-wider text-muted-foreground uppercase">
          {Icon && <Icon className="size-3.5" />} {label}
        </p>
        <div className="flex items-end justify-between gap-3">
          <p className="shrink-0 font-mono text-xl font-medium whitespace-nowrap tabular-nums">
            {value}
          </p>
          {spark && spark.length > 1 && <Sparkline values={spark} />}
        </div>
        {fraction !== undefined && <UsageBar fraction={fraction} />}
        {sub && <p className="truncate text-xs text-muted-foreground">{sub}</p>}
      </CardContent>
    </Card>
  );
}

// UsageBar is how full something is, amber past 75% and red past 90%.
export function UsageBar({ fraction, className }: { fraction: number; className?: string }) {
  const f = Math.min(1, Math.max(0, fraction));
  return (
    <div className={cn("h-1 overflow-hidden rounded-full bg-muted", className)}>
      <div
        className={cn(
          "h-full rounded-full transition-[width]",
          f > 0.9 ? "bg-destructive" : f > 0.75 ? "bg-warn" : "bg-[var(--series-1)]",
        )}
        style={{ width: `${f * 100}%` }}
      />
    </div>
  );
}

// Sparkline is a trend with no axes: its shape, not its numbers. Gaps (null)
// break the line.
function Sparkline({
  values,
  width = 72,
  height = 24,
  color = "var(--series-1)",
}: {
  values: (number | null)[];
  width?: number;
  height?: number;
  color?: string;
}) {
  const nums = values.filter((v): v is number => v !== null);
  const top = Math.max(...nums, 0) || 1;
  const step = width / Math.max(1, values.length - 1);
  let d = "";
  let pen = false;
  values.forEach((v, i) => {
    if (v === null) {
      pen = false;
      return;
    }
    const y = height - 2 - (v / top) * (height - 4);
    d += `${pen ? "L" : "M"}${(i * step).toFixed(1)},${y.toFixed(1)}`;
    pen = true;
  });
  return (
    // It gives way to the value: narrower, never pushing it out of view.
    <svg
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      height={height}
      aria-hidden
      className="min-w-0 overflow-visible"
      style={{ width, maxWidth: "100%" }}
    >
      <path
        d={d}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        vectorEffect="non-scaling-stroke"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function RangePicker({
  value,
  onChange,
}: {
  value: MetricRange;
  onChange: (r: MetricRange) => void;
}) {
  return (
    <fieldset className="flex rounded-lg border p-0.5">
      <legend className="sr-only">Time range</legend>
      {metricRanges.map((r) => (
        <button
          key={r.value}
          type="button"
          aria-pressed={value === r.value}
          onClick={() => onChange(r.value)}
          className={cn(
            "rounded-md px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground",
            value === r.value && "bg-muted font-medium text-foreground",
          )}
        >
          {r.label}
        </button>
      ))}
    </fieldset>
  );
}

export function Graph({
  title,
  note,
  children,
}: {
  title: string;
  note?: string;
  children: ReactNode;
}) {
  return (
    <section className="flex min-w-0 flex-col gap-2">
      <h3 className="text-sm font-medium">
        {title}
        {note && <span className="ml-2 text-xs font-normal text-muted-foreground">{note}</span>}
      </h3>
      {children}
    </section>
  );
}

// lastPoints is the last n points of a series, for a sparkline.
export function lastPoints<T>(points: T[] | undefined, n = 30): T[] {
  return (points ?? []).slice(-n);
}
