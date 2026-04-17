FROM golang:1.25-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/super-proxy-pool ./cmd/app

FROM debian:bookworm-slim
ARG MIHOMO_VERSION=v1.19.22
ARG MIHOMO_ASSET=mihomo-linux-amd64-v1.19.22.gz

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl gzip \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://github.com/MetaCubeX/mihomo/releases/download/${MIHOMO_VERSION}/${MIHOMO_ASSET}" -o /tmp/mihomo.gz \
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
