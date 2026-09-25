import { createConnectTransport } from "@connectrpc/connect-web";

// The Panel mounts Connect services under /api (proxied to the API role in dev).
export const transport = createConnectTransport({ baseUrl: "/api" });
