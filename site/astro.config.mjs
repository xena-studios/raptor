// raptorpanel.net: the landing page, static, served by a Cloudflare Worker
// (docs/DEPLOY.md#static-sites). It shares nothing with the web app's origin.
import { defineConfig } from "astro/config";

export default defineConfig({
  site: "https://raptorpanel.net",
  build: { inlineStylesheets: "never" },
  // Fonts are files, not data: URLs, since the CSP's font-src is 'self'.
  vite: { build: { assetsInlineLimit: 0 } },
});
