// chart-image draws a TimeChart as a picture to save or share
// (docs/PANEL.md#charts): the chart with what it shows written on it (what's
// measured, what it's about, the time range and its time zone, each
// series' latest, low, average, and peak), so the picture makes sense on
// its own in a support thread or a Discord channel.

import { raptorBox, raptorPath, wordmarkBox, wordmarkPath } from "./brand.ts";

export type ImageSeries = { name: string; color: string };
export type ImagePoint = { t: number; values: (number | null)[] };

export type ChartImage = {
  title: string;
  // What it's about: a server and its node, a node, the platform.
  subject?: string;
  note?: string;
  data: ImagePoint[];
  series: ImageSeries[];
  format: (v: number) => string;
  limit?: number;
  limitLabel?: string;
  minMax?: number;
  from: number;
  to: number;
  gap: number;
  dots?: boolean;
};

// Summary is a series' numbers over the range.
export type Summary = { last?: number; min?: number; max?: number; avg?: number };

export function summarize(data: ImagePoint[], i: number): Summary {
  const vs = data.map((p) => p.values[i]).filter((v): v is number => v != null);
  if (vs.length === 0) return {};
  return {
    last: vs[vs.length - 1],
    min: Math.min(...vs),
    max: Math.max(...vs),
    avg: vs.reduce((a, b) => a + b, 0) / vs.length,
  };
}

// filename is the picture's name: raptor-smp-cpu-2026-10-16-1405.png.
export function filename(title: string, subject: string | undefined, at: Date): string {
  const slug = (s: string) =>
    s
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 40);
  const p = (n: number) => String(n).padStart(2, "0");
  const stamp = `${at.getFullYear()}-${p(at.getMonth() + 1)}-${p(at.getDate())}-${p(at.getHours())}${p(at.getMinutes())}`;
  return `${["raptor", slug(subject ?? ""), slug(title)].filter(Boolean).join("-")}-${stamp}.png`;
}

// rangeLabel says what the range is: "Oct 16, 13:05 – 14:05 (1 hour)".
function rangeLabel(from: number, to: number): string {
  const sameDay = new Date(from).toDateString() === new Date(to).toDateString();
  const date: Intl.DateTimeFormatOptions = { month: "short", day: "numeric" };
  const time: Intl.DateTimeFormatOptions = { hour: "2-digit", minute: "2-digit" };
  const a = new Date(from).toLocaleString(undefined, { ...date, ...time });
  const b = new Date(to).toLocaleString(undefined, sameDay ? time : { ...date, ...time });
  return `${a} – ${b} (${duration(to - from)})`;
}

export function duration(ms: number): string {
  const h = Math.round(ms / 3600_000);
  if (h < 1) return `${Math.max(1, Math.round(ms / 60_000))} minutes`;
  if (h < 48) return h === 1 ? "1 hour" : `${h} hours`;
  const d = Math.round(h / 24);
  return `${d} days`;
}

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const exp = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * exp >= v) return m * exp;
  return 10 * exp;
}

// gridSteps is how many even steps the axis is cut into so each lands on
// a round value: 5 for a maximum of 2.5, 5, or 10 (×10ⁿ), 4 for 1 or 2.
export function gridSteps(max: number): number {
  const m = max / 10 ** Math.floor(Math.log10(max));
  return m === 1 || m === 2 ? 4 : 5;
}

// Colors are the page's (its theme), read from CSS variables. The page has
// them in oklch; a canvas reads them back as plain RGB, so they can be
// made see-through.
function cssColor(value: string): string {
  const probe = document.createElement("span");
  probe.style.color = value;
  probe.style.display = "none";
  document.body.appendChild(probe);
  const c = getComputedStyle(probe).color || "#888";
  probe.remove();
  const g = document.createElement("canvas").getContext("2d", { willReadFrequently: true });
  if (!g) return c;
  g.fillStyle = c;
  g.fillRect(0, 0, 1, 1);
  const [r = 0, gr = 0, b = 0] = g.getImageData(0, 0, 1, 1).data;
  return `rgb(${r}, ${gr}, ${b})`;
}

const W = 1200;
const H = 675;
// The app's own fonts, as the page has them.
const family = () =>
  getComputedStyle(document.body).fontFamily || 'ui-sans-serif, system-ui, "Segoe UI", sans-serif';
