#!/bin/bash
# First start only: the backup repository (docs/DEPLOY.md#the-server). Made
# now, while the database is being created, so the WAL that creation writes
# can be archived: without it, the archiver retries until the startup gives
# up after a minute. A repository that already holds another database (a
# server rebuilt to restore from it) is left alone: bootstrap.sh restores
# into an empty volume and never gets here.
set -uo pipefail
if pgbackrest --stanza=raptor --log-level-console=warn stanza-create; then
	echo "backup repository ready"
else
	echo "WARNING: couldn't create the backup repository; WAL won't be archived until it exists" \
		"(docs/DEPLOY.md#the-server, or #rebuilding to restore from an existing one)" >&2
fi
