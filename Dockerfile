# ── 构建阶段 ──────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /build

# 先拉依赖（利用 layer 缓存）
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 静态编译，不依赖 libc；modernc/sqlite 是纯 Go，不需要 CGO
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=$(grep 'Version = ' main.go | head -1 | sed 's/.*"\(.*\)"/\1/')" \
    -o baidupcs-server .

# ── 运行阶段 ──────────────────────────────────────────────────────────
FROM alpine:3.19

# ca-certificates: HTTPS 请求需要; tzdata: 本地时区
RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -S baidupcs && adduser -S -G baidupcs baidupcs

WORKDIR /app
COPY --from=builder /build/baidupcs-server .

# 配置和数据目录
RUN mkdir -p /app/data && chown -R baidupcs:baidupcs /app
VOLUME ["/app/data"]

ENV BAIDUPCS_GO_CONFIG_DIR=/app/data \
    TZ=Asia/Shanghai

EXPOSE 5299

USER baidupcs
ENTRYPOINT ["./baidupcs-server"]
# 默认启动 API server；可被 docker run 末尾参数覆盖
CMD ["server"]
