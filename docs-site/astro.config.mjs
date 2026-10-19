// docs.raptorpanel.net: the owner's guide to Raptor, a static Starlight
// site served by a Cloudflare Worker (docs/DEPLOY.md#static-sites). The
// design docs for contributors stay in docs/ on GitHub.
import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";
import starlightLinksValidator from "starlight-links-validator";

export default defineConfig({
  site: "https://docs.raptorpanel.net",
  trailingSlash: "ignore",
  build: { inlineStylesheets: "never" },
  // Fonts are files, not data: URLs, since the CSP's font-src is 'self'.
  vite: { build: { assetsInlineLimit: 0 } },
  integrations: [
    starlight({
      title: "Raptor Docs",
      description: "Run game servers on your own machines, managed from one place.",
      // The full logo (the raptor is never without the wordmark,
      // docs/DESIGN.md#7-brand), with "Docs" after it from theme.css.
      logo: { dark: "./src/assets/logo-dark.svg", light: "./src/assets/logo-light.svg", replacesTitle: true },
      favicon: "/favicon.svg",
      customCss: ["./src/styles/theme.css"],
      // One dark theme in both modes: commands look like the terminal
      // they're pasted into, as the app's console does.
      expressiveCode: {
        themes: ["github-dark-default"],
        styleOverrides: {
          borderRadius: "4px",
          codeBackground: "#0a0c11",
          frames: {
            terminalBackground: "#0a0c11",
            terminalTitlebarBackground: "#0a0c11",
            editorTabBarBackground: "#0a0c11",
          },
        },
      },
      social: [
        { icon: "discord", label: "Discord", href: "https://discord.gg/HyUa3sRpeN" },
        { icon: "github", label: "Source code", href: "https://github.com/xena-studios/raptor" },
      ],
      editLink: { baseUrl: "https://github.com/xena-studios/raptor/edit/main/docs-site/" },
      lastUpdated: true,
      plugins: [starlightLinksValidator()],
      sidebar: [
        { label: "Start here", items: [{ autogenerate: { directory: "start" } }] },
        { label: "Install", items: [{ autogenerate: { directory: "install" } }] },
        { label: "Servers", items: [{ autogenerate: { directory: "servers" } }] },
        { label: "Nodes", items: [{ autogenerate: { directory: "nodes" } }] },
        { label: "Team and account", items: [{ autogenerate: { directory: "team" } }] },
        { label: "Eggs", items: [{ autogenerate: { directory: "eggs" } }] },
        { label: "Command line", items: [{ autogenerate: { directory: "cli" } }] },
        { label: "Security", items: [{ autogenerate: { directory: "security" } }] },
        { label: "Help", items: [{ autogenerate: { directory: "help" } }] },
      ],
    }),
  ],
});
