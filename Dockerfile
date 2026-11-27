# Magnetar for servers and NAS devices: the web interface on port 8899,
# no window or tray icon. Multi-arch (amd64, arm64).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -buildid= -X github.com/nikolap994/magnetar/internal/engine.Version=${VERSION}" \
      -o /out/magnetar ./cmd/magnetar \
 && mkdir -p /out/config /out/downloads

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.title="Magnetar" \
      org.opencontainers.image.description="Fast, secure, open-source BitTorrent client with a modern web interface" \
      org.opencontainers.image.source="https://github.com/nikolap994/magnetar" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/magnetar /magnetar
COPY --from=build --chown=65532:65532 /out/config /config
COPY --from=build --chown=65532:65532 /out/downloads /downloads
ENV HOME=/config \
    MAGNETAR_CONTAINER=1 \
    MAGNETAR_WEB_HOST=0.0.0.0 \
    MAGNETAR_DOWNLOAD_DIR=/downloads
VOLUME ["/config", "/downloads"]
EXPOSE 8899 51413/tcp 51413/udp
USER 65532:65532
ENTRYPOINT ["/magnetar", "--headless", "--dir", "/config"]
