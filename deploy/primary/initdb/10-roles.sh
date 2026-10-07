#!/bin/bash
# First start only: the Panel's login role and the replication role
# (docs/DEPLOY.md). The Panel isn't a superuser: it owns its database and
# may create the raptor_app role its migrations use for row-level security.
set -euo pipefail
psql -v ON_ERROR_STOP=1 --username postgres --dbname raptor <<SQL
CREATE ROLE panel LOGIN PASSWORD '${PANEL_DB_PASSWORD}' CREATEROLE;
ALTER DATABASE raptor OWNER TO panel;
ALTER SCHEMA public OWNER TO panel;
-- Read-only statistics: the replica's lag and the WAL archive's health
-- for the Panel's metrics (internal/panel/telemetry).
GRANT pg_monitor TO panel;
CREATE ROLE replicator LOGIN REPLICATION PASSWORD '${REPLICATOR_PASSWORD}';
SQL