const mono = () =>
  getComputedStyle(document.documentElement).getPropertyValue("--font-mono").trim() ||
  "ui-monospace, monospace";
const font = (size: number, weight = 400) => `${weight} ${size}px ${family()}`;
const monoFont = (size: number, weight = 400) => `${weight} ${size}px ${mono()}`;

// withAlpha is a CSS color made see-through ("rgb(…)" as the page gives it).
function withAlpha(color: string, a: number): string {
  const m = color.match(/rgba?\(([^)]+)\)/);
  if (!m?.[1]) return color;
  const [r, g, b] = m[1].split(/[ ,/]+/).filter(Boolean);
  return `rgba(${r}, ${g}, ${b}, ${a})`;
}

// logo draws the running raptor and the wordmark, h tall, from (x, y) at
// the top left; it returns how wide it is.
function logo(
  g: CanvasRenderingContext2D,
  x: number,
  y: number,
  h: number,
  red: string,
  ink: string,
) {
  const sr = h / raptorBox.h;
  g.save();
  g.translate(x - raptorBox.x * sr, y - raptorBox.y * sr);
  g.scale(sr, sr);
  g.fillStyle = red;
  g.fill(new Path2D(raptorPath), "evenodd");
  g.restore();
  const rw = raptorBox.w * sr;
  const wh = h * 0.42;
  const sw = wh / wordmarkBox.h;
  const wx = x + rw + h * 0.28;
  g.save();
  g.translate(wx - wordmarkBox.x * sw, y + (h - wh) / 2 - wordmarkBox.y * sw);
  g.scale(sw, sw);
  g.fillStyle = ink;
  g.fill(new Path2D(wordmarkPath));
  g.restore();
  return wx - x + wordmarkBox.w * sw;
}

