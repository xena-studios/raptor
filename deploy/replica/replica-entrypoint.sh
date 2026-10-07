#!/bin/bash
# The replica's first start copies the primary and keeps following it
# (docs/DEPLOY.md). Later starts just run Postgres, as a standby until
# promoted (docs/DEPLOY.md#failover).
set -euo pipefail
PGDATA=${PGDATA:-/var/lib/postgresql/18/docker}
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo "replica: copying the primary at $PRIMARY_HOST"
  install -d -o postgres -g postgres -m 0700 "$PGDATA"
  export PGPASSWORD="$REPLICATOR_PASSWORD"
  # A replication slot on the primary holds WAL the replica hasn't received
  # yet. It may already exist (a rebuilt replica): that's fine.
  psql "host=$PRIMARY_HOST user=replicator replication=true dbname=replication" \
    -c "CREATE_REPLICATION_SLOT replica1 PHYSICAL" 2>&1 | grep -v "already exists" || true
  # -R writes standby.signal and primary_conninfo.
  gosu postgres env PGPASSWORD="$PGPASSWORD" pg_basebackup \
    -h "$PRIMARY_HOST" -U replicator -D "$PGDATA" -R -X stream -S replica1 -P
  unset PGPASSWORD
fi
exec docker-entrypoint.sh "$@"
