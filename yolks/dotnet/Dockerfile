# syntax=docker/dockerfile:1
ARG VERSION
# The SDK (with the ASP.NET Core runtime), so eggs can build projects
# without each server downloading its own.
FROM mcr.microsoft.com/dotnet/sdk:${VERSION}-noble
LABEL org.opencontainers.image.source="https://github.com/xena-studios/raptor"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.description="Raptor runtime image (yolks/README.md)"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl iproute2 tini tzdata git jq libicu74 \
 && rm -rf /var/lib/apt/lists/*
ENV RAPTOR_RUNTIME_VERSION="dotnet --list-runtimes" DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1
ENV HOME=/home/container USER=container
WORKDIR /home/container
STOPSIGNAL SIGINT
COPY --chmod=0755 entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/usr/bin/tini", "-g", "--"]
CMD ["/entrypoint.sh"]
