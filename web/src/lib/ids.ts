// IDs in the app's URLs (docs/PANEL.md#urls): orgs, nodes, and servers have
// a full ID (a UUID) and a short one, its last part
// (01a14621-e529-7833-b8b3-1cdd002cd9a3 → 1cdd002cd9a3). URLs carry the
// short one; pages work with the full one, which the routes look up before
// they load (resolveOrg and the rest) and useIds reads back.
import { useParams } from "@tanstack/react-router";

import { shortId } from "@/lib/short-id";
import { orgClient } from "@/lib/transport";

export { canonical, sameId, shortId } from "@/lib/short-id";

// Full IDs by scope and short ID: orgs, an org's nodes, a node's servers.
const known = new Map<string, string>();
const key = (scope: string, short: string) => `${scope}/${short}`;

function remember(scope: string, ids: string[]) {
  for (const id of ids) known.set(key(scope, shortId(id)), id);
}

// fullId is the full ID a URL's ID stands for: itself if it's full, or
// unknown (the page then finds nothing, as for any missing ID).
function fullId(scope: string, param: string | undefined): string {
  if (!param) return "";
  if (param.includes("-")) return param;
  return known.get(key(scope, param)) ?? param;
}

const orgScope = "org";
const nodeScope = (org: string) => `node:${org}`;
const serverScope = (node: string) => `server:${node}`;

async function resolve(scope: string, param: string, list: () => Promise<string[]>) {
  if (param.includes("-") || known.has(key(scope, param))) return;
  try {
    remember(scope, await list());
  } catch {
    // The page shows what went wrong when it asks itself.
  }
}

// resolveOrg looks up an org's full ID from a URL's.
export function resolveOrg(param: string): Promise<void> {
  return resolve(orgScope, param, async () => (await orgClient.listOrgs({})).orgs.map((o) => o.id));
}

// resolveNode looks up a node's full ID (after its org's).
export function resolveNode(orgParam: string, param: string): Promise<void> {
  const orgId = fullId(orgScope, orgParam);
  return resolve(nodeScope(orgId), param, async () =>
    (await orgClient.listNodes({ orgId })).nodes.map((n) => n.id),
  );
}

// resolveServer looks up a server's full ID (after its node's).
export async function resolveServer(
  orgParam: string,
  nodeParam: string,
  param: string,
): Promise<void> {
  await resolveNode(orgParam, nodeParam);
  const orgId = fullId(orgScope, orgParam);
  const nodeId = fullId(nodeScope(orgId), nodeParam);
  return resolve(serverScope(nodeId), param, async () =>
    (await orgClient.listServers({ orgId, nodeId })).servers.map((s) => s.id),
  );
}

// useIds is the page's full org, node, and server IDs ("" for none).
export function useIds(): { orgId: string; nodeId: string; serverId: string } {
  const p = useParams({ strict: false }) as {
    orgId?: string;
    nodeId?: string;
    serverId?: string;
  };
  const orgId = fullId(orgScope, p.orgId);
  const nodeId = fullId(nodeScope(orgId), p.nodeId);
  return { orgId, nodeId, serverId: fullId(serverScope(nodeId), p.serverId) };
}
