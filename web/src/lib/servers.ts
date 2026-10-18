// Creating servers and showing their state. Kept free of imports so
// `node --test` can check it.

// What the create form needs of a catalog egg (CatalogEgg).
type EggSummary = {
  id: string;
  name: string;
  category: string;
  certified: boolean;
  arch: string[];
};

// eggsFor lists the eggs a node can run, certified ones first, then by name.
// An unknown architecture (a node that hasn't reported it, or an imported
// egg that doesn't say) doesn't rule anything out.
export function eggsFor<E extends EggSummary>(eggs: E[], arch: string): E[] {
  return eggs
    .filter((e) => !arch || e.arch.length === 0 || e.arch.includes(arch))
    .sort((a, b) => Number(b.certified) - Number(a.certified) || a.name.localeCompare(b.name));
}

// Each kind of game's usual port: players type it least when it's the one
// they expect.
const usualPorts: Record<string, number> = { minecraft: 25565, steam: 27015 };

// suggestPort picks the game's usual port, or the next one up that no
// server on the node uses.
export function suggestPort(category: string, used: number[]): number {
  let port = usualPorts[category] ?? 25565;
  while (used.includes(port)) port++;
  return port;
}

// Memory to start with, by kind of game, and never more than three quarters
// of the node's.
const usualMemoryMiB: Record<string, number> = { minecraft: 2048, steam: 4096 };

export function suggestMemoryMiB(category: string, nodeMemoryBytes: number): number {
  const want = usualMemoryMiB[category] ?? 1024;
  if (!nodeMemoryBytes) return want;
  const cap = Math.floor((nodeMemoryBytes / 2 ** 20) * 0.75);
  return Math.max(256, Math.min(want, Math.floor(cap / 256) * 256));
}

type Variable = {
  name: string;
  env: string;
  default: string;
  userEditable: boolean;
  rules: string[];
};

// missingRequired lists the editable variables that must be filled and
// aren't (tokens, passwords: eggs leave their defaults empty). Wings checks
// every rule; this only catches what users most often skip.
export function missingRequired(vars: Variable[], values: Record<string, string>): Variable[] {
  return vars.filter(
    (v) => v.userEditable && v.rules.includes("required") && !(values[v.env] ?? v.default).trim(),
  );
}

// base64 encodes bytes the way Go's encoding/json does []byte (standard
// alphabet, padded), in chunks: eggs are up to ~100 KB, too many arguments
// for one String.fromCharCode call.
export function base64(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(s);
}

type CreateForm = {
  name: string;
  eggId: string;
  egg: Uint8Array;
  image: string;
  variables: Record<string, string>;
  port: number;
  memoryMiB: number;
  diskMiB: number;
  acceptEula: boolean;
  // The disk its files go on; undefined is the node's default (and all
  // there is on nodes without data disks).
  disk?: string;
};

// createParams is server.create's params (Wings' actions.CreateParams):
// what the user's passkey signs. Only changed variables are sent; Wings
// fills in the egg's defaults.
export function createParams(f: CreateForm, defaults: Record<string, string>) {
  const variables: Record<string, string> = {};
  for (const [k, v] of Object.entries(f.variables)) {
    if (v !== defaults[k]) variables[k] = v;
  }
  return {
    name: f.name.trim(),
    egg: base64(f.egg),
    egg_source: f.eggId,
    image: f.image,
    variables,
    limits: { memory_mib: f.memoryMiB, disk_mib: f.diskMiB },
    allocations: [{ ip: "0.0.0.0", port: f.port, primary: true }],
    start_after_install: true,
    accept_eula: f.acceptEula || undefined,
    disk: f.disk || undefined,
  };
}

// nodeStatus is a node's connection as a status, so nodes and servers show
// theirs the same way.
export function nodeStatus(n: { connected: boolean }): ServerStatus {
  return n.connected ? { label: "Connected", tone: "live" } : { label: "Offline", tone: "stopped" };
}

// A server's state as the page shows it: live from a connected node,
// pending while it installs, failed with the reason, or stale when its node
// is offline (the last thing the node reported).
export type ServerStatus = {
  label: string;
  tone: "live" | "pending" | "failed" | "stale" | "stopped";
  detail?: string;
};

