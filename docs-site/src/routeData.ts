// Every docs page shares its own social card, from src/pages/og/.
import { defineRouteMiddleware } from "@astrojs/starlight/route-data";

export const onRequest = defineRouteMiddleware((context) => {
  const route = context.locals.starlightRoute;
  if (!route.entry || route.id === "404") return;
  const image = new URL(`/og/${route.id || "index"}.png`, context.site).href;
  const alt = route.id ? `${route.entry.data.title}, in the Raptor docs` : "Raptor Docs: Pterodactyl had a good run.";
  route.head.push(
    { tag: "meta", attrs: { property: "og:image", content: image } },
    { tag: "meta", attrs: { property: "og:image:width", content: "1200" } },
    { tag: "meta", attrs: { property: "og:image:height", content: "630" } },
    { tag: "meta", attrs: { property: "og:image:alt", content: alt } },
  );
});
