import { useQuery } from "@connectrpc/connect-query";
import { useQuery as useTanQuery } from "@tanstack/react-query";

import { useFilesApi } from "@/components/file-manager/data";
import { AddonService } from "@/gen/raptor/panel/v1/addon_pb";
import { addonNames } from "@/lib/addon-install";
import { onCurseForge } from "@/lib/eggs";
import { useServer } from "@/lib/org-data";

// What Wings' addons.list returns: the kind's folder and its files, each
// with the hashes the sources look them up by.
export type AddonListing = {
  kind: string;
  dir: string;
  files: {
    name: string;
    size: number;
    modified_at: number;
    sha512?: string;
    sha256?: string;
    sha1?: string;
    cf_fingerprint?: number;
    disabled?: boolean;
    // Thunderstore's: the version Wings unpacked into this folder.
    package?: string;
  }[];
};

export const addonListKey = (nodeId: string, serverId: string, kind: string) => [
  "addons",
  nodeId,
  serverId,
  kind,
];

// useInstalledAddons is an add-on kind's folder and what the sources know
// of its files (IdentifyAddons): the Add-ons tab and the Needs attention
// card share it, so the same keys serve both. staleTime: how long the
// caller can go on what it has (the card waits minutes; the tab doesn't).
export function useInstalledAddons(
  orgId: string,
  nodeId: string,
  serverId: string,
  kind: string,
  loaders: string[],
  version: string,
  opts: { enabled?: boolean; staleTime?: number } = {},
) {
  const api = useFilesApi(nodeId, serverId);
  const list = useTanQuery({
    queryKey: addonListKey(nodeId, serverId, kind),
    queryFn: () => api.run<AddonListing>("addons.list", { kind }),
    enabled: opts.enabled ?? true,
    staleTime: opts.staleTime,
  });
  const files = list.data?.files ?? [];
  const hashes = files.flatMap((f) => (f.sha512 ? [f.sha512] : []));
  const sha256 = Object.fromEntries(
    files.flatMap((f) => (f.sha512 && f.sha256 ? [[f.sha512, f.sha256]] : [])),
  );
  // uMod has no lookup by hash: its plugins are named by file, and
  // compared by SHA-1. Thunderstore's packages are named by version.
  const byName = kind === "umod" || kind === "factorio";
  const names = addonNames(kind, files);
  const sha1 = byName
    ? Object.fromEntries(files.flatMap((f) => (f.sha512 && f.sha1 ? [[f.sha512, f.sha1]] : [])))
    : {};
  // CurseForge (#412): on when the Panel has a key and the node can
  // download from it; its files are looked up by fingerprint.
  const { node } = useServer(orgId, nodeId, serverId);
  const panelSources = useQuery(
    AddonService.method.listAddonSources,
    { orgId },
    { staleTime: opts.staleTime },
  );
  const curseforge =
    !!panelSources.data?.sources.includes("curseforge") &&
    !!node?.capabilities.includes("addons-curseforge") &&
    onCurseForge(kind, loaders);
  const fingerprints = curseforge
    ? Object.fromEntries(
        files.flatMap((f) => (f.sha512 && f.cf_fingerprint ? [[f.sha512, f.cf_fingerprint]] : [])),
      )
    : {};
  const known = useQuery(
    AddonService.method.identifyAddons,
    { orgId, hashes, sha256, names, sha1, curseforge: fingerprints, loaders, gameVersion: version },
    { enabled: hashes.length > 0 && !panelSources.isPending, staleTime: opts.staleTime },
  );
  return { list, files, known, curseforge };
}
