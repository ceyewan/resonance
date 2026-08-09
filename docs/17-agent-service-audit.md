# Agent Service 审计与整改报告

> 审计与整改日期：2026-08-09
> 范围：Pilot、隔离 Runtime、Pi Bridge、Tool Broker、Agent Session、预算、审批、前端、Compose 与真实 Provider 链路
> 结论：核心架构质量较高，本轮 P1/P2 接入问题已整改；DashScope 普通文本对话链路已通过真实端到端验证。建议先小流量启用，再补业务 Eval、故障注入和老用户批量回填。

## 1. 总体评价

Agent Service 不是简单的 LLM HTTP 转发。当前实现已有以下生产级基础：

- durable Agent Run、租约、重试与恢复；
- Pi Session staging、校验和、CAS 提交与失效重建；
- 流式临时事件与唯一 durable final message；
- profile/tenant/workload 多层授权；
- 隔离的 Node/Pi Runtime、私有 UDS、短期 Capability；
- Tool Broker、IAM 审批、冻结参数和 exactly-once receipt；
- Token、成本和 Provider 调用次数预算；
- 精确域名、TLS SNI/ALPN 校验的 Provider CONNECT 出口。

整改后的判断：设计适合由平台为所有用户提供默认 Bot，接入路径已经比较顺畅。它不是 BYOK；普通用户不需要也不能在聊天请求中传 Provider Key。

## 2. Provider 与密钥模式

当前采用平台托管凭证：

- Provider 固定为 `dashscope`，模型固定为 `qwen3.8-max`；
- OpenAI-compatible Base URL 和模型由可信服务端配置读取；
- `DASHSCOPE_API_KEY` 仅注入 `pilot-runtime` 与 `pilot-iam-admin-runtime`；
- Pilot control、Logic、Gateway、Task、Init、Web 和 Egress Proxy 均看不到 Key；
- Runtime 只能经精确允许的百炼 hostname 出口；
- Key 不进入 YAML、镜像、前端 Runtime Config、Session、日志或审计 Observation。

Compose 展开后的密钥可见性已经用机器检查确认。审计过程中还发现公共 `env_file` 会把整份 `.env` 注入普通服务；现已移除，所有服务改为显式环境变量白名单。

未来若支持 BYOK，必须另行设计 tenant 级加密存储、轮换、审计、账单归属和数据政策，不能把用户 Key 当作聊天字段透传。

## 3. 发现与整改

| 优先级 | 原问题 | 整改结果 |
| --- | --- | --- |
| P1 | 默认 Tenant 无 Budget Policy，Provider 前 fail closed | Init 幂等创建显式启用的默认策略；已有策略不覆盖；Pilot 启动时校验策略存在且启用 |
| P1 | 撤回消息正文可能重新进入 Agent 历史 | 公共 Message 使用显式 `recalled` tombstone；正文清空；Agent 历史跳过旧 tombstone，并拒绝已撤回的当前源事件 |
| P1 | Tool Broker 把业务结果一律写为 `ok` | 增加 `execution_pending`/`executed`；只有 durable receipt 已提交才能报告执行成功；非 2xx Bridge 响应也不再伪装成功 |
| P1 | Repo claim 测试 fixture 与生产排序矛盾 | 修正测试时间顺序并通过全量 PostgreSQL 集成测试 |
| P1 | DashScope 只写入环境文件但未进入 Pi Provider | Bridge 注册可信 `openai-completions` Provider，固定模型能力、Key 引用与保守成本上界 |
| P1 | Qwen reasoning payload 使用 `max_completion_tokens`，预算钩子只认 `max_tokens` | 同时支持两个标准字段；二者同时出现或均缺失时 fail closed；新增真实回归覆盖 |
| P1 | Docker/VPN 把目标域名解析到 RFC 2544 `198.18.0.0/15`，本地 Egress 返回 502 | 增加仅本地 Compose 启用的合成地址兼容；生产覆盖强制关闭；hostname、SNI、端口和 TLS 校验保持不变 |
| P2 | 注册后没有默认 Bot 会话 | Register、Login 触发 best-effort 幂等 provisioning；GetSessionList 做 lazy repair 并记录固定维度指标；只自动创建 `user-assistant`，不创建 IAM Bot |
| P2 | Web 丢弃 `kind/profile/version` | Dexie v2 迁移并持久化元数据；会话栏显示 BOT，详情页显示 Profile 与版本 |
| P2 | `.env` 可能跨越安全边界 | 移除公共 `env_file`，Compose 和 Runtime 都使用最小变量白名单 |
| P2 | `make update-local` 未重建 Pilot、Runtime 和 Egress Proxy | 更新脚本现在覆盖完整 Agent 拓扑，避免源码已修复但本地容器仍运行旧镜像 |

默认 Bot 使用一个受保护的全局服务身份 `resonance-agent`。每个用户获得确定性的私有会话 ID，而不是创建一批独立 Bot 账号。Repo 唯一约束和确定性 ID 使注册、登录和 lazy repair 可以安全重复执行。
Bot 会话 provisioning 不属于注册事务：失败不会回滚已创建的用户，而是记录
`logic_default_agent_session_provision_total`，并由后续 Login/GetSessionList 最终一致地修复。

## 4. 真实验证结果

### 4.1 不需要 LLM Key 的门禁

- `go test ./...`：通过，包括 Docker/Testcontainers 集成测试；
- `go vet ./...`：通过；
- Bridge：18 个测试和 TypeScript typecheck 通过；
- Web：47 个测试、typecheck、lint、production build 通过；
- Egress Proxy：严格 DNS、CONNECT、TLS ClientHello 和本地合成地址模式测试通过；
- Compose 配置与 Key 隔离检查通过。

