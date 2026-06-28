# syntax=docker/dockerfile:1.7

# ────────────────────────── build stage ──────────────────────────
FROM golang:1.25-bookworm AS build

WORKDIR /src

# Cache module downloads as a separate layer so source-only edits skip
# the network fetch on rebuild.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Fully static binary: CGO off so the result runs against distroless
# static without glibc. -trimpath strips local build paths; -s/-w drops
# the symbol + DWARF tables.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ────────────────────────── runtime stage ──────────────────────────
# distroless/static carries CA certs, tzdata, and a non-root user
# (uid/gid 65532) — nothing else. No shell, no package manager.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=build /out/server /app/server

# Config file is mounted in at deploy time (compose volume / k8s
# configmap). The server falls back to env vars when no file is
# present, so the image stays config-agnostic.

USER nonroot:nonroot

# Listen port is config-driven (server.port). 8000 is the default in
# config.yaml; override via PORT env or config-file when binding to a
# different value.
EXPOSE 8000

ENTRYPOINT ["/app/server"]