// render draws the picture at twice its size, for sharp text on screens
// that zoom. It's the app's own surface edge to edge (docs/DESIGN.md: the
// Console): the logo, what the chart is, a readout of each line's numbers
// between hairlines, the chart, and when and where it was saved. No boxes:
// grouping comes from rules, data is mono, colour only marks the lines.
function render(c: ChartImage, at = new Date()): HTMLCanvasElement {
  const canvas = document.createElement("canvas");
  const scale = 2;
  canvas.width = W * scale;
  canvas.height = H * scale;
  const g = canvas.getContext("2d");
  if (!g) return canvas;
  g.scale(scale, scale);
  const surface = cssColor("var(--card)");
  const fg = cssColor("var(--foreground)");
  const muted = cssColor("var(--muted-foreground)");
  const rule = cssColor("var(--border)");
  const brand = cssColor("var(--primary)");
  const colors = c.series.map((s) => cssColor(s.color));
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const pad = 40;
  const left = pad;
  const right = W - pad;
  const hairline = (y: number) => {
    g.strokeStyle = rule;
    g.lineWidth = 1;
    g.beginPath();
    g.moveTo(0, Math.round(y) + 0.5);
    g.lineTo(W, Math.round(y) + 0.5);
    g.stroke();
  };

  g.fillStyle = surface;
  g.fillRect(0, 0, W, H);
  g.textBaseline = "alphabetic";

  // The logo, and where it's from.
  logo(g, left, 22, 22, brand, fg);
  g.fillStyle = muted;
  g.font = monoFont(12);
  g.textAlign = "right";
  g.fillText("raptorpanel.net", right, 38);
  g.textAlign = "left";
  hairline(64);

  // What it's about, and what it is.
  let y = 100;
  if (c.subject) {
    g.fillStyle = muted;
    g.font = monoFont(12);
    g.fillText(`// ${c.subject.toLowerCase()}`, left, y);
    y += 34;
  } else {
    y += 12;
  }
  g.fillStyle = fg;
  g.font = font(30, 700);
  g.fillText(c.title, left, y);
  y += 26;
  g.fillStyle = muted;
  g.font = font(14);
  g.fillText([c.note, rangeLabel(c.from, c.to)].filter(Boolean).join(" · "), left, y);
  y += 22;
  hairline(y);

  // The readout: one row per line, its swatch and name, then now, low,
  // average, and peak, each a mono label over its value.
  const rowH = 52;
  const cols = [
    { key: "now", label: "NOW" },
    { key: "min", label: "LOW" },
    { key: "avg", label: "AVERAGE" },
    { key: "max", label: "PEAK" },
  ] as const;
  const nameW = 220;
  const colW = (right - left - nameW) / cols.length;
  c.series.forEach((s, i) => {
    const sum = summarize(c.data, i);
    const top = y + i * rowH;
    if (i > 0) {
      g.strokeStyle = rule;
      g.beginPath();
      g.moveTo(left, Math.round(top) + 0.5);
      g.lineTo(right, Math.round(top) + 0.5);
      g.stroke();
    }
    g.fillStyle = colors[i] ?? fg;
    g.fillRect(left, top + 21, 10, 10);
    g.fillStyle = fg;
    g.font = font(15, 600);
    g.fillText(s.name, left + 20, top + 31);
    cols.forEach((col, k) => {
      const cx = left + nameW + k * colW;
      g.strokeStyle = rule;
      g.beginPath();
      g.moveTo(Math.round(cx) + 0.5, top + 12);
      g.lineTo(Math.round(cx) + 0.5, top + rowH - 12);
      g.stroke();
      g.fillStyle = muted;
      g.font = monoFont(10, 500);
      g.fillText(col.label, cx + 16, top + 22);
      const v = col.key === "now" ? sum.last : sum[col.key];
      g.fillStyle = fg;
      g.font = monoFont(col.key === "now" ? 17 : 15, col.key === "now" ? 600 : 500);
      g.fillText(v === undefined ? "—" : c.format(v), cx + 16, top + 41);
    });
  });
  y += Math.max(1, c.series.length) * rowH;
  hairline(y);

  // The chart.
  g.font = monoFont(11);
  const t0 = c.from;
  const span = Math.max(1, c.to - c.from);
  const dataMax = Math.max(
    0,
    ...c.data.flatMap((p) => p.values.filter((v): v is number => v != null)),
  );
  const showLimit = c.limit !== undefined && c.limit > 0 && c.limit <= dataMax * 2.5;
  const yMax = niceMax(
    Math.max(dataMax * 1.08, showLimit ? (c.limit ?? 0) * 1.08 : 0, c.minMax ?? 1),
  );
  const steps = gridSteps(yMax);
  const fractions = Array.from({ length: steps + 1 }, (_, k) => k / steps);
  const yLabelW = Math.max(...fractions.map((f) => g.measureText(c.format(f * yMax)).width));
  const footer = H - 44;
  const box = { l: left + yLabelW + 14, r: right - 70, t: y + 30, b: footer - 40 };
  const x = (t: number) => box.l + ((t - t0) / span) * (box.r - box.l);
  const yv = (v: number) => box.b - (v / yMax) * (box.b - box.t);

  // The grid, quiet: dotted guides and a solid baseline.
  g.lineWidth = 1;
  for (const f of fractions) {
    const v = f * yMax;
    const yy = Math.round(yv(v)) + 0.5;
    g.strokeStyle = f === 0 ? rule : withAlpha(rule, 0.7);
    g.setLineDash(f === 0 ? [] : [1, 4]);
    g.beginPath();
    g.moveTo(box.l, yy);
    g.lineTo(box.r, yy);
    g.stroke();
    g.setLineDash([]);
    g.fillStyle = muted;
    g.textAlign = "right";
    g.fillText(c.format(v), box.l - 12, yy + 4);
  }
  const ticks = 5;
  for (let i = 0; i < ticks; i++) {
    const t = t0 + (i / (ticks - 1)) * span;
    g.textAlign = i === 0 ? "left" : i === ticks - 1 ? "right" : "center";
    const d = new Date(t);
    const label =
      span > 36 * 3600_000
        ? d.toLocaleString(undefined, { weekday: "short", day: "numeric", hour: "numeric" })
        : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
    g.fillStyle = muted;
    g.fillText(label, x(t), box.b + 22);
  }
  g.textAlign = "left";

  // The limit: dashed, named at its end.
  if (showLimit && c.limit !== undefined) {
    const ly = Math.round(yv(c.limit)) + 0.5;
    g.strokeStyle = withAlpha(fg, 0.5);
    g.setLineDash([6, 5]);
    g.beginPath();
    g.moveTo(box.l, ly);
    g.lineTo(box.r, ly);
    g.stroke();
    g.setLineDash([]);
    g.fillStyle = muted;
    g.font = monoFont(11, 500);
    g.fillText(
      `${(c.limitLabel ?? "Limit").toUpperCase()} ${c.format(c.limit)}`,
      box.r + 10,
      ly + 4,
    );
  }

  // Each line, split where it stopped: a soft fill under it, the line, and
  // a ringed dot with its value at its latest point.
  const runs = (i: number) => {
    const out: { t: number; v: number }[][] = [];
    let cur: { t: number; v: number }[] = [];
    let prev = 0;
    for (const p of c.data) {
      const v = p.values[i];
      if (v == null || (cur.length > 0 && p.t - prev > c.gap)) {
        if (cur.length) out.push(cur);
        cur = [];
      }
      if (v != null) {
        cur.push({ t: p.t, v });
        prev = p.t;
      }
    }
    if (cur.length) out.push(cur);
    return out;
  };
  const ends: { x: number; y: number; color: string; text: string }[] = [];
  c.series.forEach((_, i) => {
    const color = colors[i] ?? fg;
    const rs = runs(i);
    const fill = g.createLinearGradient(0, box.t, 0, box.b);
    fill.addColorStop(0, withAlpha(color, c.series.length > 1 ? 0.12 : 0.22));
    fill.addColorStop(1, withAlpha(color, 0));
    for (const r of rs) {
      if (r.length < 2) continue;
      g.beginPath();
      g.moveTo(x(r[0]?.t ?? 0), box.b);
      for (const p of r) g.lineTo(x(p.t), yv(p.v));
      g.lineTo(x(r[r.length - 1]?.t ?? 0), box.b);
      g.closePath();
      g.fillStyle = fill;
      g.fill();
    }
    g.strokeStyle = color;
    g.lineWidth = 2;
    g.lineJoin = "round";
    g.lineCap = "round";
    for (const r of rs) {
      g.beginPath();
      for (const [k, p] of r.entries()) {
        if (k) g.lineTo(x(p.t), yv(p.v));
        else g.moveTo(x(p.t), yv(p.v));
      }
      g.stroke();
      if (c.dots || r.length === 1) {
        g.fillStyle = color;
        for (const p of r) {
          g.beginPath();
          g.arc(x(p.t), yv(p.v), 3, 0, Math.PI * 2);
          g.fill();
        }
      }
    }
    const last = rs[rs.length - 1]?.at(-1);
    if (last) ends.push({ x: x(last.t), y: yv(last.v), color, text: c.format(last.v) });
  });
  ends.sort((a, b) => a.y - b.y);
  let floor = -Infinity;
  for (const e of ends) {
    g.beginPath();
    g.arc(e.x, e.y, 4.5, 0, Math.PI * 2);
    g.fillStyle = e.color;
    g.fill();
    g.lineWidth = 2;
    g.strokeStyle = surface;
    g.stroke();
    const ly = Math.max(e.y + 4, floor + 16);
    floor = ly;
    g.fillStyle = fg;
    g.font = monoFont(12, 600);
    g.textAlign = "left";
    g.fillText(e.text, box.r + 10, ly);
  }
  if (ends.length === 0) {
    g.fillStyle = muted;
    g.font = font(16);
    g.textAlign = "center";
    g.fillText("No data in this range", (box.l + box.r) / 2, (box.t + box.b) / 2);
    g.textAlign = "left";
  }

  // When it was saved, and in which time zone.
  hairline(footer);
  g.fillStyle = muted;
  g.font = monoFont(11);
  g.textAlign = "left";
  g.fillText(`Times in ${tz}`, left, footer + 27);
  g.textAlign = "right";
  g.fillText(
    `Saved ${at.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })}`,
    right,
    footer + 27,
  );
  g.textAlign = "left";
  return canvas;
}

function blob(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) =>
    canvas.toBlob(
      (b) => (b ? resolve(b) : reject(new Error("the picture couldn't be made"))),
      "image/png",
    ),
  );
}

// download saves the picture.
export async function download(c: ChartImage): Promise<void> {
  const at = new Date();
  const b = await blob(render(c, at));
  const url = URL.createObjectURL(b);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename(c.title, c.subject, at);
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

// copy puts the picture on the clipboard, to paste into a chat.
export async function copy(c: ChartImage): Promise<void> {
  // Safari wants the promise handed over at once, inside the click.
  await navigator.clipboard.write([new ClipboardItem({ "image/png": blob(render(c)) })]);
}