缺少 Provider Key 不应让普通 correctness 测试失败。单元、协议、Repo 和隔离测试必须继续使用 Fake/离线契约。

### 4.2 需要真实 Key 的门禁

已使用被 Git 忽略的本地 `.env` 执行：

1. 裸 OpenAI-compatible Chat Completions：成功，模型返回 `resonance-smoke-ok`，usage 为 prompt 69、completion 35、total 104；
2. 固定 Pi + 自定义 DashScope Provider：成功；
3. 同一 Runtime 镜像经严格 CONNECT Proxy：成功；
4. 核心文本业务链 Smoke E2E：成功。

Smoke E2E 覆盖：

```text
Register
  -> 自动创建 pinned user-assistant 会话
  -> Gateway WebSocket ACK
  -> Logic durable ChatEvent / Agent Run
  -> Pilot budget reservation
  -> 隔离 Runtime / Pi / trusted Bridge
  -> Egress Proxy / DashScope qwen3.8-max
  -> StreamBegin / StreamChunk / StreamEnd(STOP)
  -> Bot durable final message
```

可重复的真实测试位于 `test/live/agent_service_test.go`，默认跳过以避免付费调用：

```bash
RESONANCE_LIVE_AGENT_E2E=1 \
  go test ./test/live -run TestAgentServiceDashScope -v -count=1
```

测试默认只允许访问 loopback 部署；若显式设置 `RESONANCE_LIVE_ALLOW_REMOTE=1`，必须使用
专用测试环境和可清理的测试 Tenant。它会创建持久测试用户并消耗真实 Provider 额度，不应直接指向生产默认 Tenant。

轻量模型矩阵运行在已由 `make update-local` 启动的本地环境上，会依次切换 control/runtime、执行真实
E2E，并在退出时恢复 `.env` 中的默认模型：

```bash
bash test/live/run_agent_model_matrix.sh qwen3.7-plus qwen3.7-flash
```

最终镜像重新部署后，本次实测约 3 秒完成，并同时验证默认 Bot、流式内容和最终消息。

### 4.3 轻量模型真实矩阵

同一按量付费业务空间 endpoint 还验证了 `qwen3.7-plus` 和 `qwen3.7-flash`。裸 API 在
`max_completion_tokens=64` 时均把大部分额度用于 reasoning，并以 `length` 截断；提高到
512 后均完整返回预期文本并以 `stop` 结束。因此真实门禁不能只检查 HTTP 200，还必须检查
finish reason 和非空正文。

完整 Agent 矩阵结果：

| 模型 | 默认 Bot/流式/durable final | 同 Session 多轮记忆 | `get_my_profile` Tool |
| --- | --- | --- | --- |
| `qwen3.7-plus` | 通过，约 5.25 秒 | 通过 | 通过；组合场景约 12.49 秒 |
| `qwen3.7-flash` | 通过，约 2.71 秒 | 通过 | 通过；组合场景约 12.15 秒 |

矩阵脚本会检查模型确实同时进入 Pilot control 与隔离 Runtime，并在成功、失败或中断退出时恢复
`.env` 中的默认模型。本轮结束后已恢复 `qwen3.8-max`。

## 5. 当前能力

### 普通用户默认 Bot

- 文字问答与连续会话；
- 流式输出，断线后以 Inbox/History 恢复最终消息；
- 读取并压缩安全的权威会话历史；
- 查询本人资料 `get_my_profile`；
- 受到 tenant、profile、预算、运行时和网络边界保护。

### IAM 管理 Bot

IAM Profile 不会默认发给普通用户，只有具备相应 role/scope 的用户可以显式创建。它目前支持：

- 查询或列出 Tenant 用户；
- 生成成员状态变更 dry-run；
- expected version、防并发覆盖；
- 双人审批；
- exactly-once receipt 下的 ACTIVE/DISABLED 变更。

### 尚未提供

- 图片、文件、语音理解；
- Web Search、知识库/RAG；
- 邮件、日历和第三方 SaaS；
- 任意 Shell、文件系统或 HTTP Tool；
- 主动消息、建群和通用 IM 管理；
- 面向最终用户的 BYOK；
- 除当前 Profile 工具集以外的开放式插件市场。

## 6. 剩余风险与上线建议

1. 当前只证明了真实普通文本链路，不等于业务回答质量已经充分评测。仍需运行版本化业务 Eval，覆盖 Tool 选择、拒绝、审批和 IAM receipt。
2. `qwen3.8-max` 当前通过按量付费业务空间 endpoint 使用，内部预算价格暂取保守上界，不是最终账单费率；上线前要用百炼账单校准。
3. 需要补充 401、429、5xx、超时、Abort、Session resume/compaction 和断线重连的真实故障注入。
4. 现有 ACTIVE 老用户仍需要一次可重试 backfill；新注册、登录和拉取列表已能自修复。
5. Profile 版本变更必须递增版本，并明确旧会话 drain、只读归档或重建策略。
6. 本地 RFC 2544 DNS 兼容只能通过 `services.local.yaml` 用于 Docker Desktop/VPN；基础与生产配置均关闭，生产不得绕过公共地址校验。

建议发布顺序：开发/测试 Tenant → 内部用户 → 小比例普通用户 → 完成一个业务窗口的错误率、首 token、成本和 Tool 拒绝观察后再扩大。当前不建议在没有业务 Eval 和故障注入证据的情况下直接全量开放 IAM 变更能力。
