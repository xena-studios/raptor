#!/bin/bash
# The server's firewall (docs/DEPLOY.md#both-servers), with ufw: SSH, HTTPS
# from Cloudflare only (api.raptorpanel.net is proxied; anything else
# reaching the origin is refused here and again by Authenticated Origin
# Pulls), and Postgres only from the other server, on the private network.
#   firewall.sh [other server's private IP]
# The other server's address comes from the argument or PEER_PRIVATE_IP in
# /etc/raptor/stack.env; with neither (one server), Postgres takes no
# connections from outside at all. raptor-firewall.service runs this at
# every boot: Docker publishes ports around ufw, so 443 is limited with
# DOCKER-USER rules, which don't survive a reboot by themselves.
set -euo pipefail
PEER=${1:-${PEER_PRIVATE_IP:-}}
CACHE=${RAPTOR_CF_IPS:-/etc/raptor/cloudflare-ips}

# Cloudflare's ranges, kept for a boot without network: with no list at
# all, 443 is closed to everyone rather than open.
if v4=$(curl -fsS --retry 3 https://www.cloudflare.com/ips-v4) &&
  v6=$(curl -fsS --retry 3 https://www.cloudflare.com/ips-v6); then
  printf '%s\n%s\n' "$v4" "$v6" >"$CACHE.new" && mv "$CACHE.new" "$CACHE"
else
  echo "firewall: couldn't fetch Cloudflare's ranges, using $CACHE" >&2
fi
ranges=$(cat "$CACHE" 2>/dev/null || true)

ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing
ufw allow OpenSSH
if [[ -n $PEER ]]; then
  ufw allow from "$PEER" to any port 5432 proto tcp
fi
ufw --force enable

# Docker-published 443: Cloudflare's ranges only.
iptables -F DOCKER-USER 2>/dev/null || iptables -N DOCKER-USER
ip6tables -F DOCKER-USER 2>/dev/null || ip6tables -N DOCKER-USER
for cidr in $ranges; do
  if [[ $cidr == *:* ]]; then
    ip6tables -A DOCKER-USER -p tcp --dport 443 -s "$cidr" -j RETURN
  else
    iptables -A DOCKER-USER -p tcp --dport 443 -s "$cidr" -j RETURN
  fi
done
iptables -A DOCKER-USER -p tcp --dport 443 -j DROP
ip6tables -A DOCKER-USER -p tcp --dport 443 -j DROP
iptables -A DOCKER-USER -j RETURN
ip6tables -A DOCKER-USER -j RETURN
echo "firewall: SSH, 443 from Cloudflare ($(wc -w <<<"$ranges") ranges), 5432 from ${PEER:-nowhere}"
