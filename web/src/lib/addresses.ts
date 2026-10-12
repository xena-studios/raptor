// A node's addresses, as the app shows them (docs/PANEL.md#node-addresses):
// its own hostname and IPs are hidden until revealed, since they point at
// the machine and many owners put a proxy (TCPShield and the like) in
// front of it; the shown address the owner sets is what players see.

type NodeAddressing = { shortId: string; displayAddress: string };

// nodeHostname is the node's own hostname, which resolves to its IP.
export function nodeHostname(n: { shortId: string }): string {
  return `n-${n.shortId}.raptornodes.net`;
}

// hasPort reports whether a shown address has its own port ("host:port",
// "[v6]:port"), which is shown as is.
export function hasPort(addr: string): boolean {
  return /^\[.+\]:\d+$/.test(addr) || (/:\d+$/.test(addr) && addr.split(":").length === 2);
}

// serverAddress is where players reach a server on a port: the shown
// address if the owner set one, or the node's hostname. private says
// whether it reveals the machine.
export function serverAddress(
  n: NodeAddressing,
  port: number | undefined,
): { text: string; private: boolean } {
  if (n.displayAddress) {
    if (hasPort(n.displayAddress) || !port) return { text: n.displayAddress, private: false };
    return { text: `${n.displayAddress}:${port}`, private: false };
  }
  const host = nodeHostname(n);
  return { text: port ? `${host}:${port}` : host, private: true };
}
