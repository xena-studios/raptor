#!/bin/bash
# Deploys a Panel version to server #1 without downtime (docs/DEPLOY.md#deploying):
# migrations first (always backward compatible with the running version),
# then one Panel at a time. Each one, on its way out, answers 503 to Caddy's
# health check and hands its node connections to the other over 20 s, so
# browsers and nodes never see both down.
#   deploy.sh v1.2.3            (or: deploy.sh latest)
set -euo pipefail
cd "$(dirname "$0")/../primary"
TAG=${1:?usage: deploy.sh <version>}
export PANEL_IMAGE=${PANEL_REPO:-ghcr.io/xena-studios/raptor-panel}:$TAG
[ "${SKIP_SECRETS:-}" = 1 ] || ../scripts/secrets.sh

[ "${SKIP_PULL:-}" = 1 ] || docker compose pull panel-a
echo "deploy: migrating"
docker compose run --rm --no-deps panel-a migrate

wait_healthy() {
  local id
  for _ in $(seq 1 60); do
    id=$(docker compose ps -q "$1")
    [ "$(docker inspect -f '{{.State.Health.Status}}' "$id" 2>/dev/null)" = healthy ] && return 0
    sleep 2
  done
  echo "deploy: $1 didn't become healthy; the other instance is still serving" >&2
  docker compose logs --tail 50 "$1" >&2
  return 1
}

for svc in panel-a panel-b; do
  echo "deploy: replacing $svc"
  docker compose up -d --no-deps --force-recreate "$svc"
  wait_healthy "$svc"
done
# Compose reads .env (this server's settings, docs/DEPLOY.md) on every
# later run, at boot and for backups, so they start this version, not
# whatever :latest is cached.
{ grep -v '^PANEL_IMAGE=' .env 2>/dev/null || true; echo "PANEL_IMAGE=$PANEL_IMAGE"; } > .env.new && mv .env.new .env
echo "deploy: $TAG is live on both instances"
