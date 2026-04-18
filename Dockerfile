# syntax=docker/dockerfile:1

ARG BUILDPLATFORM
ARG TARGETPLATFORM

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN set -eux; \
    export CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}"; \
    if [ "${TARGETARCH}" = "arm" ] && [ -n "${TARGETVARIANT}" ]; then export GOARM="${TARGETVARIANT#v}"; fi; \
    go build -trimpath -ldflags="-s -w" -o /out/super-proxy-pool ./cmd/app

FROM --platform=$TARGETPLATFORM debian:bookworm-slim
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT
ARG MIHOMO_VERSION=v1.19.22
ARG MIHOMO_ASSET

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl gzip \
    && rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    if [ "${TARGETOS}" != "linux" ]; then \
        echo "unsupported TARGETOS: ${TARGETOS}" >&2; \
        exit 1; \
    fi; \
    asset="${MIHOMO_ASSET}"; \
    if [ -z "${asset}" ]; then \
        case "${TARGETARCH}" in \
            amd64|386|arm64|ppc64le|riscv64|s390x) mihomo_arch="${TARGETARCH}" ;; \
            arm) \
                case "${TARGETVARIANT#v}" in \
                    5|6|7) mihomo_arch="armv${TARGETVARIANT#v}" ;; \
                    "") mihomo_arch="armv7" ;; \
                    *) \
                        echo "unsupported TARGETVARIANT for arm: ${TARGETVARIANT}" >&2; \
                        exit 1; \
                        ;; \
                esac \
                ;; \
            *) \
                echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; \
                exit 1; \
                ;; \
        esac; \
        asset="mihomo-${TARGETOS}-${mihomo_arch}-${MIHOMO_VERSION}.gz"; \
    fi; \
    curl -fsSL "https://github.com/MetaCubeX/mihomo/releases/download/${MIHOMO_VERSION}/${asset}" -o /tmp/mihomo.gz \
    && gunzip /tmp/mihomo.gz \
    && mv /tmp/mihomo /usr/local/bin/mihomo \
    && chmod +x /usr/local/bin/mihomo

WORKDIR /app
COPY --from=builder /out/super-proxy-pool /usr/local/bin/super-proxy-pool

VOLUME ["/data"]
EXPOSE 7891
ENV DATA_DIR=/data
ENV MIHOMO_BINARY=/usr/local/bin/mihomo

ENTRYPOINT ["/usr/local/bin/super-proxy-pool"]
