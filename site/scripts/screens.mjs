// Takes the app screenshots the landing page shows (src/assets/screens),
// in both themes, from a dev Panel with a real node (docs/ARCHITECTURE.md).
// It needs a signed-in browser state: node scripts/screens.mjs <state.json>
// <org short ID> <server short ID>. Servers that aren't the subject (test
// leftovers) can be hidden from the sidebar with HIDE=regex; nothing else
// on the screen is changed.
import { chromium } from "playwright";

const [state, org, server] = process.argv.slice(2);
if (!state || !org || !server) {
  console.error("usage: node scripts/screens.mjs <state.json> <org short ID> <server short ID>");
  process.exit(2);
}
const base = process.env.APP ?? "http://localhost:5173";
const hide = process.env.HIDE ? new RegExp(process.env.HIDE) : null;
// Each shot's page and, for the tour, the part of the window it shows:
// the page beside the sidebar, so the text stays readable at the size the
// landing page shows it. The hero shows the whole window.
// CSS pixels at 1440 wide: the sidebar ends at 256.
const inset = (height) => ({ x: 258, y: 52, width: 1182, height });
const shots = {
  server: [`/orgs/${org}/servers/${server}`],
  console: [`/orgs/${org}/servers/${server}`, inset(640)],
  files: [`/orgs/${org}/servers/${server}/files`, inset(600)],
  backups: [`/orgs/${org}/servers/${server}/backups`, inset(500)],
  activity: [`/orgs/${org}/servers/${server}/activity`, inset(600)],
};
const out = new URL("../src/assets/screens/", import.meta.url).pathname;
const browser = await chromium.launch();
for (const theme of ["dark", "light"]) {
  const ctx = await browser.newContext({
    storageState: state, viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2, colorScheme: theme,
  });
  await ctx.addInitScript((t) => localStorage.setItem("raptor.theme", t), theme);
  const page = await ctx.newPage();
  for (const [name, [path, clip]] of Object.entries(shots)) {
    await page.goto(base + path, { waitUntil: "networkidle" });
    await page.waitForTimeout(3000);
    if (hide) {
      await page.evaluate((src) => {
        const re = new RegExp(src);
        for (const el of document.querySelectorAll('[data-slot="sidebar"] li, [data-slot="sidebar"] a')) {
          if (re.test(el.textContent?.trim() ?? "")) (el.closest("li") ?? el).style.display = "none";
        }
      }, hide.source);
    }
    await page.screenshot({ path: `${out}${name}-${theme}.png`, clip });
    console.log(theme, name);
  }
  await ctx.close();
}
await browser.close();
