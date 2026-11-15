# syntax=docker/dockerfile:1
# Plain Debian for servers that ship their own binaries (Cuberite, Pumpkin,
# Gitea, …), with the libraries most of them link against.
FROM debian:trixie-slim
LABEL org.opencontainers.image.source="https://github.com/xena-studios/raptor"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.description="Raptor runtime image (yolks/README.md)"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl iproute2 tini tzdata git jq unzip xz-utils \
    libcurl4 libssl3t64 libicu76 libsqlite3-0 zlib1g fontconfig libatomic1 \
 && rm -rf /var/lib/apt/lists/*
ENV RAPTOR_RUNTIME_VERSION=""
ENV HOME=/home/container USER=container
WORKDIR /home/container
STOPSIGNAL SIGINT
COPY --chmod=0755 entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "-g", "--"]
CMD ["/entrypoint.sh"]
