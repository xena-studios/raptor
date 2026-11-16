#!/usr/bin/env bash
# What a pull request changes, for CI to skip jobs it can't affect. Prints
# GitHub Actions outputs: go (Go code and what it embeds or tests against),
# web (the web app), wings (what the Wings end-to-end tests run). Pushes to
# main, and anything this can't tell, run everything.
#
#   scripts/ci-changes.sh <event> [<base sha>]
set -euo pipefail
event=$1
base=${2:-}
all() { printf 'go=true\nweb=true\nwings=true\n'; exit 0; }
[ "$event" = pull_request ] && [ -n "$base" ] || all
files=$(git diff --name-only "$base"...HEAD) || all
[ -n "$files" ] || all
echo "Changed:" >&2
echo "$files" >&2

has() { grep -Eq "$1" <<<"$files"; }
# CI itself, and the scripts it runs, change what every job does.
has '^(\.github/|scripts/|Taskfile\.yml$|compose\.yaml$)' && all

# Go: anything but the web app, the sites, and prose.
if grep -Evq '^(web/|docs/|docs-site/|site/|[^/]+\.md$|LICENSE$)' <<<"$files"; then go=true; else go=false; fi
# Web: the app, and the API it's generated from.
if has '^(web/|proto/)'; then web=true; else web=false; fi
# Wings' end-to-end tests: Wings, what it shares, its generated API, eggs,
# the module, the installer, and the yolks.
if has '^(internal/(wings|eggs|shared|gen)/|cmd/raptor/|go\.(mod|sum)$|install/|yolks/)'; then wings=true; else wings=false; fi
printf 'go=%s\nweb=%s\nwings=%s\n' "$go" "$web" "$wings"
