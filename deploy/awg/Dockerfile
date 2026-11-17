# syntax=docker/dockerfile:1
FROM golang:1.26.11-alpine@sha256:ecb67f987aa1a57794c91d19ade7ae0209f2f0f5496aaef9e6ae85ddf92d02ab AS build
RUN apk add --no-cache git build-base linux-headers
WORKDIR /src
RUN git init go && cd go && git remote add origin https://github.com/amnezia-vpn/amneziawg-go.git && git fetch --depth 1 origin tag v3.1.20260828 && git checkout --detach v3.1.20260828 && test "$(git rev-parse HEAD)" = b5928efb6ca19f0153958460c3d141f04abc5c2e && go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 . && CGO_ENABLED=0 GOFLAGS=-trimpath make && mkdir -p /out && cp amneziawg-go /out/
RUN git init tools && cd tools && git remote add origin https://github.com/amnezia-vpn/amneziawg-tools.git && git fetch --depth 1 origin ee0f0a9aa34ff0a0da4b3433b9512781cfe02843 && git checkout --detach FETCH_HEAD && make -C src && cp src/wg /out/awg

FROM alpine:3.23@sha256:469c9ea410b9215eaee7976ba6c0639039b444be8e5d638f7abbada189971eb2 AS base
RUN apk add --no-cache iproute2 iptables curl bind-tools ca-certificates busybox-extras
LABEL org.opencontainers.image.source="https://github.com/NetherQuartz/cu-chi" \
 org.opencontainers.image.version="awg-3.1.20260828-tools-3.1.20260812"
COPY --from=build /out/ /usr/local/bin/
COPY --from=build /src/go/LICENSE /usr/share/licenses/amneziawg-go/LICENSE
COPY --from=build /src/tools/COPYING /usr/share/licenses/amneziawg-tools/COPYING
COPY deploy/awg/entrypoint.sh /usr/local/bin/cuchi-awg
COPY deploy/awg/probe.sh /usr/local/bin/cuchi-probe
ENTRYPOINT ["/usr/local/bin/cuchi-awg"]

FROM golang:1.26.11-alpine@sha256:ecb67f987aa1a57794c91d19ade7ae0209f2f0f5496aaef9e6ae85ddf92d02ab AS gateway-build
WORKDIR /src
COPY go.mod go.sum ./
COPY internal/awg/config.go ./internal/awg/config.go
COPY internal/gateway/config.go ./internal/gateway/config.go
COPY cmd/cuchi-gateway ./cmd/cuchi-gateway
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /cuchi-gateway-config ./cmd/cuchi-gateway

FROM base AS gateway
RUN apk add --no-cache wireguard-tools-wg=1.0.20250521-r1
COPY --from=gateway-build /cuchi-gateway-config /usr/local/bin/cuchi-gateway-config
COPY deploy/gateway/entrypoint.sh /usr/local/bin/cuchi-gateway
COPY deploy/gateway/healthcheck.sh /usr/local/bin/cuchi-gateway-healthcheck
ENTRYPOINT ["/usr/local/bin/cuchi-gateway"]

# Preserve the original default target for VPS runtime builds.
FROM base AS runtime
