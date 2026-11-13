#!/bin/bash
# Rebuilds the database from the backup repository (docs/DEPLOY.md#rebuilding):
# the latest backup and every WAL segment archived after it, into an empty
# data volume, then starts it as the live database. The restore is the one
# restore-test.sh runs every month, against a scratch volume.
#   restore.sh                                  everything in the archive
#   restore.sh --type=time "--target=2026-11-05 12:00:00+00" --target-action=promote
# Refuses if the volume already holds a database: this never overwrites one.
#
# Only one server may write to the repository. Before running this, the old
# server's Postgres must be stopped or gone for good; if it comes back while
# this one runs, both archive into the same repository and it's ruined.
set -euo pipefail
cd "$(dirname "$0")/../panel"

if [ -n "$(docker compose ps -q postgres 2>/dev/null)" ]; then
  echo "restore: Postgres is running here; this only restores into a server that has none" >&2
  exit 1
fi
if docker compose run --rm --no-deps -T --entrypoint sh postgres -c 'test -n "$(ls -A /var/lib/postgresql 2>/dev/null)"'; then
  echo "restore: the data volume isn't empty; refusing to restore over a database" >&2
  echo "restore: if it's really meant to go: docker compose down -v (in $(pwd))" >&2
  exit 1
fi

echo "restore: restoring from the backup repository"
# The image's entrypoint is skipped, so no empty database is created first.
docker compose run --rm --no-deps -T --entrypoint bash postgres -c '
  install -d -o postgres -g postgres -m 0700 /var/lib/postgresql/18/docker
  gosu postgres pgbackrest --stanza=raptor --log-level-console=info restore "$@"
' _ "$@"

echo "restore: starting it"
# With no target, recovery replays every archived segment and then opens
# for writes. With one, pass --target-action=promote too (bootstrap.sh
# does): pgBackRest's default pauses at the target, still read-only.
docker compose up -d --wait postgres
for _ in $(seq 1 300); do
  [ "$(docker compose exec -T -u postgres postgres psql -qAt -d raptor -c 'SELECT NOT pg_is_in_recovery()' 2>/dev/null)" = t ] && break
  sleep 1
done
if [ "$(docker compose exec -T -u postgres postgres psql -qAt -d raptor -c 'SELECT NOT pg_is_in_recovery()')" != t ]; then
  echo "restore: FAILED: the database is still recovering after 5 minutes" >&2
  docker compose logs --tail 30 postgres >&2
  exit 1
fi
# A new timeline: until a checkpoint, pgBackRest refuses to back it up.
docker compose exec -T -u postgres postgres psql -qAt -c CHECKPOINT >/dev/null
docker compose exec -T -u postgres postgres pgbackrest --stanza=raptor check
echo "restore: OK: $(docker compose exec -T -u postgres postgres psql -qAt -d raptor -c \
  "SELECT (SELECT count(*) FROM orgs) || ' orgs, ' || (SELECT count(*) FROM nodes) || ' nodes, migration ' || (SELECT max(version_id) FROM goose_db_version)")"
