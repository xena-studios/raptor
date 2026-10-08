// Creating servers and showing their state. Kept free of imports so
// `node --test` can check it.

// What the create form needs of a catalog egg (CatalogEgg).
export type EggSummary = {
  id: string;
  name: string;
  category: string;
  certified: boolean;
  arch: string[];
};

// eggsFor lists the eggs a node can run, certified ones first, then by name.
// An unknown architecture (a node that hasn't reported it) shows them all.
export function eggsFor<E extends EggSummary>(eggs: E[], arch: string): E[] {
  return eggs
    .filter((e) => !arch || e.arch.includes(arch))
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

export type Variable = {
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

export type CreateForm = {
  name: string;
  eggId: string;
  egg: Uint8Array;
  image: string;
  variables: Record<string, string>;
  port: number;
  memoryMiB: number;
  diskMiB: number;
  acceptEula: boolean;
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
  };
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
  };
};

export type SettingsChange = {
  name: string;
  image: string;
  variables: Record<string, string>;
  memoryMiB: number;
  diskMiB: number;
  port: number;
};

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
    limits: { ...cfg.limits, memory_mib: c.memoryMiB, disk_mib: c.diskMiB },
    allocations,
  };
}

// changesCode: a new image or startup command changes what code runs, so
// the user's passkey must sign it (Wings: Manager.ChangesCode).
export function changesCode(cfg: ServerConfig, c: SettingsChange): boolean {
  return c.image !== cfg.image;
}
