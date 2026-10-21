// Every docs page's title, description, and section, for Raptor Bot's
// /docs command on the Discord server (internal/panel/discord/docs.go).
import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

import { sections } from "../sections";

export const GET: APIRoute = async () => {
  const pages = await getCollection("docs");
  const groups = Object.keys(sections);
  // In the sidebar's order, so an empty search lists the guide from the start.
  const rank = (p: (typeof pages)[number]) => {
    const g = groups.indexOf(p.id.split("/")[0]);
    return [g === -1 ? -1 : g, p.data.sidebar?.order ?? 0] as const;
  };
  const list = pages
    .filter((p) => p.id !== "404")
    .sort((a, b) => {
      const [ga, oa] = rank(a);
      const [gb, ob] = rank(b);
      return ga - gb || oa - ob || a.data.title.localeCompare(b.data.title);
    })
    .map((p) => ({
      title: p.data.title,
      description: p.data.description ?? "",
      section: sections[p.id.split("/")[0]] ?? "",
      path: p.id === "index" ? "/" : `/${p.id}/`,
    }));
  return new Response(JSON.stringify(list), { headers: { "Content-Type": "application/json" } });
};