export function serverStatus(
  s: { state: string; installState: string; installError: string },
  nodeConnected: boolean,
): ServerStatus {
  let st: ServerStatus;
  if (s.installState === "failed" || s.state === "install_failed") {
    st = { label: "Install failed", tone: "failed", detail: s.installError || undefined };
  } else if (
    s.state === "installing" ||
    s.installState === "pending" ||
    s.installState === "installing"
  ) {
    st = { label: "Installing", tone: "pending" };
  } else {
    st = (
      {
        running: { label: "Running", tone: "live" },
        starting: { label: "Starting", tone: "pending" },
        stopping: { label: "Stopping", tone: "pending" },
        crashed: { label: "Crashed", tone: "failed" },
        offline: { label: "Stopped", tone: "stopped" },
        restoring: { label: "Restoring", tone: "pending" },
        deleting: { label: "Deleting", tone: "pending" },
        moving: { label: "Moving", tone: "pending" },
      } as Record<string, ServerStatus>
    )[s.state] ?? { label: s.state || "Unknown", tone: "stopped" };
  }
  if (!nodeConnected) {
    return {
      label: st.label,
      tone: "stale",
      detail: "The node is offline: this is its last report.",
    };
  }
  return st;
}

// A server's settings as the mirror has them (GetServer's config_json).
export type ServerConfig = {
  image: string;
  startup: string;
  variables: Record<string, string> | null;
  limits: { memory_mib: number; disk_mib: number; [k: string]: unknown };
  settings: Record<string, unknown>;
  host_network: boolean;
  allocations: { ip: string; port: number; primary?: boolean }[];
  egg_hash?: string;
  disk?: string; // a data disk's ID; absent for the main volume
  egg?: {
    images: { name: string; ref: string }[] | null;
    // As Wings reports them (snake_case JSON).
    variables:
      | {
          name: string;
          description?: string;
          env: string;
          default: string;
          user_viewable?: boolean;
          user_editable?: boolean;
          rules?: string[];
        }[]
      | null;
    features?: string[];
    // The egg's add-ons (docs/EGGS.md#add-ons), by kind.
    addons?: Record<string, { loaders: string[]; dir: string; version?: string }>;
    // Where the game takes Steam Workshop items (docs/EGGS.md#steam-workshop).
    workshop?: Workshop;
    // How the game answers player queries ("minecraft": Java edition).
    players?: string;
  };
};

export type Workshop = {
  app_id: number;
  variable: string;
  // The variable takes one collection's ID; otherwise items', comma-separated.
  collection?: boolean;
  // What makes the game download a change.
  apply: "restart" | "reinstall";
};

// workshopIds are the IDs in a Workshop variable's value.
export function workshopIds(value: string): string[] {
  return value
    .split(/[\s,]+/)
    .filter((id) => /^[0-9]{1,20}$/.test(id))
    .filter((id, i, all) => all.indexOf(id) === i);
}

// workshopRef is the ID in a Workshop link
// (steamcommunity.com/sharedfiles/filedetails/?id=123) or a bare ID.
export function workshopRef(input: string): string | undefined {
  const s = input.trim();
  if (/^[0-9]{1,20}$/.test(s)) return s;
  try {
    const u = new URL(s);
    const id = u.searchParams.get("id") ?? "";
    if (/(^|\.)steamcommunity\.com$/.test(u.hostname) && /^[0-9]{1,20}$/.test(id)) return id;
  } catch {
    // not a link
  }
  return undefined;
}

// gameVersion is the server's game version for its add-ons, from the
// variable the egg names: "1.21.1", or "" when it's "latest" or unset,
// which means any.
export function gameVersion(cfg: ServerConfig, variable?: string): string {
  if (!variable) return "";
  const v =
    cfg.variables?.[variable] ?? cfg.egg?.variables?.find((x) => x.env === variable)?.default ?? "";
  return /^\d+\.\d+(\.\d+)?$/.test(v.trim()) ? v.trim() : "";
}

