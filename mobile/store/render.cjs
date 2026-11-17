// Renders the App Store art (README.md) from page.html with the web app's
// Playwright: the four screenshots at the 6.3" iPhone size, the product
// page header, and the search results asset, into out/.
//
//   node mobile/store/render.cjs [name]   (from the repo root; name: only those)
//
// CHROME can point at a Chromium if Playwright's own isn't installed.
const path = require("node:path");
const fs = require("node:fs");

const root = path.resolve(__dirname, "../..");
const { chromium } = require(path.join(root, "web/node_modules/playwright"));
const dir = __dirname;

// The brand's fonts, icon, and logo, from where the repo keeps them.
const assets = path.join(dir, "assets");
fs.mkdirSync(assets, { recursive: true });
const fonts = path.join(root, "site/node_modules/@fontsource-variable");
fs.copyFileSync(path.join(fonts, "hanken-grotesk/files/hanken-grotesk-latin-wght-normal.woff2"), path.join(assets, "hanken.woff2"));
fs.copyFileSync(path.join(fonts, "jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2"), path.join(assets, "mono.woff2"));
fs.copyFileSync(path.join(root, "mobile/assets/images/icon.png"), path.join(assets, "icon.png"));
const brand = fs.readFileSync(path.join(root, "mobile/src/lib/brand.ts"), "utf8");
const get = (n) => brand.match(new RegExp(`export const ${n} =\\s*"([^"]+)"`))[1];
fs.writeFileSync(path.join(assets, "brand.js"), `window.BRAND = ${JSON.stringify({ raptor: get("raptorPath"), wordmark: get("wordmarkPath") })};`);

// Screenshots are laid out at 1320 × 2868 (6.9") and zoomed to 1206 × 2622,
// the size App Store Connect asks for; it scales them for other iPhones.
const shot = { w: 1206, h: 2622, layout: [1320, 2868] };
const jobs = [
  ...["alerts", "servers", "server", "console"].map((s, i) => ({ name: `screenshot-${i + 1}-${s}`, q: `kind=shot&shot=${s}`, ...shot })),
  { name: "header", q: "kind=header", w: 3840, h: 1646 },
  { name: "search-results", q: "kind=search", w: 3840, h: 2560 },
];
const only = process.argv[2];

(async () => {
  const browser = await chromium.launch(process.env.CHROME ? { executablePath: process.env.CHROME } : {});
  fs.mkdirSync(path.join(dir, "out"), { recursive: true });
  for (const j of jobs) {
    if (only && !j.name.includes(only)) continue;
    const [lw, lh] = j.layout ?? [j.w, j.h];
    const page = await browser.newPage({ viewport: { width: j.w, height: j.h } });
    page.on("pageerror", (e) => console.error("page error:", e.message));
    await page.goto(`file://${dir}/page.html?${j.q}&w=${lw}&h=${lh}&zoom=${j.w / lw}`);
    await page.waitForSelector("body[data-ready='1']", { state: "attached" });
    await page.waitForLoadState("networkidle");
    await page.screenshot({ path: path.join(dir, "out", `${j.name}.png`) });
    await page.close();
    console.log("ok", j.name);
  }
  await browser.close();
})();
