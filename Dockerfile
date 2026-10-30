# VCRX Media Server. Built from the repo root:
#   docker build -t vcrx-media-server .
# Runtime is Debian with vanilla FFmpeg from the Debian archive.

FROM golang:1.26-trixie@sha256:a050dcceee6eb9bf110151662dfac7662f3bec40927c7dee48731e3d92083d42 AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY server/ ./
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/vcrx ./cmd/vcrx

FROM debian:trixie-slim@sha256:f6b08b09a05725f9431502d3b5714f5c4576e82c1a84fa1debe0c24a7e0c3ec4
RUN apt-get update \
 && apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata \
 && apt-get upgrade -y \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --gid 1000 vcrx && useradd --uid 1000 --gid 1000 --no-create-home --shell /usr/sbin/nologin vcrx \
 && mkdir -p /config /cache /media && chown vcrx:vcrx /config /cache
COPY --from=build /out/vcrx /usr/local/bin/vcrx
USER vcrx
ENV VCRX_CONFIG_DIR=/config VCRX_CACHE_DIR=/cache VCRX_MEDIA_ROOTS=/media
EXPOSE 3750
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["vcrx", "healthcheck"]
ENTRYPOINT ["vcrx"]
CMD ["serve"]
