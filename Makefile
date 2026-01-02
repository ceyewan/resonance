.PHONY: gen tidy infra infra-down dev image push deploy undeploy logs ps clean
include .env
export

# ============================================================================
# Environment Variables
# ============================================================================
WEB_HOST ?= localhost
WEB_PORT ?= 5173
RESONANCE_GATEWAY_HTTP_HOST ?= localhost
RESONANCE_GATEWAY_HTTP_PORT ?= 8080
RESONANCE_GATEWAY_WS_PORT ?= 8081
RESONANCE_GATEWAY_GRPC_PORT ?= 15091
RESONANCE_LOGIC_GRPC_PORT ?= 15090
RESONANCE_WEB_HTTP_PORT ?= 4173
RESONANCE_ETCD_PORT ?= 2379
RESONANCE_DB_PORT ?= 3306
RESONANCE_REDIS_PORT ?= 6379
RESONANCE_NATS_PORT ?= 4222
RESONANCE_PROMETHEUS_PORT ?= 9090
RESONANCE_GRAFANA_PORT ?= 3000

GATEWAY_HTTP_HOST ?= $(RESONANCE_GATEWAY_HTTP_HOST)
GATEWAY_HTTP_PORT ?= $(RESONANCE_GATEWAY_HTTP_PORT)
GATEWAY_WS_PORT ?= $(RESONANCE_GATEWAY_WS_PORT)
WEB_HTTP_PORT ?= $(RESONANCE_WEB_HTTP_PORT)
GATEWAY_URL ?= http://$(RESONANCE_GATEWAY_HTTP_HOST):$(RESONANCE_GATEWAY_HTTP_PORT)
GATEWAY_WS_URL ?= ws://$(RESONANCE_GATEWAY_HTTP_HOST):$(RESONANCE_GATEWAY_WS_PORT)/ws

# ============================================================================
# Docker Compose Configurations
# ============================================================================
COMPOSE_BASE := docker compose --env-file .env -f deploy/base.yaml
COMPOSE_STACK := docker compose --env-file .env -f deploy/base.yaml -f deploy/services.yaml

# ============================================================================
# 1. Code Generation & Dependencies
# ============================================================================
PROTO_FILES := $(shell find api/proto -name "*.proto")
GEN_TIMESTAMP := api/gen/.timestamp

gen: $(GEN_TIMESTAMP)

$(GEN_TIMESTAMP): $(PROTO_FILES) api/buf.yaml api/buf.gen.go.yaml api/buf.gen.connect.yaml api/buf.gen.ts.yaml
	@echo "🔧 Generating contract code..."
	@cd api && buf generate --template buf.gen.go.yaml
	@cd api && buf generate --template buf.gen.connect.yaml --path proto/gateway/v1/api.proto
	@cd api && buf generate --template buf.gen.ts.yaml --path proto/gateway/v1/api.proto --path proto/gateway/v1/packet.proto --path proto/common
	@mkdir -p api/gen && touch $(GEN_TIMESTAMP)
	@echo "✅ Code generation complete!"

tidy:
	@echo "🧹 Tidying go modules..."
	@go mod tidy

# ============================================================================
# 2. Local Development (Mode 1)
# ============================================================================
# Start Infrastructure (MySQL, Redis, Etcd, NATS...)
infra:
	@echo "🚀 Starting Infrastructure..."
	@$(COMPOSE_BASE) up -d
	@echo "✅ Infrastructure started!"

# Stop Infrastructure
infra-down:
	@echo "🛑 Stopping Infrastructure..."
	@$(COMPOSE_BASE) down
	@echo "✅ Infrastructure stopped!"

# Run All Services Locally (Requires infra started)
dev: gen
	@echo "🚀 Starting all services locally..."
	@trap 'echo ""; echo "🛑 Stopping all services..."; kill $$LOGIC_PID $$TASK_PID $$GATEWAY_PID $$WEB_PID 2>/dev/null; exit 0' INT TERM; \
	echo "📡 Starting Logic service..."; \
	RESONANCE_ENV=dev go run main.go -module logic & LOGIC_PID=$$!; \
	echo "   [Logic] PID: $$LOGIC_PID"; \
	echo "📡 Starting Task service..."; \
	RESONANCE_ENV=dev go run main.go -module task & TASK_PID=$$!; \
	echo "   [Task] PID: $$TASK_PID"; \
	echo "⏳ Waiting 2s..."; \
	sleep 2; \
	echo "🌐 Starting Gateway service..."; \
	RESONANCE_ENV=dev go run main.go -module gateway & GATEWAY_PID=$$!; \
	echo "   [Gateway] PID: $$GATEWAY_PID"; \
	echo "⏳ Waiting 2s..."; \
	sleep 2; \
	echo "🎨 Starting Web frontend..."; \
	cd web && VITE_API_BASE_URL=$(GATEWAY_URL) npm run dev -- --host $(WEB_HOST) --port $(WEB_PORT) & WEB_PID=$$!; \
	echo "   [Web] PID: $$WEB_PID"; \
	echo ""; \
	echo "✅ All services started!"; \
	echo "📊 Service URLs:"; \
	echo "  - Web:        http://$(WEB_HOST):$(WEB_PORT)"; \
	echo "  - Gateway:    $(GATEWAY_URL)"; \
	echo "  - Logic:      $(RESONANCE_LOGIC_GRPC_PORT)"; \
	echo ""; \
	echo "🔧 Press Ctrl+C to stop all services"; \
	wait

# ============================================================================
# 3. Production / Docker (Mode 2)
# ============================================================================
# Build Docker Image
image:
	@chmod +x scripts/build-push.sh
	@./scripts/build-push.sh local

# Push Docker Image
push:
	@chmod +x scripts/build-push.sh
	@./scripts/build-push.sh push

# Deploy to Docker (Uses DockerHub image by default)
deploy:
	@echo "🚀 Deploying to Docker (Production Image)..."
	@$(COMPOSE_STACK) up -d
	@echo "✅ Deployed!"
	@echo "📊 Service URLs:"

# Deploy Local Image (Uses resonance:local)
deploy-local:
	@echo "🚀 Deploying to Docker (Local Image)..."
	@RESONANCE_IMAGE=resonance:local $(COMPOSE_STACK) up -d
	@echo "✅ Deployed!"
	@echo "📊 Service URLs:"
	@echo "  - Web: http://localhost:$(WEB_HTTP_PORT)"

# Undeploy
undeploy:
	@echo "🛑 Stopping Docker deployment..."
	@$(COMPOSE_STACK) down
	@echo "✅ Stopped!"

# ============================================================================
# 4. Helpers
# ============================================================================
logs:
	@$(COMPOSE_STACK) logs -f

ps:
	@$(COMPOSE_STACK) ps

clean:
	@echo "🗑️ Cleaning..."
	@$(COMPOSE_STACK) down -v
	@echo "✅ Cleaned!"
