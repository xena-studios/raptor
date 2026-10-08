import { useQuery } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";

import { type Node, OrgService, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { orgClient } from "@/lib/transport";

export type OrgServer = Server & { node: Node };

// useOrgNodes is the org's nodes, kept fresh.
export function useOrgNodes(orgId: string) {
  return useQuery(OrgService.method.listNodes, { orgId }, { refetchInterval: 10_000 });
}

// useOrgServers is every server the user can see in the org, across its
// nodes (one ListServers per node), with each server's node.
export function useOrgServers(orgId: string): { servers: OrgServer[]; pending: boolean } {
  const nodes = useOrgNodes(orgId);
  const list = nodes.data?.nodes ?? [];
  const results = useQueries({
    queries: list.map((n) => ({
      queryKey: ["org-servers", orgId, n.id],
      queryFn: () => orgClient.listServers({ orgId, nodeId: n.id }),
      refetchInterval: 5_000,
    })),
  });
  const servers = results.flatMap((r, i) => {
    const node = list[i];
    return node ? (r.data?.servers ?? []).map((s) => ({ ...s, node }) as OrgServer) : [];
  });
  return { servers, pending: nodes.isPending || results.some((r) => r.isPending) };
}

// useServer is one server and its node, from the node's list.
export function useServer(orgId: string, nodeId: string, serverId: string) {
  const nodes = useOrgNodes(orgId);
  const servers = useQuery(
    OrgService.method.listServers,
    { orgId, nodeId },
    { refetchInterval: 5_000 },
  );
  return {
    node: nodes.data?.nodes.find((n) => n.id === nodeId),
    server: servers.data?.servers.find((s) => s.id === serverId),
    loaded: !!servers.data,
  };
}

// can reports whether the user may do p on a server ("*": admins and owners).
export function can(server: Server | undefined, p: string): boolean {
  return !!server && (server.permissions.includes("*") || server.permissions.includes(p));
}

export function formatMemory(bytes: bigint | number): string {
  const gb = Number(bytes) / 2 ** 30;
  return gb ? `${gb.toFixed(1)} GB` : "—";
}
