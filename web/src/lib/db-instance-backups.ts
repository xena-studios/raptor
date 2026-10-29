// A database instance's own backups (docs/DATABASES.md#backups): Wings'
// node.db.backups and node.db.backup.*.

// Instances are backed up on their own, and deleting one takes a final
// backup first.
export const dbInstanceBackupsCapability = "db-instance-backups";

// InstanceBackup is one of an instance's backups (Wings' backup.Backup).
export type InstanceBackup = {
  id: string;
  destination_id: string;
  kind: "manual" | "scheduled" | "safety" | "final" | "recovered";
  status: "pending" | "running" | "ok" | "failed";
  size: number;
  files: number;
  warning?: string;
  error?: string;
  created_at: string;
  finished_at?: string;
  expires_at?: string;
  // The databases it holds, by their names on the instance.
  databases?: string[];
};

// InstanceBackupSettings is an instance's backup settings (Wings'
// backup.InstancePolicy).
export type InstanceBackupSettings = {
  enabled: boolean;
  at: string; // HH:MM, node time
  keep_last: number;
  destination_id: string;
};

export type InstanceBackups = {
  backups: InstanceBackup[];
  settings: InstanceBackupSettings;
  destinations: { id: string; name: string; type: string }[];
};

export const instanceBackupsKey = (nodeId: string, id: string) => [
  "db-instance-backups",
  nodeId,
  id,
];

// The node's own disk and Raptor Backup Storage aren't somewhere new.
const notOffsite = ["local", "raptor"];

// settingsNeedPasskey: keeping fewer lets retention delete backups, and a
// new destination copies the databases off the node, so Wings wants
// those signed (node.db.backup.settings).
export function settingsNeedPasskey(
  next: InstanceBackupSettings,
  cur: InstanceBackupSettings,
): boolean {
  const keepsLess = next.destination_id === cur.destination_id && next.keep_last < cur.keep_last;
  const moved =
    next.destination_id !== cur.destination_id && !notOffsite.includes(next.destination_id);
  return keepsLess || moved;
}

// inProgress reports whether any backup is still being taken.
export function inProgress(backups: InstanceBackup[]): boolean {
  return backups.some((b) => b.status === "pending" || b.status === "running");
}

export const kindNames: Record<InstanceBackup["kind"], string> = {
  manual: "Manual",
  scheduled: "Daily",
  safety: "Safety",
  final: "Final",
  recovered: "Recovered",
};
