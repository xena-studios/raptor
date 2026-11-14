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

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

// shortEggId is an egg's ID for its page's URL: a UUID's last part. The
// built-in catalog's IDs (minecraft/paper) are left alone.
export function shortEggId(id: string): string {
  return uuid.test(id) ? shortId(id) : id;
}

// The params that have short forms.
const shortened = ["orgId", "nodeId", "serverId", "eggId"];

// canonical is the URL with its full org, node, server, and egg IDs made short,
// or undefined if it has none: old links keep working, and land on the
// short URL.
export function canonical(
  href: string,
  params: Record<string, string | undefined>,
): string | undefined {
  let out = href;
  for (const k of shortened) {
    const p = params[k];
    if (!p?.includes("-")) continue;
    if (k === "eggId" ? uuid.test(p) : true) out = out.replace(p, shortId(p));
  }
  return out === href ? undefined : out;
}
