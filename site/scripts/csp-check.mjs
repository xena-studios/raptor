// Loads the built site in Chromium with the headers it's served with in
// production (dist/_headers, filled in by the build) and fails on any
// Content Security Policy violation, page error, or failed request.
//
// Usage: pnpm build && pnpm test:csp
import { readdir, readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

import { chromium } from "playwright";

const dist = fileURLToPath(new URL("../dist", import.meta.url));

// The headers for every path: the "/*" block of _headers.
async function headers() {
  const text = await readFile(join(dist, "_headers"), "utf8");
  const out = {};
  let inAll = false;
  for (const line of text.split("\n")) {
    if (!line.trim() || line.trim().startsWith("#")) continue;
    if (!/^\s/.test(line)) {
      inAll = line.trim() === "/*";
      continue;
    }
    const i = line.indexOf(":");
    if (inAll && i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  const csp = out["Content-Security-Policy"];
  if (!csp) throw new Error("_headers has no CSP for /*");
  if (csp.includes("__INLINE_SCRIPTS__")) throw new Error("_headers wasn't filled in: run pnpm build");
  return out;
}

const types = {
  ".html": "text/html",
  ".js": "text/javascript",
  ".css": "text/css",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".woff": "font/woff",
};

// serve answers like the Worker: a file, a folder's index.html, or 404.
function serve(hdrs) {
  return createServer(async (req, res) => {
    const path = decodeURIComponent(new URL(req.url, "http://x").pathname);
    const file = normalize(join(dist, path));
    if (!file.startsWith(dist)) {
      res.writeHead(400).end();
      return;
    }
    for (const f of [file, join(file, "index.html")]) {
      try {
        const body = await readFile(f);
        res.writeHead(200, { ...hdrs, "Content-Type": types[extname(f)] ?? "application/octet-stream" });
        res.end(body);
        return;
      } catch {}
    }
    res.writeHead(404, hdrs).end();
  });
}

const pages = [];
for (const f of await readdir(dist, { recursive: true })) {
  if (f.endsWith("index.html")) pages.push(`/${f.slice(0, -"index.html".length)}`);
}

const hdrs = await headers();
const server = serve(hdrs);
await new Promise((ok) => server.listen(0, "127.0.0.1", ok));
const base = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch();
const problems = [];
try {
  for (const scheme of ["dark", "light"]) {
    const page = await browser.newPage({ colorScheme: scheme });
    await page.addInitScript(() => {
      document.addEventListener("securitypolicyviolation", (e) => {
        console.error(`CSP violation: ${e.violatedDirective} blocked ${e.blockedURI || "inline"}`);
      });
    });
    page.on("console", (m) => {
      const t = m.text();
      if (t.includes("CSP violation") || t.includes("Content Security Policy")) problems.push(`${page.url()}: ${t}`);
    });
    page.on("pageerror", (e) => problems.push(`${page.url()}: ${e.message}`));
    page.on("response", (r) => {
      if (r.status() >= 400) problems.push(`${r.url()}: ${r.status()}`);
    });
    for (const p of pages) await page.goto(base + p, { waitUntil: "networkidle" });
    await page.close();
  }
} finally {
  await browser.close();
  server.close();
}
if (problems.length) {
  console.error(`Problems:\n  ${problems.join("\n  ")}`);
  process.exit(1);
}
console.log(`csp ok: ${pages.length} page(s), dark and light`);
