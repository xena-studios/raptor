#!/bin/bash
# The server's firewall (docs/DEPLOY.md#firewall), with ufw: SSH, HTTPS from
# Cloudflare only (api.raptorpanel.net is proxied; anything else reaching
# the origin is refused here and again by Authenticated Origin Pulls), and
# Postgres only from the other server, on the private network.
#   firewall.sh <other server's private IP>
# Docker publishes ports around ufw, so 5432 is only bound to the private
# address in compose.yaml, and 443 is limited here with DOCKER-USER rules.
set -euo pipefail
PEER=${1:?usage: firewall.sh <other server private IP>}
ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing
ufw allow OpenSSH
ufw allow from "$PEER" to any port 5432 proto tcp
ufw --force enable

# Docker-published 443: Cloudflare's ranges only.
iptables -F DOCKER-USER 2>/dev/null || iptables -N DOCKER-USER
ip6tables -F DOCKER-USER 2>/dev/null || ip6tables -N DOCKER-USER
for cidr in $(curl -fsS https://www.cloudflare.com/ips-v4); do
  iptables -A DOCKER-USER -p tcp --dport 443 -s "$cidr" -j RETURN
done
for cidr in $(curl -fsS https://www.cloudflare.com/ips-v6); do
  ip6tables -A DOCKER-USER -p tcp --dport 443 -s "$cidr" -j RETURN
done
iptables -A DOCKER-USER -p tcp --dport 443 -j DROP
ip6tables -A DOCKER-USER -p tcp --dport 443 -j DROP
iptables -A DOCKER-USER -j RETURN
ip6tables -A DOCKER-USER -j RETURN
echo "firewall: SSH, 443 from Cloudflare, 5432 from $PEER"
