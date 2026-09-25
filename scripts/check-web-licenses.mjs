// Fails if any production web dependency uses a license that isn't AGPL-3.0-compatible.
// Usage: pnpm --dir web licenses list --json --prod | node scripts/check-web-licenses.mjs
const allowed = new Set([
  "0BSD", "Apache-2.0", "BlueOak-1.0.0", "BSD-2-Clause", "BSD-3-Clause", "CC-BY-4.0",
  "CC0-1.0", "ISC", "MIT", "MPL-2.0", "OFL-1.1", "Python-2.0", "Unlicense",
]);

// Handles simple SPDX expressions: "A OR B" needs one allowed, "A AND B" needs all.
function ok(expr) {
  const e = expr.replace(/[()]/g, "").trim();
  if (e.includes(" OR ")) return e.split(" OR ").some(ok);
  if (e.includes(" AND ")) return e.split(" AND ").every(ok);
  return allowed.has(e);
}

let input = "";
for await (const chunk of process.stdin) input += chunk;

const bad = [];
for (const [license, pkgs] of Object.entries(JSON.parse(input))) {
  if (!ok(license)) bad.push(...pkgs.map((p) => `${p.name}@${p.versions.join(",")}: ${license}`));
}

if (bad.length) {
  console.error("Disallowed licenses:\n  " + bad.join("\n  "));
  process.exit(1);
}
console.log("web licenses ok");
