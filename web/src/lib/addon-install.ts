// What the add-on pages send the node's addons.install, and how they name
// installed files to the Panel (docs/EGGS.md#add-ons).

export type InstallFile = {
  url: string;
  sha512?: string;
  sha256?: string;
  sha1?: string;
  filename: string;
  // A Thunderstore version ("Namespace-Name-1.2.3"): Thunderstore lists
  // no hashes, so the Panel downloads and hashes it first (HashAddonFile),
  // and that answer is what's installed.
  hashVersion?: string;
};

type FileLike = { url: string; sha512: string; sha256: string; sha1: string; filename: string };

// fileOf is what installs a version's file. Thunderstore's (#410) need the
// Panel's hash first: their url is Thunderstore's own link, which the node
// doesn't take.
export function fileOf(f: FileLike, source?: string, versionId?: string): InstallFile {
  if (source === "thunderstore" && versionId) {
    return { url: "", filename: f.filename, hashVersion: versionId };
  }
  return {
    url: f.url,
    sha512: f.sha512 || undefined,
    sha256: f.sha256 || undefined,
    sha1: f.sha1 || undefined,
    filename: f.filename,
  };
}

type Listed = { name: string; sha512?: string; sha1?: string; package?: string };

// addonNames are what the Panel names files by where a source has no
// lookup by hash, by SHA-512: uMod's and Factorio's file names, and the
// Thunderstore version Wings unpacked into each folder.
export function addonNames(kind: string, files: Listed[]): Record<string, string> {
  const name = (f: Listed) =>
    kind === "thunderstore" ? f.package : kind === "umod" || kind === "factorio" ? f.name : "";
  return Object.fromEntries(
    files.flatMap((f) => {
      const n = name(f);
      return f.sha512 && n ? [[f.sha512, n]] : [];
    }),
  );
}

// modLoaderOff reports whether a Thunderstore server runs the game without
// BepInEx (its egg's MOD_LOADER is vanilla), so mods wouldn't load.
export function modLoaderOff(kind: string, variables?: Record<string, string> | null): boolean {
  return kind === "thunderstore" && variables?.MOD_LOADER === "vanilla";
}
