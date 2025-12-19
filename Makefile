.PHONY: gen tidy build-gateway build-logic build-task

# 1. 生成代码 (使用 buf)
gen:
	@echo "🔧 Generating contract code..."
	@cd im-contract && rm -rf gen
	@echo "  > Generating Go (All)..."
	@cd im-contract && buf generate --template buf.gen.go.yaml
	@echo "  > Generating TypeScript (Filtered)..."
	@cd im-contract && buf generate --template buf.gen.ts.yaml --path proto/gateway --path proto/common

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