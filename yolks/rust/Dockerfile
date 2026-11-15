# syntax=docker/dockerfile:1
ARG VERSION
FROM rust:${VERSION}-slim-bookworm
LABEL org.opencontainers.image.source="https://github.com/xena-studios/raptor"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.description="Raptor runtime image (yolks/README.md)"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl iproute2 tini tzdata git jq pkg-config libssl-dev \
 && rm -rf /var/lib/apt/lists/*
# Crates and the registry go in the server's folder; the toolchain stays
# where the base image put it (PATH still has /usr/local/cargo/bin).
ENV RAPTOR_RUNTIME_VERSION="cargo --version" CARGO_HOME=/home/container/.cargo
ENV HOME=/home/container USER=container
WORKDIR /home/container
STOPSIGNAL SIGINT
COPY --chmod=0755 entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "-g", "--"]
CMD ["/entrypoint.sh"]
