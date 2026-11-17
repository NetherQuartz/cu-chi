# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26.11-alpine@sha256:ecb67f987aa1a57794c91d19ade7ae0209f2f0f5496aaef9e6ae85ddf92d02ab AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG TARGETOS
ARG TARGETARCH
ARG AWG_IMAGE
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X github.com/NetherQuartz/cu-chi/internal/deployer.RuntimeImage=$AWG_IMAGE" -o /out/cuchi ./cmd/cuchi

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/NetherQuartz/cu-chi"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/cuchi /cuchi
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/cuchi"]
