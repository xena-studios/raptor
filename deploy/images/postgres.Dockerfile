# Postgres with pgBackRest, for the primary and the replica (docs/DEPLOY.md).
# Same major version as development (compose.yaml).
FROM postgres:18-bookworm
RUN apt-get update \
 && apt-get install -y --no-install-recommends pgbackrest ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && install -d -o postgres -g postgres -m 0750 /var/log/pgbackrest /var/lib/pgbackrest /var/spool/pgbackrest /tmp/pgbackrest
