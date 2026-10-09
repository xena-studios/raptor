import { type PointerEvent, useLayoutEffect, useRef, useState } from "react";

// TimeChart is a line chart over time: thin 2px lines, a hairline grid, a
// dashed-free limit line, gaps where there's no data, and a crosshair with
// a tooltip on hover. One series needs no legend (the card names it); two
// get one, with their latest values.

export type Series = { name: string; color: string };
export type TimePoint = { t: number; values: (number | null)[] };

const pad = { top: 8, right: 8, bottom: 22, left: 44 };

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const exp = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * exp >= v) return m * exp;
  return 10 * exp;
}

function timeLabel(t: number, span: number): string {
  const d = new Date(t);
  return span > 36 * 3600_000
    ? d.toLocaleDateString(undefined, { weekday: "short", hour: "numeric" })
    : d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
}

export function TimeChart({
  data,
  series,
  format,
  limit,
  limitLabel = "Limit",
  minMax = 1,
  height = 150,
  from,
  to,
}: {
  data: TimePoint[];
  series: Series[];
  format: (v: number) => string;
  limit?: number;
  limitLabel?: string;
  // The smallest top of the scale, so a flat line near zero doesn't get
  // axis labels like 0.5 bytes.
  minMax?: number;
  height?: number;
  // The time range shown (defaults to the data's).
  from?: number;
  to?: number;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const [hover, setHover] = useState<number>();
  useLayoutEffect(() => {
    if (!box.current) return;
    const ro = new ResizeObserver(([e]) => e && setWidth(e.contentRect.width));
    ro.observe(box.current);
    return () => ro.disconnect();
  }, []);

  const t0 = from ?? data[0]?.t ?? 0;
  const t1 = to ?? data[data.length - 1]?.t ?? 1;
  const span = Math.max(1, t1 - t0);
  const dataMax = Math.max(
    0,
    ...data.flatMap((p) => p.values.filter((v): v is number => v !== null)),
  );
  // The limit shows only when it's in reach, so a 16 GB limit doesn't
  // flatten a 300 MB line.
  const showLimit = limit !== undefined && limit > 0 && limit <= dataMax * 2.5;
  const yMax = niceMax(Math.max(dataMax * 1.05, showLimit ? limit * 1.05 : 0, minMax));
  const w = Math.max(0, width - pad.left - pad.right);
  const h = height - pad.top - pad.bottom;
  const x = (t: number) => pad.left + ((t - t0) / span) * w;
  const y = (v: number) => pad.top + h - (v / yMax) * h;

  // The lines: a gap of over 3 minutes between points is a stretch with no
  // data, so the line breaks there.
  const paths = series.map((_, i) => {
    let d = "";
    let pen = false;
    let prevT = 0;
    for (const p of data) {
      const v = p.values[i];
      if (v === null || v === undefined) {
        pen = false;
        continue;
      }
      if (pen && p.t - prevT > 180_000) pen = false;
      d += `${pen ? "L" : "M"}${x(p.t).toFixed(1)},${y(v).toFixed(1)}`;
      pen = true;
      prevT = p.t;
    }
    return d;
  });

  function onMove(e: PointerEvent<SVGSVGElement>) {
    if (data.length === 0) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const t = t0 + ((e.clientX - rect.left - pad.left) / w) * span;
    let best = 0;
    for (let i = 1; i < data.length; i++) {
      if (Math.abs((data[i]?.t ?? 0) - t) < Math.abs((data[best]?.t ?? 0) - t)) best = i;
    }
    setHover(best);
  }

  const hp = hover !== undefined ? data[hover] : undefined;
  const ticks = [0, 0.5, 1].map((f) => f * yMax);
  const xTicks = [0, 1 / 3, 2 / 3, 1].map((f) => t0 + f * span);

  return (
    <div className="flex flex-col gap-2">
      {series.length > 1 && (
        <div className="flex flex-wrap gap-4 text-xs text-muted-foreground">
          {series.map((s, i) => {
            const last = [...data].reverse().find((p) => p.values[i] != null)?.values[i];
            return (
              <span key={s.name} className="flex items-center gap-1.5">
                <span className="h-0.5 w-3 rounded-full" style={{ background: s.color }} />
                {s.name}
                {last != null && (
                  <span className="text-foreground tabular-nums">{format(last)}</span>
                )}
              </span>
            );
          })}
        </div>
      )}
      <div ref={box} className="relative" style={{ height }}>
        {width > 0 && (
          <svg
            width={width}
            height={height}
            role="img"
            aria-label={`${series.map((s) => s.name).join(" and ")} over time`}
            onPointerMove={onMove}
            onPointerLeave={() => setHover(undefined)}
            className="overflow-visible"
          >
            {ticks.map((v) => (
              <g key={v}>
                <line
                  x1={pad.left}
                  x2={pad.left + w}
                  y1={y(v)}
                  y2={y(v)}
                  className="stroke-border"
                  strokeWidth={1}
                />
                <text
                  x={pad.left - 6}
                  y={y(v)}
                  dy="0.32em"
                  textAnchor="end"
                  className="fill-muted-foreground text-[10px] tabular-nums"
                >
                  {format(v)}
                </text>
              </g>
            ))}
            {xTicks.map((t, i) => (
              <text
                key={t}
                x={x(t)}
                y={height - 6}
                textAnchor={i === 0 ? "start" : i === xTicks.length - 1 ? "end" : "middle"}
                className="fill-muted-foreground text-[10px]"
              >
                {timeLabel(t, span)}
              </text>
            ))}
            {showLimit && (
              <g>
                <line
                  x1={pad.left}
                  x2={pad.left + w}
                  y1={y(limit)}
                  y2={y(limit)}
                  className="stroke-muted-foreground/60"
                  strokeWidth={1}
                />
                <text
                  x={pad.left + w}
                  y={y(limit) - 4}
                  textAnchor="end"
                  className="fill-muted-foreground text-[10px]"
                >
                  {limitLabel} {format(limit)}
                </text>
              </g>
            )}
            {paths.map((d, i) => (
              <path
                // biome-ignore lint/suspicious/noArrayIndexKey: series are fixed per chart
                key={i}
                d={d}
                fill="none"
                stroke={series[i]?.color}
                strokeWidth={2}
                strokeLinejoin="round"
                strokeLinecap="round"
              />
            ))}
            {hp && (
              <g>
                <line
                  x1={x(hp.t)}
                  x2={x(hp.t)}
                  y1={pad.top}
                  y2={pad.top + h}
                  className="stroke-muted-foreground/50"
                  strokeWidth={1}
                />
                {hp.values.map(
                  (v, i) =>
                    v != null && (
                      <circle
                        // biome-ignore lint/suspicious/noArrayIndexKey: series are fixed per chart
                        key={i}
                        cx={x(hp.t)}
                        cy={y(v)}
                        r={4}
                        fill={series[i]?.color}
                        className="stroke-card"
                        strokeWidth={2}
                      />
                    ),
                )}
              </g>
            )}
          </svg>
        )}
        {hp && (
          <div
            className="pointer-events-none absolute top-0 z-10 rounded-md border bg-popover px-2.5 py-1.5 text-xs shadow-md"
            style={{
              left: Math.min(Math.max(x(hp.t) + 10, 0), Math.max(0, width - 160)),
            }}
          >
            <p className="mb-1 text-muted-foreground">{new Date(hp.t).toLocaleString()}</p>
            {series.map((s, i) => (
              <p key={s.name} className="flex items-center gap-1.5">
                <span className="h-0.5 w-3 rounded-full" style={{ background: s.color }} />
                <span className="text-muted-foreground">{s.name}</span>
                <span className="ml-auto pl-3 tabular-nums">
                  {hp.values[i] == null ? "—" : format(hp.values[i] as number)}
                </span>
              </p>
            ))}
          </div>
        )}
        {width > 0 && data.length === 0 && (
          <p className="absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">
            No data yet
          </p>
        )}
      </div>
    </div>
  );
}
