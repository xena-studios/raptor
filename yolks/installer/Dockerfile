# syntax=docker/dockerfile:1
# The container eggs' install scripts run in (as root): the tools they use
# already in it, so no script installs packages. SteamCMD's 32-bit
# libraries on x86.
FROM debian:bookworm-slim
LABEL org.opencontainers.image.source="https://github.com/xena-studios/raptor"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.description="Raptor install image (yolks/README.md)"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl wget jq unzip zip tar xz-utils zstd bzip2 git file sed gawk \
 && if [ "$(dpkg --print-architecture)" = amd64 ]; then apt-get install -y --no-install-recommends lib32gcc-s1 lib32stdc++6; fi \
 && rm -rf /var/lib/apt/lists/*
