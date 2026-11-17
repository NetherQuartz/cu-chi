# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG TARGETOS
ARG TARGETARCH
ARG AWG_IMAGE
ARG GATEWAY_IMAGE
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X github.com/NetherQuartz/cu-chi/internal/deployer.RuntimeImage=$AWG_IMAGE -X github.com/NetherQuartz/cu-chi/internal/deployer.GatewayImage=$GATEWAY_IMAGE" -o /out/cuchi ./cmd/cuchi

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/NetherQuartz/cu-chi"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/cuchi /cuchi
COPY THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/cuchi"]
