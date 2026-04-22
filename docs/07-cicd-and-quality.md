# CI/CD 与工程质量

> 本文档描述 Resonance 的代码质量门禁、测试分层策略和工程规范。阅读完本文后，应该能回答三个问题：提交前必须通过哪些检查；测试体系如何分层；以及 CI 流水线的执行策略是什么。

---

## 1. 质量门禁

所有代码变更在提交前必须通过以下检查，这是 `CLAUDE.md` 中明确要求的前置条件：

```bash
make format && make lint
```

这两条命令覆盖了 Go、Proto、TypeScript/YAML/JSON、Markdown 四个维度的格式化和静态检查。

---

## 2. 格式化（`make format`）

格式化分四个子目标，统一通过 `make format` 一键执行：

| 子目标 | 工具 | 覆盖范围 |
| ------ | ---- | -------- |
| `format-go` | `go fix modernize` + `gofmt -s` + `goimports` | 所有 Go 文件（排除 `api/gen/`） |
| `format-proto` | `buf format -w` | `api/proto/` 下所有 `.proto` 文件 |
| `format-prettier` | Prettier | TS/YAML/JSON/CSS 等 |
| `format-markdown` | markdownlint-cli2 `--fix` | 所有 Markdown 文件 |

`goimports` 使用 `-local github.com/ceyewan/resonance` 参数，确保本地包的 import 分组与标准库和第三方包分开。

前端工具（Prettier、markdownlint）依赖 `tools/` 目录下的 node_modules，首次使用前需要执行 `cd tools && npm ci`。

---

## 3. 静态检查（`make lint`）

Lint 同样分四个子目标：

| 子目标 | 工具 | 覆盖范围 |
| ------ | ---- | -------- |
| `lint-go` | golangci-lint v1.64.8 | Go 代码静态分析 |
| `lint-proto` | `buf lint` | Proto 定义规范检查 |
| `lint-prettier` | Prettier `--check` | 格式一致性检查 |
| `lint-markdown` | markdownlint-cli2 | Markdown 规范检查 |
| `lint-web` | TypeScript `type-check` + ESLint | 前端类型与规范检查 |

golangci-lint 的规则配置在 `.golangci.yaml`，buf 的规则配置在 `api/buf.yaml`。

安全扫描（`make lint-security`）使用 `govulncheck` 检查已知漏洞，按需执行，不进入日常门禁。

---

## 4. 测试分层

测试体系分四层，优先级从高到低：

### 4.1 单元测试

验证纯业务规则，不依赖外部基础设施。主要覆盖：

- `logic/service/`：权限校验、事件生成、序列号逻辑
- `task/dispatcher/`：事件分发、Inbox 构造、推送目标计算
- `gateway/transport/ws/`：WS 编解码、连接管理

单元测试使用手写 Fake 隔离依赖，不引入 Mock 生成工具。`testify/require` 用于关键前置条件断言，`testify/assert` 用于非关键字段校验。

### 4.2 组件测试

验证单个服务在真实依赖下的行为，使用 Testcontainers 自动拉起 PostgreSQL、Redis、NATS 容器：

- `repo/*_test.go`：数据层读写、索引、并发写入、唯一约束
- `logic/integration/`：Logic gRPC 服务完整链路（注册→建会话→发消息）
- `task/integration/`：Task 消费 MQEvent → Inbox 落库
- `gateway/integration/`：Push gRPC → WS 投递最后一跳

### 4.3 集成测试

验证多服务联调的黄金链路，位于 `test/integration/`：

- `message_delivery_test.go`：在线消息实时投递（Gateway + Logic + Task + WebSocket）
- `offline_sync_test.go`：离线消息补偿（Task 落 Inbox → 重连后 PullInboxDelta）
- `read_receipt_test.go`：已读回执与未读数变化

集成测试使用 Testcontainers 启动真实基础设施，进程内组装多个服务组件，不依赖浏览器前端。

### 4.4 端到端与压测

验证完整用户场景和系统容量，不阻塞日常 PR：

- `test/e2e/`：贴近用户行为的冒烟测试
- `test/load/`：Go 压测客户端，直接复用 proto 和 WS 协议

压测分三档：冒烟（10 用户）、中等规模（100 用户）、场景压测（单大群/多单聊/离线补偿）。

---

## 5. 测试执行

```bash
# 全量测试（包含 Testcontainers，需要 Docker daemon）
make test

# 单个测试
go test ./repo/ -run TestMessageRepo_SaveInboxBatch

# 启用数据竞争检测
go test -race ./...

# 单次执行（不进入 watch 模式）
go test -count=1 ./...
```

`repo/` 层测试通过 Testcontainers 自动拉起 `postgres:17-alpine` 和 `redis:7.2-alpine`，Docker 不可用时测试会跳过并给出明确原因。

---

## 6. 代码生成

Proto 代码生成通过 `make gen` 执行，生成 Go 和 TypeScript 两套代码：

```bash
make gen   # 生成 api/gen/go/ 和 web/src/gen/
make tidy  # 整理 Go 依赖
```

生成的代码位于 `api/gen/`，不应手动修改。Proto 定义变更后必须重新执行 `make gen` 并提交生成产物。

---

## 7. 分支与提交规范

分支命名：`<type>/<description>`，例如 `feat/recall-event`、`fix/inbox-cursor`。

提交信息：`<type>(<scope>): <subject>`，中文祈使语气，首字母小写，无句号。

常用 scope：`logic` / `gateway` / `task` / `web` / `api` / `docs`。

提交类型：`feat`（新功能）/ `fix`（修复）/ `refactor`（重构）/ `docs`（文档）/ `chore`（工程）。

---

## 8. 文档更新要求

架构变更时必须同步更新对应文档：

- 协议变更（新增 proto 字段或 RPC）→ 更新 `01-protocol.md`
- 表结构变更 → 更新 `02-database.md`
- 服务职责边界变更 → 更新对应服务文档（`10/11/12-*.md`）
- 重大架构决策 → 在 `adr/` 目录新增 ADR 文档

---

## 9. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `06-deployment.md` | 部署拓扑与环境配置 |
| `43-testing-strategy.md` | 测试策略的完整展开 |
| `40-developer-onboarding.md` | 新开发者本地启动指南 |

---

## 10. 小结

Resonance 的工程质量门禁以 `make format && make lint` 为最低要求，覆盖 Go、Proto、前端和文档四个维度。测试体系分四层，单元测试和集成测试阻塞 PR，端到端和压测定时执行。所有架构变更必须同步更新文档，保证文档与代码的一致性。
