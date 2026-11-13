#!/bin/bash
# The server's firewall (docs/DEPLOY.md#the-server), with ufw: SSH, and HTTPS
# from Cloudflare only (api.raptorpanel.net is proxied; anything else
# reaching the origin is refused here and again by Authenticated Origin
# Pulls). Postgres isn't published at all. raptor-firewall.service runs this
# at every boot: Docker publishes ports around ufw, so 443 is limited with
# DOCKER-USER rules, which don't survive a reboot by themselves.
set -euo pipefail
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
ufw --force enable

# Docker-published 443: Cloudflare's ranges only.
iptables -F DOCKER-USER 2>/dev/null || iptables -N DOCKER-USER
ip6tables -F DOCKER-USER 2>/dev/null || ip6tables -N DOCKER-USER
# DOCKER-USER sees forwarded traffic both ways: what the containers send
# out (to the backup bucket, Resend, GitHub, Cloudflare's API) passes first,
# so only connections coming in are limited.
for t in iptables ip6tables; do
  $t -A DOCKER-USER -i docker0 -j RETURN
  $t -A DOCKER-USER -i br-+ -j RETURN
done
for cidr in $ranges; do
  if [[ $cidr == *:* ]]; then
    ip6tables -A DOCKER-USER -p tcp --dport 443 -s "$cidr" -j RETURN
  else
    iptables -A DOCKER-USER -p tcp --dport 443 -s "$cidr" -j RETURN
  fi
done
for t in iptables ip6tables; do
  $t -A DOCKER-USER -p tcp --dport 443 -j DROP
  $t -A DOCKER-USER -j RETURN
done
echo "firewall: SSH, and 443 from Cloudflare ($(wc -w <<<"$ranges") ranges)"
