# Loop 04 验证记录：Agent 审批、执行与审计状态基础

- 日期：2026-08-09
- 范围：`model.AgentApproval`、`model.AgentToolExecution`、`model.AgentAuditLog` 与对应 Repo
- 目标：建立 Phase 8 可持久恢复的审批、冻结参数执行和追加式审计事实，不实现 IAM Mutation 或虚构权限模型。
- 结论：本切片通过；后续 Logic/Pilot RPC、权限双重再验证、审批 Outbox 与 IAM 下游 exactly-once receipt 已在 `09-agent-iam-mutation.md` 接通。

## 1. 已实现语义

三个聚合都强制携带 `tenant_id`、`run_id` 和稳定业务 ID：

- `t_agent_approval` 属于 Logic 事实，只保存 `call_id + args_hash`、脱敏参数摘要、过期时间、决定、决定人和撤销/过期状态，不保存冻结参数正文。
- `t_agent_tool_execution` 属于 Pilot/Tool Broker 事实，保存不可变参数安全引用、Tool/Schema 版本、执行幂等键、结果安全引用/Hash 和下游操作 ID。
- `t_agent_audit_log` 属于 Pilot 事实，以调用方 `audit_id` 幂等追加，并在每个 `(tenant_id, run_id)` 内生成连续 Sequence 与 SHA-256 `prev_hash → entry_hash` 链。

Approval 与 Execution 都以 `(tenant_id, call_id)` 唯一；Execution 另外以 `(tenant_id, idempotency_key)` 唯一。创建重投在不可变字段完全一致时返回原记录，不一致时 fail closed。状态转换在 PostgreSQL 行锁内验证允许边、`args_hash` 和版本，只更新状态相关字段；重复同一转换返回原记录，不重复增加 Attempt 或改写决定/结果。

当前允许的 Approval 转换：

```text
PENDING  → APPROVED | REJECTED | REVOKED | EXPIRED
APPROVED → REVOKED | EXPIRED
```

当前允许的 Tool Execution 转换：

```text
PREPARED        → READY | FAILED_FINAL | CANCELLED
READY           → EXECUTING | FAILED_FINAL | CANCELLED
EXECUTING       → SUCCEEDED | FAILED_RETRYABLE | FAILED_FINAL | CANCELLED
FAILED_RETRYABLE → EXECUTING | FAILED_FINAL | CANCELLED
```

所有其他跃迁均返回 `ErrAgentInvalidTransition`。批准发生在 `expires_at` 之后时返回 `ErrAgentApprovalExpired`；过期状态只能在到达截止时间后写入。

## 2. PostgreSQL 并发与篡改验证

实测覆盖：

- 12 个 goroutine 并发创建同一审批，只有一个 `Created=true`；同 `call_id` 替换 `args_hash` 被拒绝。
- Tool Execution 相同创建重投返回原记录；同一执行幂等键绑定到另一 `call_id` 被拒绝。
- `PREPARED → READY → EXECUTING → FAILED_RETRYABLE → EXECUTING → SUCCEEDED` 全链路中 Attempt 恰好为 2；每一步重复调用不二次变更。
- 16 个 goroutine 并发追加同一 Run 审计，PostgreSQL transaction advisory lock 保证 Sequence 连续且 Hash 链可验证。
- 相同 `audit_id` 同内容重投返回原 Entry；替换摘要被拒绝。
- 绕过 Repo 直接修改历史审计摘要后，`VerifyAgentAuditChain` 返回 `ErrAgentAuditChainBroken`。
- 相同 `call_id` 可在不同 Tenant 独立存在，所有读取与转换 API 都要求显式 `tenant_id`。

## 3. 可复现命令

```bash
env GOCACHE=/tmp/resonance-agent-state-go-cache \
  go test ./repo \
  -run 'TestAgent(Approval|ToolExecution|Audit|State)' \
  -count=1 -v

env GOCACHE=/tmp/resonance-agent-state-race-cache \
  go test -race ./repo \
  -run 'TestAgent(Approval|ToolExecution|Audit|State)' \
  -count=1

env GOCACHE=/tmp/resonance-agent-state-go-cache \
  go test ./repo -count=1

env GOCACHE=/tmp/resonance-agent-state-go-cache \
  go vet ./repo/...
```

上述命令均通过；数据库测试使用 PostgreSQL 17 Testcontainer，未把 Docker 不可用导致的 Skip 计作成功证据。

## 4. 明确未实现的边界

- 没有 IAM Mutation、通用 SQL/Shell/HTTP Tool 或任何真实副作用。
- 没有把 `SessionMember.Role` 冒充系统权限；本切片不做授权判断。
- 没有实现 `CreateToolApproval/DecideToolApproval` RPC、ChatEvent/Outbox 或前端审批交互。
- 没有实现批准后对请求人、审批人和目标资源的当前 Role/Scope 再验证。
- `FrozenArgsRef`、`ResultRef` 和 `DetailRef` 只建立不可变引用契约；对象存储加密、访问控制、生命周期和删除仍需后续切片实现。
- 审计 Hash 链用于发现意外或越权修改，不等于外部不可抵赖日志；拥有数据库完全写权限的攻击者仍可能重算整条链，生产环境需要独立归档或签名锚点。
