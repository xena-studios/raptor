# syntax=docker/dockerfile:1
ARG VERSION
FROM ruby:${VERSION}-slim-trixie
LABEL org.opencontainers.image.source="https://github.com/xena-studios/raptor"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.description="Raptor runtime image (yolks/README.md)"
# A compiler and the libraries gems with native extensions build against
# (databases, YAML, FFI, TLS, XML), since servers install their gems at start.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl iproute2 tini tzdata git jq \
    build-essential pkg-config libffi-dev libyaml-dev libssl-dev zlib1g-dev libxml2-dev libxslt1-dev \
    libsqlite3-dev libpq-dev default-libmysqlclient-dev \
 && rm -rf /var/lib/apt/lists/*
# Gems and Bundler's settings go in HOME, the server's folder (the image's
# own /usr/local/bundle is read-only under Wings).
ENV RAPTOR_RUNTIME_VERSION="ruby --version" \
    GEM_HOME=/home/container/.gem BUNDLE_APP_CONFIG=/home/container/.bundle \
    PATH=/home/container/.gem/bin:$PATH
ENV HOME=/home/container USER=container
WORKDIR /home/container
STOPSIGNAL SIGINT
COPY --chmod=0755 entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "-g", "--"]
CMD ["/entrypoint.sh"]
