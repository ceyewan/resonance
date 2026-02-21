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

FROM golang:1.25 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/resonance .

# =========================
# 运行阶段
# =========================

FROM gcr.io/distroless/static-debian12:nonroot AS final

# 复制二进制文件
COPY --from=builder --chown=nonroot:nonroot /app/bin/resonance /app/resonance

# 复制配置文件
COPY --from=builder --chown=nonroot:nonroot /app/configs /app/configs

# 从前端构建阶段复制静态文件
COPY --from=frontend-builder --chown=nonroot:nonroot /src/web/dist /app/web/dist

WORKDIR /app
ENTRYPOINT ["/app/resonance"]
