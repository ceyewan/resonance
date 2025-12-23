.PHONY: gen tidy build-gateway build-logic build-task up down logs ps network-create

# 1. 生成代码 (使用 buf)
gen:
	@echo "🔧 Generating contract code..."
	@cd im-api && rm -rf gen
	@echo "  > Generating Go base + gRPC (All proto files)..."
	@cd im-api && buf generate --template buf.gen.go.yaml
	@echo "  > Generating ConnectRPC (Only gateway/v1/api.proto)..."
	@cd im-api && buf generate --template buf.gen.connect.yaml --path proto/gateway/v1/api.proto
	@echo "  > Generating TypeScript (Only gateway/v1/api.proto and common)..."
	@cd im-api && buf generate --template buf.gen.ts.yaml --path proto/gateway/v1/api.proto --path proto/common
	@echo "✅ Code generation complete!"
	@echo ""
	@echo "📦 Generated structure:"
	@echo "  - gateway/v1/api.proto    → gRPC + ConnectRPC + TypeScript (客户端访问)"
	@echo "  - gateway/v1/push.proto   → gRPC only (Task → Gateway)"
	@echo "  - logic/v1/*.proto        → gRPC only (服务间调用)"
	@echo "  - common/*.proto          → TypeScript (共享类型)"

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

# 5. 一键完成所有生成和依赖整理
all: gen tidy

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