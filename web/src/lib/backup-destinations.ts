// Backup destinations and where a server's backups go (docs/WINGS.md#backups),
// as Wings' backup.destinations and backup.policy return them. Kept free of
// imports so `node --test` can check it.

export type DestinationType = "local" | "folder" | "s3" | "azure" | "sftp" | "webdav";

export type Destination = {
  id: string;
  name: string;
  type: DestinationType;
  upload_limit?: number; // bytes per second
  folder?: { path: string };
  s3?: {
    endpoint: string;
    region?: string;
    bucket: string;
    prefix?: string;
    access_key: string;
    secret_key: string;
  };
  azure?: {
    container: string;
    prefix?: string;
    storage_account: string;
    storage_key?: string;
    sas_token?: string;
    storage_domain?: string;
  };
  sftp?: {
    host: string;
    port?: number;
    username: string;
    path: string;
    host_key: string;
    password?: string;
    use_node_key?: boolean;
    private_key?: string;
  };
  webdav?: { url: string; username?: string; password?: string };
  status?: {
    last_ok_at?: string;
    last_error?: string;
    last_error_at?: string;
    size: number; // -1: not measured yet
    size_at?: string;
  };
};

export const typeNames: Record<DestinationType, string> = {
  local: "This node's disk",
  folder: "Folder on the node",
  s3: "S3-compatible storage",
  azure: "Azure Blob Storage",
  sftp: "SFTP server",
  webdav: "WebDAV",
};

export const typeHints: Record<Exclude<DestinationType, "local">, string> = {
  s3: "Backblaze B2, Cloudflare R2, Wasabi, AWS, and any other S3-compatible bucket.",
  sftp: "A server of your own, over SSH. Its host key is checked every time.",
  folder: "A NAS or second disk mounted on the node.",
  webdav: "Nextcloud, ownCloud, or a NAS that speaks WebDAV.",
  azure: "A container in an Azure storage account.",
};

// S3 presets: the endpoint to fill in. {region} is replaced by the region.
export type S3Preset = {
  id: string;
  name: string;
  endpoint: string;
  regionHint?: string;
  keyNames: [string, string]; // what the provider calls the access key and secret
};

export const s3Presets: S3Preset[] = [
  {
    id: "b2",
    name: "Backblaze B2",
    endpoint: "s3.{region}.backblazeb2.com",
    regionHint: "us-west-004",
    keyNames: ["Key ID", "Application key"],
  },
  {
    id: "r2",
    name: "Cloudflare R2",
    endpoint: "{region}.r2.cloudflarestorage.com",
    regionHint: "your account ID",
    keyNames: ["Access key ID", "Secret access key"],
  },
  {
    id: "wasabi",
    name: "Wasabi",
    endpoint: "s3.{region}.wasabisys.com",
    regionHint: "us-east-1",
    keyNames: ["Access key", "Secret key"],
  },
  {
    id: "aws",
    name: "Amazon S3",
    endpoint: "s3.{region}.amazonaws.com",
    regionHint: "us-east-1",
    keyNames: ["Access key ID", "Secret access key"],
  },
  {
    id: "hetzner",
    name: "Hetzner Object Storage",
    endpoint: "{region}.your-objectstorage.com",
    regionHint: "fsn1",
    keyNames: ["Access key", "Secret key"],
  },
  {
    id: "ovh",
    name: "OVHcloud Object Storage",
    endpoint: "s3.{region}.io.cloud.ovh.net",
    regionHint: "gra",
    keyNames: ["Access key", "Secret key"],
  },
  {
    id: "gcs",
    name: "Google Cloud Storage",
    endpoint: "storage.googleapis.com",
    keyNames: ["HMAC access ID", "HMAC secret"],
  },
  {
    id: "custom",
    name: "Other (MinIO, …)",
    endpoint: "",
    keyNames: ["Access key", "Secret key"],
  },
];

export function presetEndpoint(p: S3Preset, region: string): string {
  return p.endpoint.replace("{region}", region.trim() || (p.regionHint ?? ""));
}

