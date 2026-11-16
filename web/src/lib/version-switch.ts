// The version switcher (#420): which game software a server runs, the
// variable that picks its version, and the ones to reset with it (a
// build number belongs to one version).

export type VersionSoftware = {
  source: string;
  label: string;
  variable: string;
  reset: string[];
};

const bySoftware: Record<string, VersionSoftware> = {
  paper: {
    source: "paper",
    label: "Paper",
    variable: "MINECRAFT_VERSION",
    reset: ["BUILD_NUMBER"],
  },
  purpur: {
    source: "purpur",
    label: "Purpur",
    variable: "MINECRAFT_VERSION",
    reset: ["BUILD_NUMBER"],
  },
  folia: {
    source: "folia",
    label: "Folia",
    variable: "MINECRAFT_VERSION",
    reset: ["BUILD_NUMBER"],
  },
  waterfall: {
    source: "waterfall",
    label: "Waterfall",
    variable: "MINECRAFT_VERSION",
    reset: ["BUILD_NUMBER"],
  },
  velocity: {
    source: "velocity",
    label: "Velocity",
    variable: "VELOCITY_VERSION",
    reset: ["BUILD_NUMBER"],
  },
  vanilla: { source: "vanilla", label: "Minecraft", variable: "VANILLA_VERSION", reset: [] },
  fabric: {
    source: "fabric",
    label: "Fabric",
    variable: "MC_VERSION",
    reset: ["LOADER_VERSION", "FABRIC_VERSION"],
  },
  quilt: { source: "quilt", label: "Quilt", variable: "MC_VERSION", reset: ["LOADER_VERSION"] },
};

// versionSoftware is what a server runs, by its library egg (its ID, like
// minecraft/paper, or its name, "Minecraft: Paper"), if the switcher knows
// it and the egg has the variable; undefined otherwise (forks like Leaf,
// whose versions differ, and renamed copies, keep Startup).
export function versionSoftware(
  eggSource: string,
  eggName: string,
  variables: string[],
): VersionSoftware | undefined {
  const fromId = /^minecraft\/([a-z]+)$/.exec(eggSource)?.[1];
  const fromName = /^Minecraft: ([A-Za-z]+)$/.exec(eggName)?.[1]?.toLowerCase();
  const sw = bySoftware[fromId ?? ""] ?? bySoftware[fromName ?? ""];
  return sw && variables.includes(sw.variable) ? sw : undefined;
}

// compareVersions orders versions like 1.21.4 and 26.3 by their numbers;
// anything after the numbers (-rc-1, -snapshot) sorts before the release.
export function compareVersions(a: string, b: string): number {
  const parts = (v: string) => {
    const m = /^(\d+(?:\.\d+)*)(.*)$/.exec(v);
    return { nums: (m?.[1] ?? "").split(".").map(Number), rest: m?.[2] ?? v };
  };
  const x = parts(a);
  const y = parts(b);
  for (let i = 0; i < Math.max(x.nums.length, y.nums.length); i++) {
    const d = (x.nums[i] ?? 0) - (y.nums[i] ?? 0);
    if (d !== 0) return d;
  }
  if (x.rest === y.rest) return 0;
  if (!x.rest) return 1;
  if (!y.rest) return -1;
  return x.rest.localeCompare(y.rest);
}
