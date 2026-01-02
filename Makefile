.PHONY: gen tidy build-gateway build-logic build-task build-web web-install web-dev web-build up down logs ps network-create dev-gateway dev-logic dev-task dev-web build-docker-gateway build-docker-logic build-docker-task dev dev-all
include .env
export

# ============================================================================
# Web 前端配置
# ============================================================================
# 前端开发服务器地址
WEB_HOST ?= localhost
WEB_PORT ?= 5173
# Gateway 地址（前端 API 和 WebSocket 连接地址）
GATEWAY_URL ?= http://localhost:8080

# ============================================================================
# 1. 生成代码 (使用 buf)
# ============================================================================
# 增量生成逻辑：仅当 proto 文件改变时才重新生成，避免 IDE 频繁重索引
PROTO_FILES := $(shell find api/proto -name "*.proto")
GEN_TIMESTAMP := api/gen/.timestamp

gen: $(GEN_TIMESTAMP)

$(GEN_TIMESTAMP): $(PROTO_FILES) api/buf.yaml api/buf.gen.go.yaml api/buf.gen.connect.yaml api/buf.gen.ts.yaml
	@echo "🔧 Generating contract code (incremental)..."
	@echo "  > Generating Go base + gRPC (All proto files)..."
	@cd api && buf generate --template buf.gen.go.yaml
	@echo "  > Generating ConnectRPC (Only gateway/v1/api.proto)..."
	@cd api && buf generate --template buf.gen.connect.yaml --path proto/gateway/v1/api.proto
	@echo "  > Generating TypeScript (gateway/v1/api.proto, gateway/v1/packet.proto, common)..."
	@cd api && buf generate --template buf.gen.ts.yaml --path proto/gateway/v1/api.proto --path proto/gateway/v1/packet.proto --path proto/common
	@mkdir -p api/gen && touch $(GEN_TIMESTAMP)
	@echo "✅ Code generation complete!"
	@echo ""
	@echo "📦 Generated structure:"
	@echo "  - gateway/v1/api.proto    → gRPC + ConnectRPC + TypeScript (客户端访问)"
	@echo "  - gateway/v1/push.proto   → gRPC only (Task → Gateway)"
	@echo "  - logic/v1/*.proto        → gRPC only (服务间调用)"
	@echo "  - common/*.proto          → TypeScript (共享类型)"
	@echo "  - gateway/v1/packet.proto → TypeScript (WebSocket 消息格式)"

# ============================================================================
# 2. 整理依赖
# ============================================================================
tidy:
	@echo "🧹 Tidying go modules..."
	@go mod tidy

# ============================================================================
# 3. 编译服务
# ============================================================================
build-gateway:
	@echo "🏗️ Building Gateway..."
	@go build -o bin/gateway main.go

build-logic:
	@echo "🏗️ Building Logic..."
	@go build -o bin/logic main.go

build-task:
	@echo "🏗️ Building Task..."
	@go build -o bin/task main.go

build-web:
	@echo "🏗️ Building Web Static Server..."
	@go build -o bin/web main.go

# ============================================================================
# 4. 开发环境运行
# ============================================================================
dev-gateway: gen
	@echo "🚀 Starting Gateway in DEV mode..."
	@RESONANCE_ENV=dev go run main.go -module gateway

dev-logic: gen
	@echo "🚀 Starting Logic in DEV mode..."
	@RESONANCE_ENV=dev go run main.go -module logic

dev-task: gen
	@echo "🚀 Starting Task in DEV mode..."
	@RESONANCE_ENV=dev go run main.go -module task

dev-web: web-build
	@echo "🚀 Starting Web static server..."
	@RESONANCE_ENV=dev go run main.go -module web

# ============================================================================
# 5. Web 前端相关命令
# ============================================================================

# 安装前端依赖
web-install:
	@echo "📦 Installing web dependencies..."
	@cd web && npm install
	@echo "✅ Web dependencies installed!"

# 启动前端开发服务器
web-dev: gen
	@echo "🚀 Starting web development server..."
	@echo "   Web:  http://$(WEB_HOST):$(WEB_PORT)"
	@echo "   API:  $(GATEWAY_URL)"
	@cd web && \
	VITE_API_BASE_URL=$(GATEWAY_URL) \
	npm run dev -- --host $(WEB_HOST) --port $(WEB_PORT)

# 构建前端生产版本
web-build: gen
	@echo "🏗️ Building web for production..."
	@echo "   API: $(GATEWAY_URL)"
	@cd web && \
	VITE_API_BASE_URL=$(GATEWAY_URL) \
	npm run build
	@echo "✅ Web build complete! Output: web/dist/"

