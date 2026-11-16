// Every docs page shares its own social card, from src/pages/og/. The home
// page's title is the site's name alone, not "Raptor Docs · Raptor Docs",
// and the not-found page (src/pages/404.astro) claims no canonical address.
import { defineRouteMiddleware } from "@astrojs/starlight/route-data";

export const onRequest = defineRouteMiddleware((context) => {
  const route = context.locals.starlightRoute;
  if (route.id === "404") {
    route.head = route.head.filter((t) => !(t.tag === "link" && t.attrs?.rel === "canonical") && t.attrs?.property !== "og:url");
    return;
  }
  if (!route.entry) return;
  if (!route.id) {
    const title = route.head.find((t) => t.tag === "title");
    if (title) title.content = route.siteTitle;
  }
  const image = new URL(`/og/${route.id || "index"}.png`, context.site).href;
  const alt = route.id
    ? `${route.entry.data.title}, in the Raptor docs`
    : `Raptor Docs: ${route.entry.data.hero?.title ?? route.entry.data.title}`;
  route.head.push(
    { tag: "meta", attrs: { property: "og:image", content: image } },
    { tag: "meta", attrs: { property: "og:image:width", content: "1200" } },
    { tag: "meta", attrs: { property: "og:image:height", content: "630" } },
    { tag: "meta", attrs: { property: "og:image:alt", content: alt } },
  );
});
