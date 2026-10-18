// Makes public/og.png, the social card, from src/pages/og-card.astro.
// Run with the dev server up: pnpm dev, then node scripts/og.mjs [url].
import { chromium } from "playwright";

const url = process.argv[2] ?? "http://localhost:4321/og-card";
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1200, height: 630 }, colorScheme: "dark" });
await page.goto(url, { waitUntil: "networkidle" });
// The dev server's toolbar isn't part of the card.
await page.addStyleTag({ content: "astro-dev-toolbar { display: none !important; }" });
await page.locator(".og-card").screenshot({ path: new URL("../public/og.png", import.meta.url).pathname });
await browser.close();
