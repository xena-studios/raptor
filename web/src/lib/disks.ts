import { useQuery } from "@tanstack/react-query";

import { formatBytes } from "@/lib/metrics";
import { commandClient } from "@/lib/transport";

// What Wings' node.disks returns (internal/wings/system/disks.go): the
// machine's disks, and where Raptor keeps data.

export type BlockDevice = {
  name: string;
  path: string;
  type: "disk" | "part";
  size: number;
  model?: string;
  serial?: string;
  fstype?: string;
  pttype?: string;
  label?: string;
  mountpoints?: string[];
  read_only: boolean;
  removable: boolean;
  rotational: boolean;
  children?: BlockDevice[];
  use: string;
  disk_id?: string;
};

export type DataDisk = {
  id: string; // "main" or a data disk's ID
  role: "servers" | "backups";
  mountpoint: string;
  device?: string;
  model?: string;
  total: number;
  free: number;
  servers: number;
  mounted: boolean;
  image?: boolean;
};

export type NodeDisks = { devices: BlockDevice[] | null; data: DataDisk[]; default: string };

// useNodeDisks is a node's disks (admins; nodes before data disks don't
// answer, and get no disk picker).
export function useNodeDisks(nodeId: string, enabled = true) {
  return useQuery({
    queryKey: ["node-disks", nodeId],
    queryFn: async () => {
      const res = await commandClient.execute({ nodeId, action: "node.disks", paramsJson: "{}" });
      return JSON.parse(res.resultJson) as NodeDisks;
    },
    enabled,
    retry: false,
  });
}

// diskName is how a data disk is called on the page.
export function diskName(d: DataDisk): string {
  if (d.id === "main") return d.image ? "Main volume (on the system disk)" : "Main volume";
  return [d.model, d.device ? d.device.replace("/dev/", "") : "", `disk ${d.id}`]
    .filter(Boolean)
    .join(" · ");
}

export function diskSpace(d: DataDisk): string {
  if (!d.mounted) return "Not mounted";
  return `${formatBytes(d.free)} free of ${formatBytes(d.total)}`;
}

// useLabels say what a block device is used for.
export const useLabels: Record<string, string> = {
  empty: "Empty",
  servers: "Server data",
  backups: "Backups",
  main: "Main server data",
  system: "System",
  mounted: "Mounted elsewhere",
  formatted: "Has data or a filesystem",
  partitions: "Partitioned",
  member: "RAID, LVM, or encrypted",
  too_small: "Too small",
  read_only: "Read-only",
};

// serverDisks are the disks a new server can go on.
export function serverDisks(d?: NodeDisks): DataDisk[] {
  return (d?.data ?? []).filter((x) => x.role === "servers" && x.mounted);
}
