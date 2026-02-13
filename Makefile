# Resonance Makefile - 任务编排
# 所有配置统一在 .env 文件中管理

.PHONY: help gen tidy format format-go format-proto format-prettier lint lint-go lint-proto lint-prettier lint-web dev up down logs clean

# 默认目标：显示帮助
.DEFAULT_GOAL := help

# 加载 .env 文件（如果存在）
-include .env
export

# Docker Compose 命令
COMPOSE := docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml

# ============================================================================
# 帮助信息
# ============================================================================
help: ## 显示帮助信息
	@echo "Resonance 开发工具"
	@echo ""
	@echo "用法: make <target>"
	@echo ""
	@echo "常用命令:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# ============================================================================
# 代码生成
# ============================================================================
gen: ## 生成 protobuf 代码
	@echo "🔧 生成 protobuf 代码..."
	@cd api && buf generate --template buf.gen.go.yaml
	@cd api && buf generate --template buf.gen.connect.yaml --path proto/gateway/v1/api.proto
	@cd api && buf generate --template buf.gen.ts.yaml --path proto/gateway/v1/api.proto --path proto/gateway/v1/packet.proto --path proto/common
	@echo "✅ 代码生成完成"

tidy: ## 整理 Go 依赖
	@echo "🧹 整理 Go 依赖..."
	@go mod tidy
	@echo "✅ 完成"

format: format-go format-proto format-prettier ## 一键格式化 Go/Proto/TS/YAML/MD
	@echo "✅ 全量格式化完成"

format-go: ## 格式化 Go 代码（排除 api/gen）
	@echo "🔧 格式化 Go 代码..."
	@GO_FILES="$$(rg --files -g '*.go' -g '!api/gen/**')"; \
	if [ -n "$$GO_FILES" ]; then \
		echo "$$GO_FILES" | xargs gofmt -w; \
	fi

format-proto: ## 格式化 Proto 定义
	@echo "🔧 格式化 Proto..."
	@cd api && buf format -w proto

format-prettier: ## 格式化 TS/YAML/Markdown/JSON 等
	@echo "🔧 格式化 Prettier 支持的文件..."
	@prettier --write .

lint: lint-go lint-proto lint-prettier lint-web ## 一键执行 Go/Proto/Prettier/Web Lint
	@echo "✅ 全量 Lint 通过"

lint-go: ## Go 静态检查（golangci-lint）
	@echo "🔍 Go lint (golangci-lint)..."
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "❌ 未安装 golangci-lint，请先安装后重试"; \
		exit 1; \
	fi
	@golangci-lint run --config .golangci.yaml ./...

lint-proto: ## Proto lint 检查
	@echo "🔍 Buf lint..."
	@cd api && buf lint

lint-prettier: ## Prettier 格式检查
	@echo "🔍 Prettier check..."
	@prettier --check .

lint-web: ## 前端 ESLint 检查
	@echo "🔍 Web lint..."
	@cd web && npm run type-check
	@if [ -f web/eslint.config.js ] || [ -f web/eslint.config.mjs ] || [ -f web/eslint.config.cjs ] || [ -f web/.eslintrc ] || [ -f web/.eslintrc.js ] || [ -f web/.eslintrc.cjs ] || [ -f web/.eslintrc.json ] || [ -f web/.eslintrc.yaml ] || [ -f web/.eslintrc.yml ]; then \
		cd web && npm run lint; \
	else \
		echo "ℹ️  未检测到 ESLint 配置，已跳过 npm run lint"; \
	fi

# ============================================================================
# 本地开发（直接运行，不用 Docker）
# ============================================================================
dev: gen ## 本地开发模式（需要先启动基础设施）
	@echo "🚀 启动本地开发环境..."
	@echo "⚠️  请确保已运行: make up"
	@echo ""
	@trap 'echo ""; echo "🛑 停止所有服务..."; kill $$LOGIC_PID $$TASK_PID $$GATEWAY_PID $$WEB_PID 2>/dev/null; exit 0' INT TERM; \
	echo "📡 启动 Logic..."; \
	RESONANCE_ENV=dev go run main.go -module logic & LOGIC_PID=$$!; \
	echo "📡 启动 Task..."; \
	RESONANCE_ENV=dev go run main.go -module task & TASK_PID=$$!; \
	sleep 2; \
	echo "🌐 启动 Gateway..."; \
	RESONANCE_ENV=dev go run main.go -module gateway & GATEWAY_PID=$$!; \
	sleep 2; \
	echo "🎨 启动 Web..."; \
	cd web && npm run dev & WEB_PID=$$!; \
	echo ""; \
	echo "✅ 所有服务已启动"; \
	echo "📊 访问地址:"; \
	echo "  - Web:     http://localhost:5173"; \
	echo "  - Gateway: http://localhost:8080"; \
	echo ""; \
	echo "🔧 按 Ctrl+C 停止"; \
	wait

# ============================================================================
# Docker 部署
# ============================================================================
up: ## 启动所有服务（Docker）- 需要在 .env 中设置 RESONANCE_ENV=prod
	@chmod +x scripts/deploy-local.sh
	@./scripts/deploy-local.sh

down: ## 停止所有服务
	@echo "🛑 停止服务..."
	@$(COMPOSE) down
	@echo "✅ 已停止"

logs: ## 查看日志
	@$(COMPOSE) logs -f

clean: ## 清理所有数据（包括 volumes）
	@echo "🗑️  清理数据..."
	@$(COMPOSE) down -v
	@echo "✅ 已清理"
