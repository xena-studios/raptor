// Short IDs (lib/ids.ts): an ID's last part. No imports, so it's tested
// on its own.

// shortId is an ID's last part; a short ID is its own.
export function shortId(id: string): string {
  return id.slice(id.lastIndexOf("-") + 1);
}

// sameId reports whether two IDs, full or short, are the same one.
export function sameId(a: string, b: string): boolean {
  return a === b || shortId(a) === shortId(b);
}

// The params that have short forms; others (an egg's ID) are left alone.
const shortened = ["orgId", "nodeId", "serverId"];

// canonical is the URL with its full org, node, and server IDs made short,
// or undefined if it has none: old links keep working, and land on the
// short URL.
export function canonical(
  href: string,
  params: Record<string, string | undefined>,
): string | undefined {
  let out = href;
  for (const k of shortened) {
    const p = params[k];
    if (p?.includes("-")) out = out.replace(p, shortId(p));
  }
  return out === href ? undefined : out;
}
