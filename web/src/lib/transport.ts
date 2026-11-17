import { MethodOptions_IdempotencyLevel } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AddonService } from "@/gen/raptor/panel/v1/addon_pb";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { BillingService } from "@/gen/raptor/panel/v1/billing_pb";
import { CommandService } from "@/gen/raptor/panel/v1/command_pb";
import { DemoService } from "@/gen/raptor/panel/v1/demo_pb";
import { EggService } from "@/gen/raptor/panel/v1/egg_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { toastActions } from "@/lib/action-toasts";
import { guardSession } from "@/lib/session-cache";

// The API: https://api.raptorpanel.net/api in production (VITE_API_URL, a
// separate origin; docs/DECISIONS.md #81), /api through Vite's proxy in
// development. The session is an HttpOnly cookie on the API's origin, so
// every call includes credentials and the page never sees the token.
export const apiURL: string = import.meta.env.VITE_API_URL ?? "/api";

// forgetSession clears the route guards' cached session (lib/session-cache.ts)
// after anything that can change it: an AuthService call that isn't a read
// (signing in or out, re-authenticating, profile changes), or any request
// the API answers with "unauthenticated" (docs/DECISIONS.md #385).
const forgetSession: Interceptor = (next) => async (req) => {
  const changes =
    req.service.typeName === AuthService.typeName &&
    req.method.idempotency !== MethodOptions_IdempotencyLevel.NO_SIDE_EFFECTS;
  try {
    return await next(req);
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.Unauthenticated) guardSession.clear();
    throw err;
  } finally {
    if (changes) guardSession.clear();
  }
};

// A request the API doesn't answer in 90 seconds counts as the API being
// unreachable (a command to a node can take up to a minute).
export const transport = createConnectTransport({
  baseUrl: apiURL,
  defaultTimeoutMs: 90_000,
  interceptors: [forgetSession, toastActions],
  fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
});

// Plain clients, for code outside React components (route guards).
export const authClient = createClient(AuthService, transport);
export const billingClient = createClient(BillingService, transport);
export const orgClient = createClient(OrgService, transport);
export const commandClient = createClient(CommandService, transport);
export const staffClient = createClient(StaffService, transport);
export const eggClient = createClient(EggService, transport);
export const addonClient = createClient(AddonService, transport);
export const demoClient = createClient(DemoService, transport);
