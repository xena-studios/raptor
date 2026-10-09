import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { CatalogService } from "@/gen/raptor/panel/v1/catalog_pb";
import { CommandService } from "@/gen/raptor/panel/v1/command_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";

// The API: https://api.raptorpanel.net/api in production (VITE_API_URL, a
// separate origin; docs/DECISIONS.md #81), /api through Vite's proxy in
// development. The session is an HttpOnly cookie on the API's origin, so
// every call includes credentials and the page never sees the token.
export const apiURL: string = import.meta.env.VITE_API_URL ?? "/api";

// A request the API doesn't answer in 90 seconds counts as the API being
// unreachable (a command to a node can take up to a minute).
export const transport = createConnectTransport({
  baseUrl: apiURL,
  defaultTimeoutMs: 90_000,
  fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
});

// Plain clients, for code outside React components (route guards).
export const authClient = createClient(AuthService, transport);
export const orgClient = createClient(OrgService, transport);
export const commandClient = createClient(CommandService, transport);
export const catalogClient = createClient(CatalogService, transport);
