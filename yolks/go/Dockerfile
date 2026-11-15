# syntax=docker/dockerfile:1
ARG VERSION
FROM golang:${VERSION}-bookworm
LABEL org.opencontainers.image.source="https://github.com/xena-studios/raptor"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.description="Raptor runtime image (yolks/README.md)"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl iproute2 tini tzdata git jq \
 && rm -rf /var/lib/apt/lists/*
# No telemetry from servers, and the image's own Go (no toolchain
# downloads); modules and the build cache go in HOME, the server's folder.
ENV RAPTOR_RUNTIME_VERSION="go version" GOTELEMETRY=off GOTOOLCHAIN=local GOPATH=/home/container/.go
ENV HOME=/home/container USER=container
WORKDIR /home/container
STOPSIGNAL SIGINT
COPY --chmod=0755 entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "-g", "--"]
CMD ["/entrypoint.sh"]