# ============================================================================
# 6. 一键完成所有生成和依赖整理
# ============================================================================
all: gen tidy web-install

# ============================================================================
# 7. 强制清理并重新生成
# ============================================================================
gen-clean:
	@echo "🧹 Cleaning generated code..."
	@rm -rf api/gen
	@$(MAKE) gen

gen-force:
	@rm -f $(GEN_TIMESTAMP)
	@$(MAKE) gen

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

# ============================================================================
# 本地一键启动 (基础设施已通过 make up 启动后)
# ============================================================================

# 启动所有本地服务 (logic + task + gateway + web)
dev-all: gen
	@echo "🚀 Starting all Resonance services locally..."
	@echo ""
	@echo "📡 Starting Logic service..."
	@RESONANCE_ENV=dev go run main.go -module logic &
	LOGIC_PID=$!
	@echo "   [Logic] PID: $$LOGIC_PID"
	@echo ""
	@echo "📡 Starting Task service..."
	@RESONANCE_ENV=dev go run main.go -module task &
	TASK_PID=$!
	@echo "   [Task] PID: $$TASK_PID"
	@echo ""
	@echo "⏳ Waiting 2s for Logic/Task to initialize..."
	@sleep 2
	@echo ""
	@echo "🌐 Starting Gateway service..."
	@RESONANCE_ENV=dev go run main.go -module gateway &
	GATEWAY_PID=$!
	@echo "   [Gateway] PID: $$GATEWAY_PID"
	@echo ""
	@echo "⏳ Waiting 2s for Gateway to initialize..."
	@sleep 2
	@echo ""
	@echo "🎨 Starting Web frontend..."
	@cd web && VITE_API_BASE_URL=$(GATEWAY_URL) npm run dev &
	WEB_PID=$!
	@echo "   [Web] PID: $$WEB_PID"
	@echo ""
	@echo "✅ All services started!"
	@echo ""
	@echo "📊 Service URLs:"
	@echo "  - Web:        http://$(WEB_HOST):$(WEB_PORT)"
	@echo "  - Gateway:    $(GATEWAY_URL)"
	@echo "  - Logic:      $(RESONANCE_LOGIC_SERVICE_NAME)"
	@echo "  - Task:       $(RESONANCE_TASK_SERVICE_NAME)"
	@echo ""
	@echo "🔧 Press Ctrl+C to stop all services"
	@trap "echo ''; echo '🛑 Stopping all services...'; kill $$LOGIC_PID $$TASK_PID $$GATEWAY_PID $$WEB_PID 2>/dev/null; exit 0" INT TERM
	@wait

# 仅启动后端服务 (logic + task + gateway)，不启动 web
dev: gen
	@echo "🚀 Starting backend services locally..."
	@echo ""
	@echo "📡 Starting Logic service..."
	@RESONANCE_ENV=dev go run main.go -module logic &
	LOGIC_PID=$!
	@echo "   [Logic] PID: $$LOGIC_PID"
	@echo ""
	@echo "📡 Starting Task service..."
	@RESONANCE_ENV=dev go run main.go -module task &
	TASK_PID=$!
	@echo "   [Task] PID: $$TASK_PID"
	@echo ""
	@echo "⏳ Waiting 2s for Logic/Task to initialize..."
	@sleep 2
	@echo ""
	@echo "🌐 Starting Gateway service..."
	@RESONANCE_ENV=dev go run main.go -module gateway &
	GATEWAY_PID=$!
	@echo "   [Gateway] PID: $$GATEWAY_PID"
	@echo ""
	@echo "✅ Backend services started!"
	@echo ""
	@echo "📊 Service endpoints:"
	@echo "  - Gateway HTTP:  $(GATEWAY_URL)"
	@echo "  - Gateway WS:    ws://$(RESONANCE_GATEWAY_DEV_HOST):$(RESONANCE_GATEWAY_PORT)/ws"
	@echo "  - Logic:         $(RESONANCE_LOGIC_SERVICE_NAME)"
	@echo "  - Task:          $(RESONANCE_TASK_SERVICE_NAME)"
	@echo ""
	@echo "🔧 Press Ctrl+C to stop all services"
	@trap "echo ''; echo '🛑 Stopping backend services...'; kill $$LOGIC_PID $$TASK_PID $$GATEWAY_PID 2>/dev/null; exit 0" INT TERM
	@wait
