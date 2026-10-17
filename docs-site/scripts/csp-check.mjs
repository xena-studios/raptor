// Loads the built site in Chromium with the headers it's served with in
// production (dist/_headers, filled in by the build) and fails on any
// Content Security Policy violation, page error, or failed request. It
// visits every page, and the 404 page, in both themes, then runs a search,
// which loads Pagefind's WebAssembly.
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
  ".mjs": "text/javascript",
  ".css": "text/css",
  ".svg": "image/svg+xml",
  ".json": "application/json",
  ".xml": "application/xml",
  ".wasm": "application/wasm",
  ".woff2": "font/woff2",
};

// serve answers like the Worker: a file, a folder's index.html, or 404.html.
function serve(hdrs) {
  return createServer(async (req, res) => {
    const path = decodeURIComponent(new URL(req.url, "http://x").pathname);
    const file = normalize(join(dist, path));
    if (!file.startsWith(dist)) {
      res.writeHead(400).end();
      return;
    }
    for (const [f, status] of [
      [file, 200],
      [join(file, "index.html"), 200],
      [join(dist, "404.html"), 404],
    ]) {
      try {
        const body = await readFile(f);
        res.writeHead(status, { ...hdrs, "Content-Type": types[extname(f)] ?? "application/octet-stream" });
        res.end(body);
        return;
      } catch {}
    }
  });
}

const missing = "/no-such-page/";
const pages = [missing];
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
      if (r.status() >= 400 && !r.url().endsWith(missing)) problems.push(`${r.url()}: ${r.status()}`);
    });
    for (const p of pages) await page.goto(base + p, { waitUntil: "networkidle" });
    if (scheme === "dark") {
      // Search: open it, type, and wait for a result.
      await page.goto(base, { waitUntil: "networkidle" });
      await page.click("button[data-open-modal]");
      await page.fill(".pagefind-ui__search-input", "passkey");
      await page.waitForSelector(".pagefind-ui__result", { timeout: 10_000 }).catch(() => {
        problems.push("search: no results for “passkey”");
      });
    }
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
console.log(`csp ok: ${pages.length} page(s), dark and light, and a search`);
