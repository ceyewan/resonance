.PHONY: gen tidy build-gateway build-logic build-task web-install web-dev web-build up down logs ps network-create
include .env
export

# 1. 生成代码 (使用 buf)
gen:
	@echo "🔧 Generating contract code..."
	@cd api && rm -rf gen
	@echo "  > Generating Go base + gRPC (All proto files)..."
	@cd api && buf generate --template buf.gen.go.yaml
	@echo "  > Generating ConnectRPC (Only gateway/v1/api.proto)..."
	@cd api && buf generate --template buf.gen.connect.yaml --path proto/gateway/v1/api.proto
	@echo "  > Generating TypeScript (gateway/v1/api.proto, gateway/v1/packet.proto, common)..."
	@cd api && buf generate --template buf.gen.ts.yaml --path proto/gateway/v1/api.proto --path proto/gateway/v1/packet.proto --path proto/common
	@echo "✅ Code generation complete!"
	@echo ""
	@echo "📦 Generated structure:"
	@echo "  - gateway/v1/api.proto    → gRPC + ConnectRPC + TypeScript (客户端访问)"
	@echo "  - gateway/v1/push.proto   → gRPC only (Task → Gateway)"
	@echo "  - logic/v1/*.proto        → gRPC only (服务间调用)"
	@echo "  - common/*.proto          → TypeScript (共享类型)"
	@echo "  - gateway/v1/packet.proto → TypeScript (WebSocket 消息格式)"

# 2. 整理依赖
tidy:
	@echo "🧹 Tidying go modules..."
	@go mod tidy

# 3. 编译服务
build-gateway:
	@echo "🏗️ Building Gateway..."
	@go build -o bin/gateway main.go

build-logic:
	@echo "🏗️ Building Logic..."
	@go build -o bin/logic main.go

build-task:
	@echo "🏗️ Building Task..."
	@go build -o bin/task main.go

# 4. 运行示例 (开发调试用)
run-gateway:
	@go run main.go -module gateway

run-logic:
	@go run main.go -module logic

run-task:
	@go run main.go -module task

# 5. Web 前端相关命令

# 安装前端依赖
web-install:
	@echo "📦 Installing web dependencies..."
	@cd web && npm install
	@echo "✅ Web dependencies installed!"

# 启动前端开发服务器（自动从 .env 读取 Gateway 地址）
web-dev: gen
	@echo "🚀 Starting web development server..."
	@echo "   Local: http://$(WEB_HOST):$(WEB_PORT)"
	@echo "   API:   http://$(GATEWAY_HTTP_HOST):$(GATEWAY_HTTP_PORT)"
	@cd web && \
	VITE_API_BASE_URL=http://$(GATEWAY_HTTP_HOST):$(GATEWAY_HTTP_PORT) \
	VITE_WS_HOST=$(GATEWAY_HTTP_HOST) \
	VITE_WS_PORT=$(GATEWAY_HTTP_PORT) \
	npm run dev -- --host $(WEB_HOST) --port $(WEB_PORT)

# 构建前端生产版本（自动从 .env 读取 Gateway 地址）
web-build: gen
	@echo "🏗️ Building web for production..."
	@echo "   API: http://$(GATEWAY_HTTP_HOST):$(GATEWAY_HTTP_PORT)"
	@cd web && \
	VITE_API_BASE_URL=http://$(GATEWAY_HTTP_HOST):$(GATEWAY_HTTP_PORT) \
	VITE_WS_HOST=$(GATEWAY_HTTP_HOST) \
	VITE_WS_PORT=$(GATEWAY_HTTP_PORT) \
	npm run build
	@echo "✅ Web build complete! Output: web/$(WEB_BUILD_DIR)"

# 6. 一键完成所有生成和依赖整理
all: gen tidy web-install

# ============================================================================
# Docker Compose 指令 (基础设施)
# ============================================================================

# 创建 Docker 网络
network-create:
	@echo "🌐 Creating Docker network..."
	@docker network create resonance-net 2>/dev/null || true

# 启动所有基础服务 (etcd, mysql, redis, nats, prometheus, grafana)
up: network-create
	@echo "🚀 Starting Resonance infrastructure..."
	@docker compose --env-file .env -f deploy/compose.yaml up -d
	@echo "✅ Infrastructure started!"
	@echo ""
	@echo "📊 Service URLs:"
	@echo "  - Prometheus: http://localhost:9090"
	@echo "  - Grafana:    http://localhost:3000 (admin/admin)"
	@echo "  - MySQL:      localhost:3306"
	@echo "  - Redis:      localhost:6379"
	@echo "  - NATS:       localhost:4222"
	@echo "  - etcd:       localhost:2379"

# 停止所有服务
down:
	@echo "🛑 Stopping Resonance infrastructure..."
	@docker compose -f deploy/compose.yaml down
	@echo "✅ Infrastructure stopped!"

# 查看所有服务的日志
logs:
	@docker compose -f deploy/compose.yaml logs -f

# 查看具体服务日志 (用法: make logs-service SERVICE=mysql)
logs-service:
	@docker compose -f deploy/compose.yaml logs -f ${SERVICE}

# 查看服务状态
ps:
	@docker compose -f deploy/compose.yaml ps

# 重启所有服务
restart: down up

# 清理所有数据 (包括卷)
clean:
	@echo "🗑️ Cleaning Resonance infrastructure..."
	@docker compose -f deploy/compose.yaml down -v
	@echo "✅ Infrastructure cleaned!"
