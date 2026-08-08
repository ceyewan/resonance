# Resonance Makefile - 任务编排
# 所有配置统一在 .env 文件中管理

.PHONY: help gen tidy format format-go format-proto format-prettier format-markdown lint lint-go lint-security lint-proto lint-prettier lint-markdown lint-web test test-go init dev up-infra down-infra logs-infra up update-local up-prod rollback-agent-validate down down-prod logs logs-prod clean

# 默认目标：显示帮助
.DEFAULT_GOAL := help

# 加载 .env 文件（如果存在）
-include .env
export

# Docker Compose 命令
COMPOSE_INFRA := docker compose --env-file .env -p resonance -f deploy/base.yaml
COMPOSE := docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml
COMPOSE_PROD := docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.prod.yaml --profile production
PRETTIER := ./tools/node_modules/.bin/prettier
MARKDOWNLINT := ./tools/node_modules/.bin/markdownlint-cli2
GOLANGCI_LINT_VERSION ?= 2.12.2

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
	@cd api && buf generate --template buf.gen.connect.yaml --path proto/gateway/v1/auth.proto --path proto/gateway/v1/session.proto --path proto/gateway/v1/agent_approval.proto
	@cd api && buf generate --template buf.gen.ts.yaml --path proto/gateway/v1/auth.proto --path proto/gateway/v1/session.proto --path proto/gateway/v1/agent_approval.proto --path proto/gateway/v1/packet.proto --path proto/common
	@echo "✅ 代码生成完成"

tidy: ## 整理 Go 依赖
	@echo "🧹 整理 Go 依赖..."
	@go mod tidy
	@echo "✅ 完成"

format: format-go format-proto format-prettier format-markdown ## 一键格式化 Go/Proto/TS/YAML/MD
	@echo "✅ 全量格式化完成"

format-go: ## 现代化并格式化 Go 代码（go fix modernize + gofmt + goimports）
	@echo "🔧 现代化并格式化 Go 代码..."
	@if ! command -v goimports >/dev/null 2>&1; then \
		echo "❌ 未安装 goimports，请先执行: go install golang.org/x/tools/cmd/goimports@latest"; \
		exit 1; \
	fi
	@GO_PACKAGES="$$(go list ./... | grep -v '^github.com/ceyewan/resonance/api/gen' || true)"; \
	if [ -n "$$GO_PACKAGES" ]; then \
		echo "$$GO_PACKAGES" | xargs go fix; \
	fi
	@find . -name '*.go' -not -path './api/gen/*' -not -path './node_modules/*' -not -path './genesis/*' -print0 \
		| xargs -0 gofmt -s -w
	@find . -name '*.go' -not -path './api/gen/*' -not -path './node_modules/*' -not -path './genesis/*' -print0 \
		| xargs -0 goimports -local github.com/ceyewan/resonance -w

format-proto: ## 格式化 Proto 定义
	@echo "🔧 格式化 Proto..."
	@cd api && buf format -w proto

format-prettier: ## 格式化 TS/YAML/JSON/CSS 等
	@echo "🔧 格式化 Prettier 支持的文件..."
	@if [ ! -e "$(PRETTIER)" ]; then \
		echo "❌ 未安装 repo 级前端工具，请先执行: cd tools && npm ci"; \
		exit 1; \
	fi
	@$(PRETTIER) --write .

format-markdown: ## 自动修复可修复的 Markdown 规范问题
	@echo "🔧 修复可自动处理的 Markdown 问题..."
	@if [ ! -e "$(MARKDOWNLINT)" ]; then \
		echo "❌ 未安装 repo 级前端工具，请先执行: cd tools && npm ci"; \
		exit 1; \
	fi
	@$(MARKDOWNLINT) --fix

lint: lint-go lint-proto lint-prettier lint-markdown lint-web ## 一键执行 Go/Proto/Prettier/Markdown/Web Lint
	@echo "✅ 全量 Lint 通过"

lint-go: ## Go 静态检查（golangci-lint）
	@echo "🔍 Go lint (golangci-lint)..."
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "❌ 未安装 golangci-lint，请先执行: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANGCI_LINT_VERSION)"; \
		exit 1; \
	fi
	@if ! golangci-lint version 2>/dev/null | grep -Fq "version $(GOLANGCI_LINT_VERSION)"; then \
		echo "❌ golangci-lint 版本不匹配，需要 $(GOLANGCI_LINT_VERSION)"; \
		echo "   安装命令: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANGCI_LINT_VERSION)"; \
		exit 1; \
	fi
	@golangci-lint run --config .golangci.yaml ./...

lint-security: ## Go 漏洞扫描（govulncheck）
	@echo "🔍 Go vulnerability check (govulncheck)..."
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "❌ 未安装 govulncheck，请先执行: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
		exit 1; \
	fi
	@govulncheck ./...

