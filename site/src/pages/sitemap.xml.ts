// /sitemap.xml: every page search engines should know, the library's
// pages included. The privacy page and the social card are left out.
import type { APIRoute } from "astro";
import { href, library } from "../lib/library";

const pages = ["/", "/features/", "/library/", "/pricing/", "/changelog/", "/vs/pterodactyl/", "/compare/", "/privacy"];

export const GET: APIRoute = () => {
  const urls = [...pages, ...library.map(href)].map((p) => `<url><loc>https://raptorpanel.net${p}</loc></url>`);
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls.join("\n")}\n</urlset>\n`,
    { headers: { "Content-Type": "application/xml" } },
  );
};
