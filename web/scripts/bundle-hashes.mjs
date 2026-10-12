// Writes the SHA-256 of every file in the built web app, in sha256sum's
// format, so anyone can check app.raptorpanel.net serves exactly what a
// release built (docs/DEPLOY.md#published-bundle-hashes). Each release
// carries it as web-bundle.sha256, listed in the signed checksums.txt.
//
// Usage: node scripts/bundle-hashes.mjs [dist] [out]  (out: stdout if unset)
import { createHash } from "node:crypto";
import { readdir, readFile, writeFile } from "node:fs/promises";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const dist = process.argv[2] ?? fileURLToPath(new URL("../dist", import.meta.url));

async function files(dir) {
  const out = [];
  for (const e of await readdir(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) out.push(...(await files(p)));
    else if (e.isFile()) out.push(p);
  }
  return out;
}

const lines = [];
for (const f of await files(dist)) {
  const name = relative(dist, f).split(sep).join("/");
  const sum = createHash("sha256")
    .update(await readFile(f))
    .digest("hex");
  lines.push(`${sum}  ${name}`);
}
// Sorted by name, byte order: the same build gives the same file.
lines.sort((a, b) => (a.slice(66) < b.slice(66) ? -1 : 1));
const text = `${lines.join("\n")}\n`;
if (process.argv[3]) await writeFile(process.argv[3], text);
else process.stdout.write(text);
