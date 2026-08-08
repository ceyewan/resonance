# =============================================================================
# Resonance 通用服务 Dockerfile（通过 -module flag 启动 gateway/logic/task/web）
# =============================================================================

# =========================
# 前端构建阶段
# =========================
FROM node:22-alpine AS frontend-builder
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

FROM golang:1.26 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/resonance .

# =========================
# 运行阶段
# =========================

FROM alpine:3.20 AS final

# 复制二进制文件
COPY --from=builder /app/bin/resonance /app/resonance

# 复制配置文件
COPY --from=builder /app/configs /app/configs

# 从前端构建阶段复制静态文件
COPY --from=frontend-builder /src/web/dist /app/web/dist

WORKDIR /app
USER nobody
ENTRYPOINT ["/app/resonance"]

# =========================
# Pilot 控制面运行阶段：明确不包含 Node、Pi 或 Provider 凭证运行时。
# =========================
FROM alpine:3.20 AS pilot-control-final

RUN addgroup -S -g 10001 resonance \
    && adduser -S -D -H -u 10001 -G resonance resonance \
    && mkdir -p /app /run/resonance-agent /var/lib/resonance-pilot/sessions \
    && chown -R resonance:resonance /run/resonance-agent /var/lib/resonance-pilot

COPY --from=builder /app/bin/resonance /app/resonance
COPY --from=builder /app/configs /app/configs

WORKDIR /app
USER resonance
ENTRYPOINT ["/app/resonance"]

# =========================
# 隔离 Pi Runtime 阶段
# =========================
# Pi 0.84.1 要求 Node >= 22.19；Bridge 的 package-lock 固定所有 npm 依赖。
FROM node:22-alpine AS pilot-runtime-final

RUN addgroup -S -g 10001 resonance \
    && adduser -S -D -H -u 10001 -G resonance resonance \
    && mkdir -p /app /opt/resonance/bridge /run/resonance-agent /home/resonance \
       /var/lib/resonance-pilot/work /var/lib/resonance-pilot/sessions \
    && chown -R resonance:resonance /run/resonance-agent /home/resonance /var/lib/resonance-pilot

COPY --from=builder /app/bin/resonance /app/resonance
COPY --from=builder /app/configs /app/configs

WORKDIR /opt/resonance/bridge
COPY pilot/bridge/package.json pilot/bridge/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts \
    && test "$(./node_modules/.bin/pi --version)" = "0.84.1"
COPY pilot/bridge/src ./src

WORKDIR /app
USER resonance
ENTRYPOINT ["/app/resonance"]
