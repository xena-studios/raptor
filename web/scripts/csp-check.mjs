// Loads the built app in Chromium with the headers it's served with in
// production (public/_headers) and fails on any Content Security Policy
// violation, so a change that needs a looser policy is caught here, not
// by users (docs/DEPLOY.md#static-sites).
//
// Usage: pnpm build && pnpm test:csp
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

import { chromium } from "playwright";

const web = fileURLToPath(new URL("..", import.meta.url));
const dist = join(web, "dist");

// The headers for every path: the "/*" block of _headers.
async function headers() {
  const text = await readFile(join(web, "public", "_headers"), "utf8");
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
  if (!out["Content-Security-Policy"]) throw new Error("_headers has no CSP for /*");
  return out;
}

const types = {
  ".html": "text/html",
  ".js": "text/javascript",
  ".css": "text/css",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ico": "image/x-icon",
  ".json": "application/json",
  ".webmanifest": "application/manifest+json",
  ".woff2": "font/woff2",
  ".ttf": "font/ttf",
};

// serve answers like the Worker: files from dist, the app for anything
// else, and nothing for the API (it isn't here; pages show it's down).
function serve(hdrs) {
  return createServer(async (req, res) => {
    const path = decodeURIComponent(new URL(req.url, "http://x").pathname);
    if (path.startsWith("/api/")) {
      res.writeHead(503, hdrs).end();
      return;
    }
    let file = normalize(join(dist, path));
    if (!file.startsWith(dist)) {
      res.writeHead(400).end();
      return;
    }
    let body;
    try {
      body = await readFile(file);
    } catch {
      file = join(dist, "index.html");
      body = await readFile(file);
    }
    res.writeHead(200, {
      ...hdrs,
      "Content-Type": types[extname(file)] ?? "application/octet-stream",
    });
    res.end(body);
  });
}

// Pages to load: the ones that work without a session, and ones that
// send you to sign in.
const pages = ["/signin", "/signin/link", "/", "/orgs/x", "/settings", "/admin", "/invite?token=x"];

const hdrs = await headers();
const server = serve(hdrs);
await new Promise((ok) => server.listen(0, "127.0.0.1", ok));
const base = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch();
const violations = [];
try {
  const page = await browser.newPage();
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => {
      console.error(
        `CSP violation: ${e.violatedDirective} blocked ${e.blockedURI || "inline"} (${e.sourceFile}:${e.lineNumber})`,
      );
    });
  });
  page.on("console", (m) => {
    const t = m.text();
    if (t.includes("CSP violation") || t.includes("Content Security Policy"))
      violations.push(`${page.url()}: ${t}`);
  });
  page.on("pageerror", (e) => violations.push(`${page.url()}: ${e.message}`));
  for (const p of pages) {
    await page.goto(base + p, { waitUntil: "networkidle" });
    await page.waitForTimeout(500);
  }
} finally {
  await browser.close();
  server.close();
}
if (violations.length) {
  console.error(`CSP violations:\n  ${violations.join("\n  ")}`);
  process.exit(1);
}
console.log(`csp ok: ${pages.length} pages`);
