#!/bin/bash
# Raptor's runtime images' entrypoint (yolks/README.md). It behaves like
# the Pterodactyl and Pelican yolks' entrypoint, so every egg's startup
# command runs unchanged: {{VAR}} becomes ${VAR}, and the result is
# evaluated in the server's folder. Wings runs the image as the servers'
# user with a read-only root filesystem, so this writes nothing anywhere.

TZ=${TZ:-UTC}
export TZ

# The container's own address, which some eggs bind to.
INTERNAL_IP=$(ip route get 1 2>/dev/null | awk '{print $(NF-2); exit}')
export INTERNAL_IP

cd /home/container || exit 1

if [ -n "${RAPTOR_RUNTIME_VERSION:-}" ]; then
	printf '\033[1m\033[33mcontainer~ \033[0m%s\n' "$RAPTOR_RUNTIME_VERSION"
	eval "$RAPTOR_RUNTIME_VERSION" 2>&1 | head -n 3
fi

PARSED=$(echo "$STARTUP" | sed -e 's/{{/${/g' -e 's/}}/}/g')
printf '\033[1m\033[33mcontainer~ \033[0m%s\n' "$PARSED"
# shellcheck disable=SC2086
eval "$PARSED"
