# syntax=docker/dockerfile:1

# Build stage: compile the API binary from cmd/api.
# SQLCipher is built from C source through cgo, so gcc and musl headers are required.
FROM golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=1 CGO_CFLAGS="-D_LARGEFILE64_SOURCE" GOOS=linux go build -trimpath -ldflags="-s -w -X nestwall/backend/internal/buildinfo.Version=${VERSION}" -o /out/api ./cmd/api

# Runtime stage: minimal pinned base, non-root user.
# Migrations are embedded in the binary (see database/database.go).
# The database file lives in /app/database, which compose bind-mounts from the host.
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
# tzdata lets TZ (set in compose) resolve a named zone for scheduled backups.
RUN apk add --no-cache tzdata \
    && addgroup -S app && adduser -S -G app app \
    && mkdir -p /app/database && chown app:app /app/database
WORKDIR /app
COPY --from=builder /out/api /app/api
USER app
ENV API_PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["/app/api", "healthcheck"]
ENTRYPOINT ["/app/api"]
