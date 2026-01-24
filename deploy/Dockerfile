# =============================================================================
# Resonance 通用服务 Dockerfile（通过 -module flag 启动 gateway/logic/task/web）
# =============================================================================

# =========================
# 前端构建阶段
# =========================
FROM node:18-alpine AS frontend-builder
WORKDIR /src
# 复制生成的 API 代码以支持软链接解析 (web/src/gen -> ../../api/gen/ts)
COPY api/gen/ts ./api/gen/ts

WORKDIR /src/web
# 复制 package.json 和 lock 文件以利用缓存
COPY web/package*.json ./
RUN npm ci

# 复制前端源码并构建
COPY web/ .
RUN npm run build

# =========================
# 后端构建阶段
# =========================

ARG CGO_ENABLED=0
FROM golang:1.25 AS builder

ARG CGO_ENABLED
RUN if [ "$CGO_ENABLED" = "1" ]; then \
      apt-get update && \
      apt-get install -y --no-install-recommends gcc libc6-dev libsqlite3-dev && \
      rm -rf /var/lib/apt/lists/*; \
    fi

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG CGO_ENABLED
RUN if [ "$CGO_ENABLED" = "1" ]; then \
      CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /app/bin/resonance .; \
    else \
      CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/resonance .; \
    fi

# =========================
# 运行阶段（自动选择镜像）
# =========================

FROM gcr.io/distroless/static-debian12:nonroot AS static
FROM gcr.io/distroless/base-debian12:nonroot AS base

FROM static AS final
ARG CGO_ENABLED
COPY --from=builder /app/bin/resonance /app/resonance
COPY --from=builder /app/configs /app/configs
# 从前端构建阶段复制静态文件
COPY --from=frontend-builder /src/web/dist /app/web/dist
WORKDIR /app
USER nonroot:nonroot
ENTRYPOINT ["/app/resonance"]

FROM base AS final-cgo
ARG CGO_ENABLED
COPY --from=builder /usr/lib/x86_64-linux-gnu/libsqlite3.so* /usr/lib/x86_64-linux-gnu/
COPY --from=builder /app/bin/resonance /app/resonance
COPY --from=builder /app/configs /app/configs
# 从前端构建阶段复制静态文件
COPY --from=frontend-builder /src/web/dist /app/web/dist
WORKDIR /app
USER nonroot:nonroot
ENTRYPOINT ["/app/resonance"]

# =========================
# 构建命令示例
# =========================
# CGO_ENABLED=0 -> docker build --build-arg CGO_ENABLED=0 --target final -t resonance:static -f deploy/Dockerfile .
# CGO_ENABLED=1 -> docker build --build-arg CGO_ENABLED=1 --target final-cgo -t resonance:cgo -f deploy/Dockerfile .