type SettingsChange = {
  name: string;
  image: string;
  variables: Record<string, string>;
  memoryMiB: number;
  diskMiB: number;
  port: number;
  // Left out, they stay as they are.
  swapMiB?: number;
  cpuPercent?: number;
  behavior?: Partial<Behavior>;
};

// Behavior is what Wings does when a server crashes or is stopped (Wings'
// server.Settings).
export type Behavior = {
  // Restart it after a crash, with a growing wait between tries.
  crash_auto_restart: boolean;
  // Exit code 0 means it stopped, not that it crashed.
  clean_exit_is_stop: boolean;
  // How long a stop waits before killing it ("60s"; 1s to 10m).
  stop_timeout: string;
};

// behaviorOf is a server's behavior, with Wings' defaults for what isn't set.
export function behaviorOf(cfg: ServerConfig): Behavior {
  const s = cfg.settings ?? {};
  return {
    crash_auto_restart: s.crash_auto_restart !== false,
    clean_exit_is_stop: s.clean_exit_is_stop === true,
    stop_timeout: typeof s.stop_timeout === "string" ? s.stop_timeout : "1m0s",
  };
}

// seconds is a Go duration ("1m30s", "45s") in whole seconds.
export function seconds(d: string): number {
  let total = 0;
  for (const [, n, unit] of d.matchAll(/(\d+(?:\.\d+)?)(h|ms|m|s)/g)) {
    const v = Number(n);
    total += unit === "h" ? v * 3600 : unit === "m" ? v * 60 : unit === "s" ? v : v / 1000;
  }
  return Math.round(total);
}

// updateParams is server.update's params: the server's whole config with
// the changes, and no egg, which tells Wings to keep the server's own (the
// Panel doesn't hold egg files). Fields this page doesn't edit go back as
// they were.
export function updateParams(cfg: ServerConfig, c: SettingsChange) {
  const { egg: _egg, egg_hash: _hash, ...rest } = cfg;
  const allocations = cfg.allocations.map((a) => (a.primary ? { ...a, port: c.port } : a));
  if (!allocations.some((a) => a.primary))
    allocations.unshift({ ip: "0.0.0.0", port: c.port, primary: true });
  return {
    ...rest,
    name: c.name.trim(),
    image: c.image,
    variables: { ...(cfg.variables ?? {}), ...c.variables },
    limits: {
      ...cfg.limits,
      memory_mib: c.memoryMiB,
      disk_mib: c.diskMiB,
      ...(c.swapMiB !== undefined && { swap_mib: c.swapMiB }),
      ...(c.cpuPercent !== undefined && { cpu_percent: c.cpuPercent }),
    },
    settings: { ...cfg.settings, ...c.behavior },
    allocations,
  };
}

// changesCode: a new image or startup command changes what code runs, so
// the user's passkey must sign it (Wings: Manager.ChangesCode).
export function changesCode(cfg: ServerConfig, c: SettingsChange): boolean {
  return c.image !== cfg.image;
}

// changeEggParams is server.update's params to change what a server is:
// the new egg, with its own runtime and startup command (Wings picks the
// defaults), and a reinstall straight after, wiping the files first if
// asked. Settings both eggs have (the same variable) keep their values;
// the rest start at the new egg's defaults.
export function changeEggParams(
  cfg: ServerConfig,
  name: string,
  egg: { id: string; file: Uint8Array },
  wipe: boolean,
) {
  const { egg: _egg, egg_hash: _hash, ...rest } = cfg;
  let envs: string[] = [];
  try {
    const parsed = JSON.parse(new TextDecoder().decode(egg.file)) as {
      variables?: { env_variable?: string }[];
    };
    envs = (parsed.variables ?? []).flatMap((v) => (v.env_variable ? [v.env_variable] : []));
  } catch {
    // A YAML egg: every setting starts at its default.
  }
  const variables: Record<string, string> = {};
  for (const [k, v] of Object.entries(cfg.variables ?? {})) {
    if (envs.includes(k)) variables[k] = v;
  }
  return {
    ...rest,
    name,
    egg: base64(egg.file),
    egg_source: egg.id,
    image: "",
    startup: "",
    variables,
    reinstall: { wipe },
  };
}
