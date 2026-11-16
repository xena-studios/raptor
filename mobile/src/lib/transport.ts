import { Code, ConnectError, createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import { AlertService } from "@/gen/raptor/panel/v1/alert_pb";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { CommandService } from "@/gen/raptor/panel/v1/command_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { accessToken, expire } from "@/lib/auth";
import { apiURL } from "@/lib/config";

// The Panel's Connect API, as the web app calls it, with the access token
// as a bearer instead of a cookie. A call the Panel answers UNAUTHENTICATED
// gets one more try with a fresh token.
const bearer: Interceptor = (next) => async (req) => {
  const token = await accessToken();
  req.header.set("Authorization", `Bearer ${token}`);
  try {
    return await next(req);
  } catch (err) {
    if (!(err instanceof ConnectError) || err.code !== Code.Unauthenticated) throw err;
    expire(token);
    req.header.set("Authorization", `Bearer ${await accessToken()}`);
    return next(req);
  }
};

export const transport = createConnectTransport({
  baseUrl: `${apiURL}/api`,
  defaultTimeoutMs: 90_000,
  interceptors: [bearer],
});

export const authClient = createClient(AuthService, transport);
export const orgClient = createClient(OrgService, transport);
export const alertClient = createClient(AlertService, transport);
export const commandClient = createClient(CommandService, transport);

// message is an error in words for the screen.
export function message(err: unknown): string {
  if (err instanceof ConnectError) {
    if (err.code === Code.Unavailable || err.code === Code.DeadlineExceeded) {
      return "Can't reach Raptor right now. Check your connection and try again.";
    }
    return err.rawMessage;
  }
  return err instanceof Error ? err.message : String(err);
}
