#!/bin/bash
# What the release workflow may run on server #1 (docs/DEPLOY.md#automatic-deploys):
# its SSH key is forced to this script, as the deploy user, through one
# sudo rule. It takes one argument, a release tag, and deploys it only if
# it's a stable release on main: the files in deploy/ come from the tag,
# then deploy.sh rolls the Panel to its image. Nothing else is reachable
# with that key.
#   deploy-from-ci.sh v1.2.3
set -euo pipefail

# Everything runs from a function, so bash has read the whole script before
# the checkout below can change this file.
main() {
	local tag=${1:-}
	if ! [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		echo "deploy-from-ci: refusing ${tag@Q}: not a release tag (vX.Y.Z)" >&2
		exit 2
	fi
	local repo=${RAPTOR_REPO:-/opt/raptor}
	exec 9>"${RAPTOR_DEPLOY_LOCK:-/run/raptor-deploy.lock}"
	flock -n 9 || { echo "deploy-from-ci: another deploy is running" >&2; exit 75; }

	git -C "$repo" fetch -q --force --tags origin main
	local commit
	commit=$(git -C "$repo" rev-parse -q --verify "refs/tags/$tag^{commit}") ||
		{ echo "deploy-from-ci: no tag $tag on origin" >&2; exit 2; }
	git -C "$repo" merge-base --is-ancestor "$commit" origin/main ||
		{ echo "deploy-from-ci: $tag isn't on main" >&2; exit 2; }
	git -C "$repo" checkout -q --detach "$commit"
	echo "deploy-from-ci: $tag ($commit)"
	"$repo/deploy/scripts/deploy.sh" "$tag"
}

main "$@"
exit
