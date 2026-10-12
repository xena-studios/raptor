// Checks a deployed web app against a release's web-bundle.sha256: every
// file it lists is fetched from the site and must hash the same
// (docs/DEPLOY.md#published-bundle-hashes). Files the site serves as
// configuration rather than content (_headers) are skipped.
//
// Usage: node scripts/verify-deployed.mjs web-bundle.sha256 [https://app.raptorpanel.net]
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";

const [manifest, origin = "https://app.raptorpanel.net"] = process.argv.slice(2);
if (!manifest) {
  console.error("usage: verify-deployed.mjs <web-bundle.sha256> [origin]");
  process.exit(2);
}
const config = new Set(["_headers", "_redirects"]);
const bad = [];
let checked = 0;
for (const line of (await readFile(manifest, "utf8")).split("\n")) {
  const m = line.match(/^([0-9a-f]{64}) {2}(.+)$/);
  if (!m || config.has(m[2])) continue;
  const res = await fetch(new URL(m[2], `${origin}/`));
  if (!res.ok) {
    bad.push(`${m[2]}: ${res.status}`);
    continue;
  }
  const sum = createHash("sha256")
    .update(Buffer.from(await res.arrayBuffer()))
    .digest("hex");
  if (sum !== m[1]) bad.push(`${m[2]}: served ${sum}, release has ${m[1]}`);
  checked++;
}
if (bad.length) {
  console.error(`${origin} doesn't match:\n  ${bad.join("\n  ")}`);
  process.exit(1);
}
console.log(`${origin} matches: ${checked} files`);
