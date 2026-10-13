// describeCron says a schedule's cron expression the way people say it
// ("Every day at 4:00 AM"), for the common shapes; anything else comes
// back as undefined, and the expression itself is shown.

const days = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

const macros: Record<string, string> = {
  "@hourly": "Every hour",
  "@daily": "Every day at 12:00 AM",
  "@midnight": "Every day at 12:00 AM",
  "@weekly": "Every Sunday at 12:00 AM",
  "@monthly": "On the 1st of every month at 12:00 AM",
  "@yearly": "Every January 1st at 12:00 AM",
  "@annually": "Every January 1st at 12:00 AM",
};

const num = (s: string, max: number) =>
  /^\d+$/.test(s) && Number(s) <= max ? Number(s) : undefined;

function time(h: number, m: number): string {
  const hour = h % 12 === 0 ? 12 : h % 12;
  return `${hour}:${String(m).padStart(2, "0")} ${h < 12 ? "AM" : "PM"}`;
}

function ordinal(n: number): string {
  const s = n % 100 >= 11 && n % 100 <= 13 ? "th" : ({ 1: "st", 2: "nd", 3: "rd" }[n % 10] ?? "th");
  return `${n}${s}`;
}

export function describeCron(expr: string): string | undefined {
  const cron = expr.trim();
  if (macros[cron]) return macros[cron];
  const f = cron.split(/\s+/);
  if (f.length !== 5) return undefined;
  const [min, hour, dom, mon, dow] = f as [string, string, string, string, string];
  if (mon !== "*") return undefined;
  const m = num(min, 59);
  const h = num(hour, 23);

  // Every N minutes, every N hours.
  const everyMin = /^\*\/(\d+)$/.exec(min);
  if (everyMin && hour === "*" && dom === "*" && dow === "*") {
    return everyMin[1] === "1" ? "Every minute" : `Every ${everyMin[1]} minutes`;
  }
  if (min === "*" && hour === "*" && dom === "*" && dow === "*") return "Every minute";
  const everyHour = /^\*\/(\d+)$/.exec(hour);
  if (m !== undefined && everyHour && dom === "*" && dow === "*") {
    const n = everyHour[1];
    const at = m === 0 ? "" : `, at ${m} past`;
    return n === "1" ? `Every hour${at}` : `Every ${n} hours${at}`;
  }
  if (m !== undefined && hour === "*" && dom === "*" && dow === "*") {
    return m === 0 ? "Every hour" : `Every hour at ${m} past`;
  }
  if (m === undefined || h === undefined) return undefined;
  const at = time(h, m);
  if (dom === "*" && dow === "*") return `Every day at ${at}`;
  if (dom === "*") {
    if (dow === "1-5") return `Weekdays at ${at}`;
    if (dow === "0,6" || dow === "6,0") return `Weekends at ${at}`;
    const d = num(dow, 7);
    if (d !== undefined) return `Every ${days[d % 7]} at ${at}`;
    return undefined;
  }
  const d = num(dom, 31);
  if (d !== undefined && d > 0 && dow === "*")
    return `On the ${ordinal(d)} of every month at ${at}`;
  return undefined;
}
