# syntax=docker/dockerfile:1.7

############################
# Frontend build stage
############################
# Pin to BUILDPLATFORM (host arch) so Bun runs natively. The frontend
# dist is platform-independent JS/CSS, so building on the build host
# avoids the well-known Bun-under-QEMU hang during cross-builds.
FROM --platform=$BUILDPLATFORM oven/bun:1.3-alpine AS frontend
WORKDIR /app
COPY frontend/package.json frontend/bun.lock* frontend/bun.lockb* ./
RUN bun install --frozen-lockfile || bun install
COPY frontend/ ./
RUN bun run build

############################
# Backend build stage
############################
# Same trick for Go: build on the host, but Go's own cross-compile
# (GOARCH=$TARGETARCH below) produces a real linux/$TARGETARCH static
# binary much faster than emulating amd64 under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
ARG TARGETARCH

# Deps first for better caching
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY frontend ./frontend
# Overwrite placeholder dist/ with freshly built frontend assets.
COPY --from=frontend /app/dist ./frontend/dist

ARG VERSION=0.0.0-dev
# BUILD_TAGS is space-separated Go build tags (e.g. "btp"). Empty by default.
ARG BUILD_TAGS=""
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build ${BUILD_TAGS:+-tags ${BUILD_TAGS}} \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/kafkito \
      ./cmd/kafkito

############################
# Runtime stage (distroless)
############################
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/kafkito /kafkito
USER nonroot:nonroot
EXPOSE 37421
ENV PORT=37421
ENTRYPOINT ["/kafkito"]

