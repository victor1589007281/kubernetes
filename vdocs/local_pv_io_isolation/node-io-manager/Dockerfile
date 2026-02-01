# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# 安装构建依赖
RUN apk add --no-cache git make

# 复制 go.mod 和 go.sum
COPY go.mod go.sum* ./
RUN go mod download || true

# 复制源码
COPY . .

# 构建
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /node-io-manager ./cmd/manager
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /iocli ./cmd/iocli

# Runtime stage
FROM alpine:3.19

# 安装运行时依赖
RUN apk add --no-cache ca-certificates tzdata

# 复制二进制文件
COPY --from=builder /node-io-manager /usr/local/bin/
COPY --from=builder /iocli /usr/local/bin/

# 创建配置目录
RUN mkdir -p /etc/node-io-manager

# 设置时区
ENV TZ=Asia/Shanghai

# 暴露端口
EXPOSE 8080 9100

# 运行
ENTRYPOINT ["/usr/local/bin/node-io-manager"]
