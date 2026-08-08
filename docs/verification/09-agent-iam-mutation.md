# Loop 09 验证记录：审批后 IAM Mutation 最小纵向切片

- 日期：2026-08-09
- Mutation：`set_tenant_member_status`（ACTIVE/DISABLED）
- 结论：真实 IAM 写闭环已接通；Pilot 不能直写 IAM 表，所有副作用经 Logic 权威事务执行。

## 1. 权威边界

Tool Broker 从已验证 Capability 注入 `tenant_id/run_id/call_id/requester_id`，模型只能提交目标用户名、目标状态、`expected_version` 和 `dry_run`。规范化参数（包含注入身份）使用固定 JSON domain 和 SHA-256 绑定。

- `dry_run=true`：调用 Logic 权威 preview，检查当前 requester write scope、目标版本、自操作、Agent Bot 和最后管理员保护；不创建 Approval、Frozen Args、Execution、Receipt，也不更新成员关系。
- `dry_run=false`：Pilot 原子写入不可变 Frozen Args 与 `PREPARED` Execution，再通过每次重新签名的 Logic RPC 创建持久 Approval；该 Tool 调用本身仍不执行 IAM 写入。
- 批准事件只使用 `tenant_id+call_id` 唤醒。Pilot 执行前重新读取 Frozen Args、Approval 和当前 requester Scope；Logic 首次执行再次检查 requester `iam:users:write`、approver `agent:approval:decide`、Approval binding/version/expiry/revoke，并在同一 PostgreSQL 事务内重复检查 IAM 角色与成员状态。
- Logic 事务以 `(tenant_id,idempotency_key)` 写入不可变 Receipt。响应丢失重试先按完整 call/args/requester/target/version binding 查询 Receipt；如果事实已经提交，即使 Approval 随后过期/撤销或 requester/approver 降权，也返回同一 Receipt，不再次写 IAM。没有 Receipt 的新执行仍执行全部当前权限检查。

Pilot 的 reconciler 在启动时和固定周期扫描 `PREPARED/READY/EXECUTING/FAILED_RETRYABLE`，覆盖 prepare 后崩溃、审批事件丢失、进程重启和 RPC 响应丢失。每个阶段使用稳定 Audit ID 追加到 Tenant/Run SHA-256 审计链。

## 2. 安全保护

首次执行拒绝：

- Capability tenant 与请求/冻结参数不一致；
- `call_id/args_hash/frozen ref/requester/target/expected_version` 任一替换；
- Approval pending/rejected/revoked/expired、版本不匹配；
- requester 或 approver 在执行前降权/禁用；
- requester 修改自己、修改 Agent Bot、禁用最后一个 ACTIVE IAM admin；
- 目标成员版本已变化。

审批版本会在 `PREPARED→READY` 时冻结进 Execution，供 `EXECUTING/FAILED_RETRYABLE` 恢复路径查询原始 Receipt，避免把“已提交但 ACK 丢失”误判为失败。

## 3. 故障注入与 PostgreSQL 契约

Fake 故障注入覆盖：prepare 后 Approval RPC 失败、重启 reconciler 补建 Approval、批准事件完全丢失、至少一次重复事件、Logic 提交后响应丢失、响应丢失后 Approval 撤销和 requester 降权、执行前降权、跨租户事件、冻结参数替换，以及 dry-run 零持久副作用。

PostgreSQL 17 Testcontainer 覆盖：并发原子冻结、参数替换拒绝、真实成员状态 CAS、Receipt 单事实/响应恢复、dry-run 不建 Approval/Execution/Receipt、requester/approver 降权、自操作、Agent Bot、Approval expiry/revoke 和跨租户拒绝。

## 4. 可复现命令

```bash
env GOCACHE=/tmp/resonance-go-cache \
  go test ./repo -run 'TestAgentMutationPreparationRepo|TestAgentIAMMutationRepo' -count=1 -v

env GOCACHE=/tmp/resonance-go-cache \
  go test -race ./repo -run 'TestAgentMutationPreparationRepo|TestAgentIAMMutationRepo' -count=1

env GOCACHE=/tmp/resonance-go-cache \
  go test -race ./pilot/mutation ./pilot/logicclient ./logic/service -count=1

env GOCACHE=/tmp/resonance-go-cache \
  go test -race ./pilot/toolbroker -run 'TestBroker_Mutation|TestToolRegistry_Manifest' -count=1

env GOCACHE=/tmp/resonance-go-cache \
  go vet ./pkg/agentmutation ./repo ./logic/service ./logic/server ./logic \
    ./pilot/mutation ./pilot/logicclient ./pilot/toolbroker ./pilot/identity ./pilot/config ./pilot
```

PostgreSQL 与回环 HTTP 测试需要 Docker 和本机 loopback bind 权限；本次记录中的对应命令均实际运行通过，没有把 sandbox Skip 当作成功。

## 5. 保留边界

- 只实现租户成员 ACTIVE/DISABLED，不提供角色写入、删除用户、Shell、SQL 或任意 HTTP Mutation。
- Frozen Args 目前存储在 PostgreSQL `bytea`；生产 KMS/列加密、外部 WORM 审计锚点和 retention policy 仍需后续完成。
- 审批 UI、通知与批量运维不在本切片范围内。
