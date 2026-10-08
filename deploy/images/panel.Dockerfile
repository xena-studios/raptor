# The Panel's image (docs/DEPLOY.md): one static binary, no shell, running as
# nobody. Built by the release workflow and pushed to GHCR.
# Cross-compiles on the build machine for every target platform.
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY db ./db
COPY eggs ./eggs
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/xena-studios/raptor/internal/shared/buildinfo.Version=${VERSION} -X github.com/xena-studios/raptor/internal/shared/buildinfo.Commit=${COMMIT} -X github.com/xena-studios/raptor/internal/shared/buildinfo.Date=${DATE}" \
    -o /out/panel ./cmd/panel

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/panel /usr/local/bin/panel
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/panel"]
CMD ["serve", "api"]
