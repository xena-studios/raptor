#!/bin/bash
# Deploys a Panel version without downtime (docs/DEPLOY.md#deploying):
# migrations first (always backward compatible with the running version),
# then one Panel at a time. Each one, on its way out, answers 503 to Caddy's
# health check and hands its node connections to the other over 20 s, so
# browsers and nodes never see both down.
#   deploy.sh v1.2.3            (or: deploy.sh latest)
set -euo pipefail
# deploy-from-ci.sh holds its lock on fd 9; anything started here that
# outlives the deploy (op's daemon did) must not take it along, or it keeps
# every later deploy out. The lock stays held by the parent.
exec 9>&-
cd "$(dirname "$0")/../panel"
TAG=${1:?usage: deploy.sh <version>}
export PANEL_IMAGE=${PANEL_REPO:-ghcr.io/xena-studios/raptor-panel}:$TAG
[ "${SKIP_SECRETS:-}" = 1 ] || ../scripts/secrets.sh

# A server set up before deploy/primary became deploy/panel (#344): its
# settings come along, and its Postgres and Caddy, which still mount files
# from deploy/primary that the checkout removed, are recreated from here
# (a few seconds without the database or HTTPS, once).
if [ -f ../primary/.env ] && [ ! -f .env ]; then
  grep -E '^(PANEL_IMAGE|SERVER_NAME)=' ../primary/.env > .env || true
fi
moved=
for svc in postgres caddy; do
  id=$(docker compose ps -aq "$svc" 2>/dev/null || true)
  [ -n "$id" ] || continue
  dir=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' "$id" 2>/dev/null || true)
  [ -n "$dir" ] && [ "$dir" != "$(pwd)" ] && moved="$moved $svc"
done

# The server's name goes in the Panels' hostnames (compose.yaml), so their
# metrics keep the same labels across deploys and differ between servers.
grep -q '^SERVER_NAME=' .env 2>/dev/null || echo "SERVER_NAME=$(hostname -s)" >> .env

[ "${SKIP_PULL:-}" = 1 ] || docker compose pull panel-a
if [ -n "$moved" ]; then
  echo "deploy: recreating$moved from $(pwd) (they were made from another directory)"
  # shellcheck disable=SC2086 # one word per service
  docker compose up -d --wait --no-deps $moved
fi
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
# Caddy reads its config when it starts (no admin API), and a checkout
# replaces the Caddyfile with a new file its container doesn't see. When a
# release changes it, restart Caddy: about a second of dropped connections,
# which browsers and nodes reconnect from. On a new server Caddy isn't
# running yet, and this starts it.
running=$({ docker compose exec -T caddy cat /etc/caddy/Caddyfile 2>/dev/null || true; } | sha256sum | cut -d' ' -f1)
if [ "$running" != "$(sha256sum <Caddyfile | cut -d' ' -f1)" ]; then
  echo "deploy: the Caddyfile changed (or Caddy isn't running); starting Caddy"
  docker compose up -d --no-deps --force-recreate caddy
fi
# Compose reads .env (this server's settings, docs/DEPLOY.md) on every
# later run, at boot and for backups, so they start this version, not
# whatever :latest is cached.
{ grep -v '^PANEL_IMAGE=' .env 2>/dev/null || true; echo "PANEL_IMAGE=$PANEL_IMAGE"; } > .env.new && mv .env.new .env
# The boot unit and timers come from the same checkout, so they always
# match the scripts they run.
if [ "${SKIP_UNITS:-}" != 1 ]; then
  cp ../systemd/raptor-*.service ../systemd/raptor-*.timer /etc/systemd/system/
  systemctl daemon-reload
fi
# Every release leaves its image behind; a year of them fills a small disk.
# Images in use, and the last week's (to roll back to), stay.
docker image prune -af --filter "until=168h" >/dev/null || true
echo "deploy: $TAG is live on both instances"
