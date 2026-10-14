#!/bin/bash
# The private link between the two servers (docs/DEPLOY.md#the-private-link):
# a WireGuard interface, wg0, carrying Postgres replication. OVH's VPSs
# can't join a vRack, so the servers' private addresses (10.0.0.2 and
# 10.0.0.3) live on this tunnel instead.
#   wireguard.sh up | down
# Settings come from /etc/raptor/stack.env:
#   WG_ADDRESS=10.0.0.2/24             this server's private address
#   PEER_PRIVATE_IP=10.0.0.3           the other server's
#   WG_PEER_PUBLIC_KEY=<base64>        the other server's public key
#   WG_PEER_ENDPOINT=203.0.113.9:51820 the other server's public address
# This server's private key is read from 1Password ("WireGuard <hostname>",
# field private_key) straight into the interface: it's never on disk.
# raptor-wireguard.service runs this at boot, before the firewall and the
# stack (Postgres binds to the private address, so wg0 must exist first).
set -euo pipefail
: "${WG_ADDRESS:?set WG_ADDRESS, this server private address like 10.0.0.2/24, in /etc/raptor/stack.env}"
: "${PEER_PRIVATE_IP:?set PEER_PRIVATE_IP in /etc/raptor/stack.env}"
: "${WG_PEER_PUBLIC_KEY:?set WG_PEER_PUBLIC_KEY in /etc/raptor/stack.env}"
: "${WG_PEER_ENDPOINT:?set WG_PEER_ENDPOINT, the other server public address and port 51820, in /etc/raptor/stack.env}"
VAULT="Raptor production"
PORT=51820

case "${1:-up}" in
down)
  ip link del wg0 2>/dev/null || true
  exit 0
  ;;
up) ;;
*)
  echo "usage: wireguard.sh up|down" >&2
  exit 2
  ;;
esac

OP_SERVICE_ACCOUNT_TOKEN=$(cat /etc/raptor/op-token)
export OP_SERVICE_ACCOUNT_TOKEN
item="WireGuard $(hostname -s)"

ip link del wg0 2>/dev/null || true
ip link add wg0 type wireguard
ip address add "$WG_ADDRESS" dev wg0
# The key goes from op to wg through a pipe; nothing is written.
wg set wg0 listen-port "$PORT" private-key <(op read "op://$VAULT/$item/private_key")
# Keepalives hold the path open through NAT and stateful firewalls, so the
# replica's connection doesn't stall after a quiet spell.
wg set wg0 peer "$WG_PEER_PUBLIC_KEY" endpoint "$WG_PEER_ENDPOINT" \
  allowed-ips "$PEER_PRIVATE_IP/32" persistent-keepalive 25
ip link set wg0 up
echo "wireguard: wg0 $WG_ADDRESS, peer $PEER_PRIVATE_IP at $WG_PEER_ENDPOINT"
