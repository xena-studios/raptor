# syntax=docker/dockerfile:1
# x86-only servers on both CPUs: plain Debian on amd64, and Debian's own
# Box64 on arm64, which runs x86 Linux programs there (Mojang's Bedrock
# server). Eggs start the server through box64 when it's on PATH. SDL2
# is there for game servers built with it (World of PADMAN).
FROM debian:trixie-slim
ARG TARGETARCH
LABEL org.opencontainers.image.source="https://github.com/xena-studios/raptor"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.description="Raptor runtime image (yolks/README.md)"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl iproute2 tini tzdata jq unzip libcurl4 libssl3t64 libsdl2-2.0-0 \
    $([ "$TARGETARCH" = arm64 ] && echo box64) \
 && rm -rf /var/lib/apt/lists/*
ENV RAPTOR_RUNTIME_VERSION="box64 --version 2>/dev/null || uname -m"
ENV HOME=/home/container USER=container
WORKDIR /home/container
STOPSIGNAL SIGINT
COPY --chmod=0755 entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "-g", "--"]
CMD ["/entrypoint.sh"]
