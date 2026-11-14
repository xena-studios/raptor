# syntax=docker/dockerfile:1
# SteamCMD and the Source engine's games (x86 only): the 32-bit libraries
# SteamCMD and Source need, and what most dedicated servers link against.
FROM debian:bookworm-slim
LABEL org.opencontainers.image.source="https://github.com/xena-studios/raptor"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.description="Raptor runtime image (yolks/README.md)"
RUN dpkg --add-architecture i386 \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl iproute2 tini tzdata jq unzip \
    lib32gcc-s1 lib32stdc++6 lib32z1 libatomic1 libcurl4 libssl3 libicu72 libsdl2-2.0-0 libsdl2-2.0-0:i386 libtinfo6:i386 \
 && rm -rf /var/lib/apt/lists/*
ENV RAPTOR_RUNTIME_VERSION=""
ENV HOME=/home/container USER=container
WORKDIR /home/container
STOPSIGNAL SIGINT
COPY --chmod=0755 entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "-g", "--"]
CMD ["/entrypoint.sh"]
