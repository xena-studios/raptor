#!/usr/bin/env bash
# The Go packages in one CI test shard, so the slowest packages (which
# alone take minutes with -race) run on runners of their own.
#
#   scripts/test-shard.sh api|backup|catalog|rest
set -euo pipefail
case $1 in
api) echo ./internal/panel/api ;;
backup) echo ./internal/wings/backup/... ;;
catalog) echo ./internal/panel/catalog ./internal/panel/images ./internal/wings/files ;;
rest)
  go list ./... | grep -Ev '/internal/panel/api$|/internal/wings/backup(/|$)|/internal/panel/catalog$|/internal/panel/images$|/internal/wings/files$' ;;
*) echo "unknown shard $1" >&2; exit 2 ;;
esac
