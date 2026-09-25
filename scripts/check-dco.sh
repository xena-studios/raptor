#!/usr/bin/env bash
# Fails if any commit in <base>..<head> lacks a Signed-off-by line matching its author.
# Usage: scripts/check-dco.sh <base> <head>
set -euo pipefail

base=$1
head=$2
fail=0

for sha in $(git rev-list --no-merges "$base..$head"); do
  author=$(git show -s --format='%an <%ae>' "$sha")
  if ! git show -s --format='%B' "$sha" | grep -qxF "Signed-off-by: $author"; then
    echo "missing sign-off: $(git show -s --format='%h %s' "$sha") (expected: Signed-off-by: $author)"
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "Fix with: git rebase --signoff $base"
  exit 1
fi
echo "dco ok"
