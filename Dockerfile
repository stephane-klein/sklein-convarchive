FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-X main.version=${VERSION}" -o /out/sklein-convarchive ./cmd

FROM --platform=$BUILDPLATFORM debian:trixie-slim AS runtime-base
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/*

FROM debian:trixie-slim
COPY --from=runtime-base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=runtime-base /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/sklein-convarchive /usr/local/bin/sklein-convarchive

ENTRYPOINT ["/usr/local/bin/sklein-convarchive"]
