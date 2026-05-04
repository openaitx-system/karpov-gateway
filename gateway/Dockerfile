# syntax=docker/dockerfile:1.7
# Music Gateway 后端 — go multi-stage build
#
# 注意: migrations/*.sql 通过 //go:embed 编译进二进制, runtime 镜像无需单独 COPY。
# 构建上下文: 仓库 gateway/ 目录 (即 docker-compose 里 build.context: ../../gateway)。

# ---- builder ----
FROM golang:1.26-alpine AS builder
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOFLAGS=-mod=readonly \
    GOTOOLCHAIN=auto

WORKDIR /src
RUN apk add --no-cache git ca-certificates tzdata

# 先单独 COPY mod 文件以最大化 layer 缓存命中
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags='-s -w' \
        -o /out/qqmusic-gateway \
        ./cmd/qqmusic-gateway

# ---- runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S mgw && adduser -S -G mgw -h /app mgw

WORKDIR /app
COPY --from=builder /out/qqmusic-gateway /usr/local/bin/qqmusic-gateway

ENV TZ=Asia/Shanghai \
    GIN_MODE=release

USER mgw

# Gateway HTTP + 6 个 gRPC 子服务端口
EXPOSE 8080 9000 9001 9002 9003 9004 9005

CMD ["/usr/local/bin/qqmusic-gateway"]
