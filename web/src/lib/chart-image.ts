// chart-image draws a TimeChart as a picture to save or share
// (docs/PANEL.md#charts): the chart with what it shows written on it (what's
// measured, what it's about, the time range and its time zone, each
// series' latest, low, average, and peak), so the picture makes sense on
// its own in a support thread or a Discord channel.

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
export function rangeLabel(from: number, to: number): string {
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

// Colors are the page's (its theme), read from CSS variables.
function cssColor(value: string): string {
  const probe = document.createElement("span");
  probe.style.color = value;
  probe.style.display = "none";
  document.body.appendChild(probe);
  const c = getComputedStyle(probe).color;
  probe.remove();
  return c || "#888";
}

const W = 1200;
const H = 675;
// The app's own font, as the page has it.
const family = () =>
  getComputedStyle(document.body).fontFamily || 'ui-sans-serif, system-ui, "Segoe UI", sans-serif';
const font = (size: number, weight = 400) => `${weight} ${size}px ${family()}`;

// render draws the picture at twice its size, for sharp text on screens
// that zoom.
export function render(c: ChartImage, at = new Date()): HTMLCanvasElement {
  const canvas = document.createElement("canvas");
  const scale = 2;
  canvas.width = W * scale;
  canvas.height = H * scale;
  const g = canvas.getContext("2d");
  if (!g) return canvas;
  g.scale(scale, scale);
  const bg = cssColor("var(--card)");
  const fg = cssColor("var(--foreground)");
  const muted = cssColor("var(--muted-foreground)");
  const border = cssColor("var(--border)");
  const brand = cssColor("var(--primary)");
  const colors = c.series.map((s) => cssColor(s.color));

  g.fillStyle = bg;
  g.fillRect(0, 0, W, H);
  g.textBaseline = "alphabetic";

  // Header: Raptor, then what the chart is.
  const left = 48;
  g.fillStyle = brand;
  g.font = font(13, 800);
  g.fillText("RAPTOR", left, 44);
  g.fillStyle = muted;
  g.font = font(13);
  g.textAlign = "right";
  g.fillText(`Saved ${at.toLocaleString()}`, W - left, 44);
  g.textAlign = "left";

  g.fillStyle = fg;
  g.font = font(30, 700);
  g.fillText(c.title, left, 88);
  let yText = 88;
  if (c.subject) {
    yText += 28;
    g.font = font(18, 500);
    g.fillText(c.subject, left, yText);
  }
  yText += 24;
  g.fillStyle = muted;
  g.font = font(14);
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  g.fillText(
    [c.note, `${rangeLabel(c.from, c.to)}, times in ${tz}`].filter(Boolean).join(" · "),
    left,
    yText,
  );

  // Legend: each series' numbers.
  yText += 34;
  let xLegend = left;
  c.series.forEach((s, i) => {
    const sum = summarize(c.data, i);
    g.fillStyle = colors[i] ?? fg;
    g.fillRect(xLegend, yText - 9, 18, 4);
    xLegend += 26;
    g.fillStyle = fg;
    g.font = font(15, 600);
    g.fillText(s.name, xLegend, yText);
    xLegend += g.measureText(s.name).width + 10;
    g.fillStyle = muted;
    g.font = font(14);
    const nums =
      sum.last === undefined
        ? "no data"
        : `now ${c.format(sum.last)} · low ${c.format(sum.min ?? 0)} · average ${c.format(sum.avg ?? 0)} · peak ${c.format(sum.max ?? 0)}`;
    g.fillText(nums, xLegend, yText);
    xLegend += g.measureText(nums).width + 32;
  });

  // The chart.
  const box = { l: left + 64, r: W - left, t: yText + 30, b: H - 72 };
  const t0 = c.from;
  const span = Math.max(1, c.to - c.from);
  const dataMax = Math.max(
    0,
    ...c.data.flatMap((p) => p.values.filter((v): v is number => v != null)),
  );
  const showLimit = c.limit !== undefined && c.limit > 0 && c.limit <= dataMax * 2.5;
  const yMax = niceMax(
    Math.max(dataMax * 1.05, showLimit ? (c.limit ?? 0) * 1.05 : 0, c.minMax ?? 1),
  );
  const x = (t: number) => box.l + ((t - t0) / span) * (box.r - box.l);
  const y = (v: number) => box.b - (v / yMax) * (box.b - box.t);

  g.lineWidth = 1;
  g.font = font(12);
  for (const f of [0, 0.25, 0.5, 0.75, 1]) {
    const v = f * yMax;
    g.strokeStyle = border;
    g.beginPath();
    g.moveTo(box.l, Math.round(y(v)) + 0.5);
    g.lineTo(box.r, Math.round(y(v)) + 0.5);
    g.stroke();
    g.fillStyle = muted;
    g.textAlign = "right";
    g.fillText(c.format(v), box.l - 10, y(v) + 4);
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
    g.fillText(label, x(t), box.b + 22);
  }
  g.textAlign = "left";

  if (showLimit && c.limit !== undefined) {
    g.strokeStyle = muted;
    g.setLineDash([6, 4]);
    g.beginPath();
    g.moveTo(box.l, y(c.limit));
    g.lineTo(box.r, y(c.limit));
    g.stroke();
    g.setLineDash([]);
    g.fillStyle = muted;
    g.textAlign = "right";
    g.fillText(`${c.limitLabel ?? "Limit"} ${c.format(c.limit)}`, box.r, y(c.limit) - 6);
    g.textAlign = "left";
  }

  g.lineWidth = 3;
  g.lineJoin = "round";
  g.lineCap = "round";
  c.series.forEach((_, i) => {
    g.strokeStyle = colors[i] ?? fg;
    g.fillStyle = colors[i] ?? fg;
    g.beginPath();
    let pen = false;
    let prev = 0;
    for (const p of c.data) {
      const v = p.values[i];
      if (v == null) {
        pen = false;
        continue;
      }
      if (pen && p.t - prev > c.gap) pen = false;
      if (pen) g.lineTo(x(p.t), y(v));
      else g.moveTo(x(p.t), y(v));
      pen = true;
      prev = p.t;
    }
    g.stroke();
    if (c.dots) {
      for (const p of c.data) {
        const v = p.values[i];
        if (v == null) continue;
        g.beginPath();
        g.arc(x(p.t), y(v), 3.5, 0, Math.PI * 2);
        g.fill();
      }
    }
  });
  if (c.data.length === 0) {
    g.fillStyle = muted;
    g.font = font(16);
    g.textAlign = "center";
    g.fillText("No data in this range", (box.l + box.r) / 2, (box.t + box.b) / 2);
    g.textAlign = "left";
  }

  g.fillStyle = muted;
  g.font = font(12);
  g.fillText("raptorpanel.net", left, H - 24);
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
