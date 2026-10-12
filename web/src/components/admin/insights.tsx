import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { CountBucket } from "@/gen/raptor/panel/v1/staff_pb";
import { cn } from "@/lib/utils";

// Pieces the admin overview's tabs share: a range picker in days, count
// breakdowns as bars, and daily counts as chart points.

export const dayRanges = [
  { days: 7, label: "7 days" },
  { days: 30, label: "30 days" },
  { days: 90, label: "90 days" },
  { days: 365, label: "1 year" },
];

export function DaysPicker({ value, onChange }: { value: number; onChange: (d: number) => void }) {
  return (
    <fieldset className="flex rounded-lg border p-0.5">
      <legend className="sr-only">Time range</legend>
      {dayRanges.map((r) => (
        <button
          key={r.days}
          type="button"
          aria-pressed={value === r.days}
          onClick={() => onChange(r.days)}
          className={cn(
            "rounded-md px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground",
            value === r.days && "bg-muted font-medium text-foreground",
          )}
        >
          {r.label}
        </button>
      ))}
    </fieldset>
  );
}

// dayPoints turns per-day counts ("YYYY-MM-DD") into chart points, at noon.
export function dayPoints(buckets: { name: string; count: number }[]) {
  return buckets.map((b) => ({ t: new Date(`${b.name}T12:00:00Z`).getTime(), values: [b.count] }));
}

// Breakdown is a count per name, as bars.
export function Breakdown({
  title,
  buckets,
  names,
  className,
}: {
  title: string;
  buckets: CountBucket[];
  // Names for people, for raw values like "github".
  names?: Record<string, string>;
  className?: string;
}) {
  const total = buckets.reduce((n, b) => n + b.count, 0);
  return (
    <Card size="sm" className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {buckets.length === 0 && <p className="text-sm text-muted-foreground">Nothing yet.</p>}
        {buckets.map((b) => (
          <div key={b.name} className="flex flex-col gap-1">
            <div className="flex justify-between gap-3 text-sm">
              <span className="truncate">{names?.[b.name] ?? b.name}</span>
              <span className="tabular-nums text-muted-foreground">
                {b.count}
                {total > 0 && (
                  <span className="ml-1.5 text-xs">{Math.round((b.count / total) * 100)}%</span>
                )}
              </span>
            </div>
            <div className="h-1 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-[var(--series-1)]"
                style={{ width: `${total ? (b.count / total) * 100 : 0}%` }}
              />
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

export const dollars = (cents: number | bigint) => {
  const c = Number(cents);
  return c % 100 === 0 ? `$${(c / 100).toLocaleString()}` : `$${(c / 100).toFixed(2)}`;
};

export const pct = (n: number, of: number) => (of > 0 ? `${Math.round((n / of) * 100)}%` : "—");

// methodNames are how people signed in and which accounts they linked.
export const methodNames: Record<string, string> = {
  email: "Email code or link",
  passkey: "Passkey",
  totp: "Authenticator app",
  recovery: "Recovery code",
  github: "GitHub",
  google: "Google",
  discord: "Discord",
};

// stateNames are server states.
export const stateNames: Record<string, string> = {
  running: "Running",
  offline: "Stopped",
  starting: "Starting",
  stopping: "Stopping",
  crashed: "Crashed",
  installing: "Installing",
  install_failed: "Install failed",
};
