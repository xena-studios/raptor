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
