# syntax=docker/dockerfile:1
ARG VERSION
FROM dunglas/frankenphp:1-php${VERSION}-trixie
LABEL org.opencontainers.image.source="https://github.com/xena-studios/raptor"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.description="Raptor runtime image (yolks/README.md)"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl iproute2 tini tzdata git jq unzip \
 && rm -rf /var/lib/apt/lists/*
# The extensions most PHP apps ask for (servers can't add any at start: the
# image is read-only), PHP's production settings, and Composer.
RUN install-php-extensions apcu bcmath exif gd gmp intl mysqli opcache pcntl pdo_mysql pdo_pgsql pgsql redis sockets zip \
 && cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini"
COPY --from=composer/composer:2-bin /composer /usr/local/bin/composer
# Wings drops every capability, and a binary that asks for one (FrankenPHP's
# low-port capability) then can't start at all. Caddy's data, Composer's
# cache, and the server's own .ini files (php.d/) live in the server's
# folder; no telemetry.
RUN setcap -r /usr/local/bin/frankenphp
ENV RAPTOR_RUNTIME_VERSION="php --version" \
    XDG_CONFIG_HOME=/home/container/.config XDG_DATA_HOME=/home/container/.local/share \
    COMPOSER_HOME=/home/container/.composer COMPOSER_NO_INTERACTION=1 \
    PHP_INI_SCAN_DIR=:/home/container/php.d
ENV HOME=/home/container USER=container
WORKDIR /home/container
STOPSIGNAL SIGINT
COPY --chmod=0755 entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "-g", "--"]
CMD ["/entrypoint.sh"]
