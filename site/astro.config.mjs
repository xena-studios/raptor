// raptorpanel.net: the landing page, static, served by a Cloudflare Worker
// (docs/DEPLOY.md#static-sites). It shares nothing with the web app's origin.
import { defineConfig } from "astro/config";

export default defineConfig({
  site: "https://raptorpanel.net",
  // Hashed build files go in /assets, not Astro's default /_astro.
  build: { inlineStylesheets: "never", assets: "assets" },
  // Fonts are files, not data: URLs, since the CSP's font-src is 'self'.
  vite: { build: { assetsInlineLimit: 0 } },
});
