import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv, type Plugin } from "vite";

// preconnectAPI has the page open its connection to the API (VITE_API_URL,
// a separate origin in production) while the app's code loads. There's no
// crossorigin attribute: the app's requests send the session cookie, and
// those use the browser's credentialed connections. Development calls /api
// on the page's own origin, so there's nothing to do.
function preconnectAPI(apiURL: string | undefined): Plugin {
  return {
    name: "raptor-preconnect-api",
    transformIndexHtml() {
      if (!apiURL || !URL.canParse(apiURL)) return [];
      const href = new URL(apiURL).origin;
      return [{ tag: "link", attrs: { rel: "preconnect", href }, injectTo: "head-prepend" }];
    },
  };
}

export default defineConfig(({ mode }) => ({
  plugins: [
    tanstackRouter({ target: "react", autoCodeSplitting: true }),
    react(),
    tailwindcss(),
    preconnectAPI(loadEnv(mode, import.meta.dirname, "VITE_").VITE_API_URL),
  ],
  resolve: {
    alias: { "@": path.resolve(import.meta.dirname, "src") },
  },
  // The CSP allows fonts from 'self' only, so small font files stay files
  // rather than data: URIs.
  build: { assetsInlineLimit: (file) => (/\.woff2?$/.test(file) ? false : undefined) },
  // Monaco's workers import modules of their own.
  worker: { format: "es" },
  server: {
    proxy: {
      // ws: the live socket (/api/live) too.
      "/api": { target: "http://localhost:8080", ws: true },
    },
  },
}));
