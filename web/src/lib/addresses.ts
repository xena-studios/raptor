// Addresses, as the app shows them (docs/PANEL.md#node-addresses): a
// node's own hostname and IPs are hidden until revealed, since they point
// at the machine and many owners put a proxy (TCPShield and the like) in
// front of it. A server's shown address, which the owner sets, is what
// players see; the node's pages only ever show the node's own, hidden.

// nodeHostname is the node's own hostname, which resolves to its IP.
export function nodeHostname(n: { shortId: string }): string {
  return `${n.shortId}.raptornodes.net`;
}

// serverAddress is where players reach a server on a port: its shown
// address exactly as the owner typed it (with a port only if they put one:
// a domain with an SRV record, or a proxy, often needs none), or its
// node's hostname with the port. private says whether it reveals the
// machine.
export function serverAddress(
  node: { shortId: string },
  server: { displayAddress: string },
  port: number | undefined,
): { text: string; private: boolean } {
  const shown = server.displayAddress;
  if (shown) return { text: shown, private: false };
  const host = nodeHostname(node);
  return { text: port ? `${host}:${port}` : host, private: true };
}

// domainName is a name the owner typed, cleaned up: no scheme, path,
// port, or trailing dot. It's empty when it isn't a domain name.
export function domainName(input: string): string {
  let s = input.trim().toLowerCase();
  s = s
    .replace(/^[a-z]+:\/\//, "")
    .replace(/[/?#].*$/, "")
    .replace(/:\d*$/, "")
    .replace(/\.$/, "");
  const label = /^(?!-)[a-z0-9-]{1,63}(?<!-)$/;
  const parts = s.split(".");
  if (s.length > 253 || parts.length < 2 || !parts.every((p) => label.test(p))) return "";
  if (/^[0-9]+$/.test(parts.at(-1) ?? "")) return "";
  return s;
}

// subdomainLabel is the part before the owner's domain (play, or mc.eu),
// cleaned up; empty when it isn't valid.
export function subdomainLabel(input: string): string {
  const s = input
    .trim()
    .toLowerCase()
    .replace(/^\.+|\.+$/g, "");
  const label = /^(?!-)[a-z0-9-]{1,63}(?<!-)$/;
  return s !== "" && s.split(".").every((p) => label.test(p)) ? s : "";
}

// DnsRecord is one record to add at a DNS provider. name is relative to
// the domain, as most providers want it (@ is the domain itself); full is
// the whole name. private says the value leads to the machine, so the app
// shows it hidden until revealed.
export type DnsRecord =
  | { type: "CNAME" | "A"; name: string; full: string; value: string; private: boolean }
  | {
      type: "SRV";
      name: string;
      full: string;
      service: string;
      protocol: string;
      host: string;
      priority: number;
      weight: number;
      port: number;
      target: string;
      private: boolean;
    };

// domainRecords is what to add so players reach a server at a domain the
// owner has (example.com): at a subdomain of it (sub: play), or at the
// domain itself (sub: ""). srv is for Minecraft Java and its proxies,
// which look up an SRV record so players needn't type the port; at the
// domain itself it's the only record they need, so a website there keeps
// working. Other games need an A record with the node's IPv4 there, since
// a CNAME isn't allowed on a domain itself. shown is what players type.
export function domainRecords(o: {
  domain: string;
  sub: string;
  host: string;
  ipv4: string;
  port: number;
  srv: boolean;
}): { records: DnsRecord[]; shown: string } {
  const full = o.sub ? `${o.sub}.${o.domain}` : o.domain;
  const records: DnsRecord[] = [];
  if (o.sub) {
    records.push({ type: "CNAME", name: o.sub, full, value: o.host, private: true });
  } else if (!o.srv) {
    records.push({ type: "A", name: "@", full, value: o.ipv4, private: true });
  }
  if (o.srv) {
    const name = o.sub ? `_minecraft._tcp.${o.sub}` : "_minecraft._tcp";
    records.push({
      type: "SRV",
      name,
      full: `_minecraft._tcp.${full}`,
      service: "_minecraft",
      protocol: "_tcp",
      host: o.sub || "@",
      priority: 0,
      weight: 5,
      port: o.port,
      target: o.host,
      private: true,
    });
  }
  return { records, shown: o.srv ? full : `${full}:${o.port}` };
}