lint-proto: ## Proto lint 检查
	@echo "🔍 Buf lint..."
	@cd api && buf lint

lint-prettier: ## Prettier 格式检查（TS/YAML/JSON/CSS 等）
	@echo "🔍 Prettier check..."
	@if [ ! -e "$(PRETTIER)" ]; then \
		echo "❌ 未安装 repo 级前端工具，请先执行: cd tools && npm ci"; \
		exit 1; \
	fi
	@$(PRETTIER) --check .

lint-markdown: ## Markdown lint 检查
	@echo "🔍 Markdown lint..."
	@if [ ! -e "$(MARKDOWNLINT)" ]; then \
		echo "❌ 未安装 repo 级前端工具，请先执行: cd tools && npm ci"; \
		exit 1; \
	fi
	@$(MARKDOWNLINT)

lint-web: ## 前端 ESLint 检查
	@echo "🔍 Web lint..."
	@cd web && npm run type-check
	@if [ -f web/eslint.config.js ] || [ -f web/eslint.config.mjs ] || [ -f web/eslint.config.cjs ] || [ -f web/.eslintrc ] || [ -f web/.eslintrc.js ] || [ -f web/.eslintrc.cjs ] || [ -f web/.eslintrc.json ] || [ -f web/.eslintrc.yaml ] || [ -f web/.eslintrc.yml ]; then \
		cd web && npm run lint; \
	else \
		echo "ℹ️  未检测到 ESLint 配置，已跳过 npm run lint"; \
	fi

test: test-go ## 执行测试
	@echo "✅ 全量测试通过"

test-go: ## Go 测试
	@echo "🧪 Go test..."
	@go test ./...

# ============================================================================
# 数据库初始化
# ============================================================================
init: ## 初始化数据库（建表 + 种子数据，幂等可重复执行）
	@echo "🔧 初始化数据库..."
	@go run main.go -module init
	@echo ""

# ============================================================================
# 本地开发（直接运行，不用 Docker）
# ============================================================================
dev: ## 本地开发模式（先执行 make up-infra，再直跑业务服务）
	@echo "🚀 启动本地开发环境..."
	@echo "⚠️  请先执行: make up-infra"
	@echo "⚠️  不要先执行 make up，否则会与本地直跑端口冲突"
	@echo ""
	@trap 'echo ""; echo "🛑 停止所有服务..."; kill $$LOGIC_PID $$TASK_PID $$GATEWAY_PID $$WEB_PID 2>/dev/null; exit 0' INT TERM; \
	echo "📡 启动 Logic..."; \
	go run main.go -module logic & LOGIC_PID=$$!; \
	echo "📡 启动 Task..."; \
	go run main.go -module task & TASK_PID=$$!; \
	sleep 2; \
	echo "🌐 启动 Gateway..."; \
	go run main.go -module gateway & GATEWAY_PID=$$!; \
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
up-infra: ## 启动基础设施（postgres/redis/nats/etcd）
	@echo "🚀 启动基础设施..."
	@$(COMPOSE_INFRA) up -d
	@echo "✅ 基础设施已启动"

down-infra: ## 停止基础设施
	@echo "🛑 停止基础设施..."
	@$(COMPOSE_INFRA) down
	@echo "✅ 基础设施已停止"

logs-infra: ## 查看基础设施日志
	@$(COMPOSE_INFRA) logs -f

up: ## 启动所有服务（Docker）
	@chmod +x deploy/scripts/deploy-local.sh
	@./deploy/scripts/deploy-local.sh

update-local: ## 重新构建并更新本地 Docker 部署
	@chmod +x deploy/scripts/update-local.sh
	@./deploy/scripts/update-local.sh

up-prod: ## 启动生产配置（Caddy 反代，不暴露业务端口）
	@chmod +x deploy/scripts/deploy-production.sh
	@./deploy/scripts/deploy-production.sh latest

rollback-agent-validate: ## 只校验 Agent 回滚的 control/runtime 不变 digest 组合
	@./deploy/scripts/rollback-agent.sh --validate-only

down: ## 停止所有服务
	@echo "🛑 停止服务..."
	@$(COMPOSE) down
	@echo "✅ 已停止"

down-prod: ## 停止生产配置服务
	@echo "🛑 停止生产服务..."
	@$(COMPOSE_PROD) down
	@echo "✅ 已停止"

logs: ## 查看日志
	@$(COMPOSE) logs -f

logs-prod: ## 查看生产配置日志
	@$(COMPOSE_PROD) logs -f

clean: ## 清理所有数据（包括 volumes）
	@echo "🗑️  清理数据..."
	@$(COMPOSE) down -v
	@echo "✅ 已清理"
