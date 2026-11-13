// Fills in dist/_headers' CSP with the SHA-256 of every inline script the
// build emitted (Starlight's theme and a few small helpers), so those run
// and nothing else inline does. Runs after `astro build`.
import { createHash } from "node:crypto";
import { readdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const dist = fileURLToPath(new URL("../dist", import.meta.url));
const hashes = new Set();
for (const f of await readdir(dist, { recursive: true })) {
  if (!f.endsWith(".html")) continue;
  const html = await readFile(join(dist, f), "utf8");
  // Tag and attribute names are case-insensitive in HTML, and an end tag
  // may carry spaces or attributes (`</script >`): a script this missed
  // would be refused by the CSP, one it misread would get the wrong hash.
  for (const [, attrs, body] of html.matchAll(/<script(\s[^>]*)?>([\s\S]*?)<\/script(?:[\s/][^>]*)?>/gi)) {
    if (/\ssrc\s*=/i.test(attrs ?? "") || !body) continue;
    hashes.add(`'sha256-${createHash("sha256").update(body).digest("base64")}'`);
  }
}
const file = join(dist, "_headers");
const text = await readFile(file, "utf8");
const placeholder = " __INLINE_SCRIPTS__";
if (!text.includes(placeholder)) throw new Error("_headers has no inline-script placeholder");
const filled = [...hashes].sort().map((h) => ` ${h}`).join("");
await writeFile(file, text.replace(placeholder, filled));
console.log(`_headers: ${hashes.size} inline script hashes`);
