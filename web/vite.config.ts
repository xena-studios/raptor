import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [tanstackRouter({ target: "react", autoCodeSplitting: true }), react(), tailwindcss()],
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
});
