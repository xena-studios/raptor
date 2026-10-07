#!/bin/bash
# Restores the latest backup and archived WAL into a throwaway Postgres and
# checks it against the primary (docs/DEPLOY.md#backups). Run monthly
# (raptor-restore-test.timer) and before trusting a new backup setup: a
# backup that's never been restored isn't a backup.
set -euo pipefail
# The stack holding the primary: server #2's after a failover (ROLE=replica
# in /etc/raptor/stack.env). The config files are always primary/'s.
cd "$(dirname "$0")/../${ROLE:-primary}"
CONF=$(cd ../primary && pwd)
image=$(docker compose config --images | grep -m1 postgres)
name=raptor-restore-test-$$
vol=$name-data
trap 'docker rm -f "$name" >/dev/null 2>&1; docker volume rm -f "$vol" >/dev/null 2>&1' EXIT

# Make sure the newest WAL is in the archive, so the restore reaches "now".
docker compose exec -T -u postgres postgres psql -qAt -d raptor -c "SELECT pg_switch_wal()" >/dev/null
sleep 5

echo "restore-test: restoring into a scratch volume"
docker volume create "$vol" >/dev/null
docker run --rm --env-file "${ENV_FILE:-/run/raptor/postgres.env}" ${NETWORK:+--network "$NETWORK"} \
  -v "$vol":/var/lib/postgresql -v "$CONF/pgbackrest.conf:/etc/pgbackrest/pgbackrest.conf:ro" \
  --entrypoint bash "$image" -c '
    install -d -o postgres -g postgres -m 0700 /var/lib/postgresql/18/docker
    gosu postgres pgbackrest --stanza=raptor --log-level-console=warn restore
  '

echo "restore-test: starting the restored copy"
# The primary's settings (a restore refuses to run with smaller ones), minus
# archiving: this copy must never write to the real archive.
docker run -d --name "$name" --env-file "${ENV_FILE:-/run/raptor/postgres.env}" ${NETWORK:+--network "$NETWORK"} --shm-size 256m \
  -v "$vol":/var/lib/postgresql -v "$CONF/pgbackrest.conf:/etc/pgbackrest/pgbackrest.conf:ro" \
  -v "$CONF/postgresql.conf:/etc/raptor/postgresql.conf:ro" \
  "$image" postgres -c config_file=/etc/raptor/postgresql.conf -c archive_mode=off -c listen_addresses=localhost >/dev/null
for _ in $(seq 1 120); do
  if [ "$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null)" != true ]; then
    echo "restore-test: FAILED: the restored copy stopped:" >&2
    docker logs --tail 30 "$name" >&2
    exit 1
  fi
  if docker exec -u postgres "$name" psql -qAt -d raptor -c "SELECT NOT pg_is_in_recovery()" 2>/dev/null | grep -qx t; then
    break
  fi
  sleep 1
done

q() { docker exec -u postgres "$1" psql -qAt -d raptor -c "$2"; }
check="SELECT (SELECT max(version_id) FROM goose_db_version) || ' ' || (SELECT count(*) FROM users) || ' ' || (SELECT count(*) FROM nodes) || ' ' || (SELECT count(*) FROM orgs)"
restored=$(q "$name" "$check")
primary=$(q "$(docker compose ps -q postgres)" "$check")
echo "restore-test: migration, users, nodes, orgs: restored [$restored], primary [$primary]"
if [ -z "$restored" ] || [ "$restored" != "$primary" ]; then
  echo "restore-test: FAILED: the restored copy doesn't match the primary" >&2
  exit 1
fi
echo "restore-test: OK"
