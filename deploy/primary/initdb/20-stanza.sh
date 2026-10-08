#!/bin/bash
# First start only: the backup repository (docs/DEPLOY.md#server-1). Made
# now, while the database is being created, so the WAL that creation writes
# can be archived: without it, the archiver retries until the startup gives
# up after a minute. A repository that already holds another database (a
# server rebuilt to restore from it) is left alone: restore over this one.
set -uo pipefail
if pgbackrest --stanza=raptor --log-level-console=warn stanza-create; then
	echo "backup repository ready"
else
	echo "WARNING: couldn't create the backup repository; WAL won't be archived until it exists" \
		"(docs/DEPLOY.md#server-1, or #backups to restore from an existing one)" >&2
fi
