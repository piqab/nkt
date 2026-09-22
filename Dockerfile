# syntax=docker/dockerfile:1

# ---- frontend -----------------------------------------------------------------
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
# Vite writes straight into the Go embed directory.
RUN npm run build

# ---- binary -------------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
ARG VERSION=docker
# CGO off keeps the binary static: modernc.org/sqlite is pure Go, so nothing links.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/nkt ./cmd/nkt

# ---- runtime ------------------------------------------------------------------
FROM alpine:3.21
# The tools the collector shells out to. nginx and haproxy are present only so
# that `nginx -t` and `haproxy -c` can validate configs before they are written.
RUN apk add --no-cache ca-certificates iproute2 iptables nginx haproxy docker-cli tzdata

COPY --from=build /out/nkt /usr/local/bin/nkt

ENV NKT_MODE=local \
    NKT_ADDR=0.0.0.0:8077 \
    NKT_DATA_DIR=/var/lib/netknownsthat

VOLUME ["/var/lib/netknownsthat"]
EXPOSE 8077
# Без USER намеренно: одиночный nkt читает /etc, systemd и сокет Docker
# хоста — ему нужен root (см. README, «Безопасность»).
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s CMD wget -qO- http://127.0.0.1:8077/api/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/nkt"]
