// How a schedule's run reads in the Runs list (docs/PANEL.md#schedule-runs).
// The node's own words for a failed step aren't shown: the known ones
// become a sentence, the rest a plain "it failed". Kept free of imports so
// `node --test` can check it.

export type RunStep = { type: string; ok: boolean; error: string };

const stepNames: Record<string, string> = {
  command: "Send a command",
  power: "Power",
  wait: "Wait",
  backup: "Back up",
};

export function stepName(type: string): string {
  return stepNames[type] ?? "A step";
}

// Errors Wings words for the schedule's steps, by a piece of their text.
const known: [string, string][] = [
  ["isn't running", "the server wasn't running"],
  ["is installing", "the server was installing"],
  ["isn't installed", "the server isn't installed"],
  ["being restored", "a backup was being restored"],
  ["being deleted", "the server was being deleted"],
  ["disk limit", "the server was over its disk limit"],
  ["too many commands", "too many commands were sent at once"],
  ["longer than 4 KiB", "the command is too long"],
  ["line breaks", "the command has a line break in it"],
  ["backups aren't available", "backups aren't set up on this node"],
  ["context canceled", "the run was stopped"],
];

const generic: Record<string, string> = {
  command: "the command couldn't be sent",
  power: "the power action didn't work",
  wait: "the wait was cut short",
  backup: "the backup failed",
};

// stepFailure is why a step failed, as a sentence's end.
export function stepFailure(step: RunStep): string {
  const e = step.error.toLowerCase();
  for (const [needle, words] of known) if (e.includes(needle.toLowerCase())) return words;
  return generic[step.type] ?? "it failed";
}

export function skipText(reason: string): string {
  switch (reason) {
    case "offline":
      return "the server was offline";
    case "still_running":
      return "the last run was still going";
    case "missed":
      return "the node was down when it was due";
  }
  return "it couldn't run";
}

export function reasonText(reason: string): string {
  switch (reason) {
    case "manual":
      return "Run now";
    case "missed":
      return "late, after the node was down";
  }
  return "on schedule";
}

// duration is how long a run took, short: "4s", "2m 5s", "1h 3m".
export function duration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return s % 60 ? `${m}m ${s % 60}s` : `${m}m`;
  const h = Math.floor(m / 60);
  return m % 60 ? `${h}h ${m % 60}m` : `${h}h`;
}
