// Addresses, as the app shows them (docs/PANEL.md#node-addresses): a
// node's own hostname and IPs are hidden until revealed, since they point
// at the machine and many owners put a proxy (TCPShield and the like) in
// front of it. A server's shown address, which the owner sets, is what
// players see; the node's pages only ever show the node's own, hidden.

// nodeHostname is the node's own hostname, which resolves to its IP.
export function nodeHostname(n: { shortId: string }): string {
  return `${n.shortId}.raptornodes.net`;
}

// hasPort reports whether a shown address has its own port ("host:port",
// "[v6]:port"), which is shown as is.
export function hasPort(addr: string): boolean {
  return /^\[.+\]:\d+$/.test(addr) || (/:\d+$/.test(addr) && addr.split(":").length === 2);
}

// serverAddress is where players reach a server on a port: its shown
// address if the owner set one, or its node's hostname. private says
// whether it reveals the machine.
export function serverAddress(
  node: { shortId: string },
  server: { displayAddress: string },
  port: number | undefined,
): { text: string; private: boolean } {
  const shown = server.displayAddress;
  if (shown) {
    if (hasPort(shown) || !port) return { text: shown, private: false };
    return { text: `${shown}:${port}`, private: false };
  }
  const host = nodeHostname(node);
  return { text: port ? `${host}:${port}` : host, private: true };
}