// presetFor guesses which preset an endpoint is, for editing.
export function presetFor(endpoint: string): S3Preset {
  const e = endpoint.replace(/^https?:\/\//, "");
  for (const p of s3Presets) {
    if (!p.endpoint) continue;
    const [before, after] = p.endpoint.split("{region}");
    if (after === undefined ? e === before : e.startsWith(before ?? "") && e.endsWith(after))
      return p;
  }
  return s3Presets[s3Presets.length - 1] as S3Preset;
}

// regionOf takes the region back out of a preset's endpoint.
export function regionOf(p: S3Preset, endpoint: string): string {
  const e = endpoint.replace(/^https?:\/\//, "");
  const [before, after] = p.endpoint.split("{region}");
  if (after === undefined) return "";
  return e.slice((before ?? "").length, e.length - after.length);
}

// --- where a server's backups go ---

export type Retention = {
  keep_last: number;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
};

export type Target = Retention & { destination_id: string };

export type Policy = { targets: Target[]; ignore?: string[] };

export type RetentionPreset = { id: string; name: string; keep: Retention; hint: string };

export const retentionPresets: RetentionPreset[] = [
  {
    id: "light",
    name: "Light",
    keep: { keep_last: 3, keep_daily: 0, keep_weekly: 0, keep_monthly: 0 },
    hint: "The last 3",
  },
  {
    id: "standard",
    name: "Standard",
    keep: { keep_last: 3, keep_daily: 7, keep_weekly: 4, keep_monthly: 0 },
    hint: "The last 3, a week of dailies, a month of weeklies",
  },
  {
    id: "long",
    name: "Long-term",
    keep: { keep_last: 3, keep_daily: 7, keep_weekly: 4, keep_monthly: 6 },
    hint: "Standard, plus one a month for 6 months",
  },
];

export function sameRetention(a: Retention, b: Retention): boolean {
  return (
    a.keep_last === b.keep_last &&
    a.keep_daily === b.keep_daily &&
    a.keep_weekly === b.keep_weekly &&
    a.keep_monthly === b.keep_monthly
  );
}

// presetOf is the preset a retention matches, or "custom".
export function presetOf(r: Retention): string {
  return retentionPresets.find((p) => sameRetention(p.keep, r))?.id ?? "custom";
}

// describeRetention: "the last 3, 7 daily, 4 weekly".
export function describeRetention(r: Retention): string {
  const parts = [
    r.keep_last && `the last ${r.keep_last}`,
    r.keep_daily && `${r.keep_daily} daily`,
    r.keep_weekly && `${r.keep_weekly} weekly`,
    r.keep_monthly && `${r.keep_monthly} monthly`,
  ].filter(Boolean);
  return parts.length ? `Keeps ${parts.join(", ")}` : "Keeps nothing";
}

// keepsLess is Wings' Policy.KeepsLess: a destination both policies use
// keeps fewer backups in next. Dropping a destination deletes nothing.
export function keepsLess(next: Policy, cur: Policy): boolean {
  return cur.targets.some((old) => {
    const t = next.targets.find((x) => x.destination_id === old.destination_id);
    return (
      !!t &&
      (t.keep_last < old.keep_last ||
        t.keep_daily < old.keep_daily ||
        t.keep_weekly < old.keep_weekly ||
        t.keep_monthly < old.keep_monthly)
    );
  });
}

// needsPasskey is when Wings wants the change signed: it lets retention
// delete more, or sends the server's backups somewhere new off its own
// disk (docs/SECURITY-MODEL.md#passkey-signed-commands).
export function needsPasskey(next: Policy, cur: Policy): boolean {
  const added = next.targets.some(
    (t) =>
      t.destination_id !== "local" &&
      !cur.targets.some((c) => c.destination_id === t.destination_id),
  );
  return added || keepsLess(next, cur);
}

// formatSpeed: an upload limit in bytes per second, as MB/s.
export function formatSpeed(bytesPerSecond: number | undefined): string {
  if (!bytesPerSecond) return "No limit";
  const mb = bytesPerSecond / 1_000_000;
  return `${mb >= 10 ? Math.round(mb) : mb.toFixed(1).replace(/\.0$/, "")} MB/s`;
}
