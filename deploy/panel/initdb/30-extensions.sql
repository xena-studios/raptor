-- First start only: per-query statistics (postgresql.conf preloads the
-- library). The extension needs a superuser; the Panel's role reads it
-- through pg_monitor (10-roles.sh). A server made before this file: run it
-- once by hand (docs/DEPLOY.md#query-statistics).
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
