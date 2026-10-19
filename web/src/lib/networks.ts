import { useQuery } from "@tanstack/react-query";

import { commandClient } from "@/lib/transport";

// What Wings' node.networks returns (internal/wings/networks,
// docs/NETWORKS.md): the owner's networks, and every network on the
// machine's Docker.

export type RaptorNetwork = {
  id: string;
  name: string;
  subnet: string;
  internet: boolean;
  ipv6: boolean;
  mtu: number;
  host_access: boolean;
  gateway?: string;
  ipv6_subnet?: string;
  servers: string[]; // IDs of the servers on it, primary or not
  // Servers' fixed addresses on it, by server ID, once they've started
  // there (nodes with network rules).
  addresses?: Record<string, string>;
  created_at?: number;
};

// A rule letting connections from one network (or one server on it) to
// another (or one server), on a protocol and ports.
export type NetworkRule = {
  from: string;
  from_server?: string;
  to: string;
  to_server?: string;
  protocol?: "tcp" | "udp" | "both";
  ports?: string; // "" is every port
  note?: string;
};

export type HostNetwork = {
  name: string;
  driver: string;
  subnets: string[] | null;
  internal: boolean;
  containers: number;
  raptor: boolean; // one of Raptor's
};

export type NodeNetworks = {
  networks: RaptorNetwork[];
  rules?: NetworkRule[]; // nodes with network rules
  host: HostNetwork[];
};

// What node.network.save takes: no ID creates one.
export type NetworkInput = {
  id?: string;
  name: string;
  subnet?: string;
  internet: boolean;
  ipv6: boolean;
  mtu?: number;
  host_access: boolean;
};

// The capability nodes that can have networks report.
export const networksCapability = "networks";
// And the capability nodes that can have rules between them report.
export const networkRulesCapability = "network-rules";

export const networksKey = (nodeId: string) => ["node-networks", nodeId];

// useNodeNetworks is a node's networks (admins; nodes whose Wings can't
// have any don't answer).
export function useNodeNetworks(nodeId: string, enabled = true) {
  return useQuery({
    queryKey: networksKey(nodeId),
    queryFn: async () => {
      const res = await commandClient.execute({
        nodeId,
        action: "node.networks",
        paramsJson: "{}",
      });
      return JSON.parse(res.resultJson) as NodeNetworks;
    },
    enabled,
    retry: false,
  });
}

// The server network every server is on unless it's put on another.
export const defaultNetworkName = "Default network";

// networkTraits are a network's settings that differ from the defaults,
// in words.
export function networkTraits(n: RaptorNetwork): string[] {
  const out: string[] = [];
  if (!n.internet) out.push("No internet");
  if (!n.host_access) out.push("Can't reach the machine");
  if (n.ipv6) out.push("IPv6");
  if (n.mtu) out.push(`MTU ${n.mtu}`);
  return out;
}

// recreates reports whether saving changes a setting that makes Wings
// create the network again (its servers have to be stopped).
export function recreates(cur: RaptorNetwork, next: NetworkInput): boolean {
  return (
    (!!next.subnet && next.subnet !== cur.subnet) ||
    next.internet !== cur.internet ||
    next.ipv6 !== cur.ipv6 ||
    (next.mtu ?? 0) !== cur.mtu
  );
}
