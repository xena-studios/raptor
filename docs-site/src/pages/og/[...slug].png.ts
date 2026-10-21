// A social card for every docs page (/og/<page>.png, 1200x630), drawn at
// build time with Satori and resvg in the landing page's card style: the
// logo, the page's section, its title, and what it covers. src/routeData.ts
// points each page's og:image here.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { Resvg } from "@resvg/resvg-js";
import type { APIRoute, GetStaticPaths } from "astro";
import { getCollection, type CollectionEntry } from "astro:content";
import satori from "satori";

// DESIGN.md's colours as hex: resvg doesn't read oklch.
const ink = "#0F0808";
const bone = "#F5F0F0";
const ash = "#A9A09F";
const red = "#F05653";

// The build runs in docs-site/.
const font = (pkg: string, file: string) =>
  readFileSync(join(process.cwd(), "node_modules/@fontsource", pkg, "files", file));
const fonts = [
  { name: "Hanken Grotesk", data: font("hanken-grotesk", "hanken-grotesk-latin-700-normal.woff"), weight: 700 as const },
  { name: "Hanken Grotesk", data: font("hanken-grotesk", "hanken-grotesk-latin-500-normal.woff"), weight: 500 as const },
  { name: "JetBrains Mono", data: font("jetbrains-mono", "jetbrains-mono-latin-500-normal.woff"), weight: 500 as const },
];
const logo = `data:image/svg+xml;base64,${Buffer.from(
  readFileSync(join(process.cwd(), "src/assets/logo-dark.svg"), "utf8")
    .replace("oklch(0.66 0.19 25)", red)
    .replace("oklch(0.96 0.005 25)", bone),
).toString("base64")}`;

// The sidebar's group for each directory (astro.config.mjs).
const sections: Record<string, string> = {
  start: "Start here",
  install: "Install",
  servers: "Servers",
  nodes: "Nodes",
  team: "Team and account",
  eggs: "Eggs",
  cli: "Command line",
  security: "Security",
  help: "Help",
};

export const getStaticPaths = (async () => {
  const pages = await getCollection("docs");
  return pages.map((page) => ({ params: { slug: page.id }, props: { page } }));
}) satisfies GetStaticPaths;

type Node = { type: string; props: Record<string, unknown> };
const el = (type: string, style: Record<string, unknown>, children?: unknown): Node => ({ type, props: { style, children } });

export const GET: APIRoute = async ({ props }) => {
  const { page } = props as { page: CollectionEntry<"docs"> };
  const home = page.id === "index";
  const section = home ? "Docs" : (sections[page.id.split("/")[0]] ?? "Docs");
  const title = home ? [el("span", {}, "Pterodactyl had "), el("span", { color: red }, "a good run.")] : page.data.title;
  const titleSize = page.data.title.length > 30 ? 68 : 80;
  // The home page's description repeats the headline; the card says what's inside.
  const description = home
    ? "Everything about Raptor, from linking your first machine to backups, teams, alerts, and the command line."
    : (page.data.description ?? "");

  const card = el(
    "div",
    {
      // Satori sizes boxes without their padding and border: 1200x630 in all.
      width: 1200 - 160,
      height: 630 - 128 - 10,
      display: "flex",
      flexDirection: "column",
      justifyContent: "space-between",
      padding: "64px 80px",
      background: ink,
      borderBottom: `10px solid ${red}`,
      fontFamily: "Hanken Grotesk",
      color: bone,
    },
    [
      el("div", { display: "flex", alignItems: "center", justifyContent: "space-between" }, [
        { type: "img", props: { src: logo, width: 251, height: 40 } },
        el("div", { fontFamily: "JetBrains Mono", fontSize: 22, letterSpacing: "0.08em", color: ash, textTransform: "uppercase" }, `// ${section}`),
      ]),
      el("div", { display: "flex", flexDirection: "column", gap: 24 }, [
        el(
          "div",
          { display: "flex", flexWrap: "wrap", fontSize: titleSize, fontWeight: 700, lineHeight: 1.04, letterSpacing: "-0.03em", maxWidth: 1000 },
          title,
        ),
        el("div", { display: "block", fontSize: 30, fontWeight: 500, lineHeight: 1.35, color: ash, maxWidth: 980, lineClamp: 2 }, description),
      ]),
      el("div", { display: "flex", fontFamily: "JetBrains Mono", fontSize: 22, color: ash }, "docs.raptorpanel.net"),
    ],
  );

  const svg = await satori(card as never, { width: 1200, height: 630, fonts });
  const png = new Resvg(svg, { fitTo: { mode: "width", value: 1200 } }).render().asPng();
  return new Response(png, { headers: { "Content-Type": "image/png" } });
};
