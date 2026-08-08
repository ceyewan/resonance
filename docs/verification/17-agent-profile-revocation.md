# Agent Profile 撤销边界验证

日期：2026-08-09

## 目标

证明 AI Session 里保存的 Profile 只是不可变配置快照，不会把创建会话时的权限永久
带入后续 Run。管理员被降权、成员被禁用或系统角色发生变化后，新事件、排队任务、
活动任务和旧 Session 都必须 fail closed；同时不能用后续撤权回滚已经由 Logic 确认的
用户可见事实。

## 授权与取消状态机

同一套 `AuthorizeAgentProfile` 规则由 Logic 会话创建、Pilot Ingress 和 Coordinator
共享：

- `user-assistant` 要求当前 `chat:use` Scope；
- `iam-admin` 同时要求当前 `iam-admin` 系统角色与 `iam:users:read` Scope；
- 未知 Profile 一律拒绝。

Ingress 在写 durable Run 前从权威 TenantMembership、SystemRoleBinding 和 Human User
重新解析 Principal。Coordinator 在领取后、Runtime settled 后以及
`final_event_id == 0` 的最终消息提交前再次解析。任何确定的撤权都会：

1. 停止当前 Run 的 lease heartbeat；
2. 把该 Conversation 的 active Session Binding 标记为 dirty；
3. 取消同 Tenant、Actor、Profile 和 ProfileVersion 的 queued/retryable Run；
4. 通过完整 lease token/version fence 取消当前 claimed/running/prepared Run。

瞬时 IAM 读取错误不会被误判为撤权，也不会继续提交；它按可恢复错误处理。Tool
Broker 对每次调用继续做当前权限复核。`iam-admin` TextDelta 不进入临时 Stream，只有
通过最终权限复核并由 Logic 持久化的消息才对用户可见。

## 不可回滚的事实边界

`final_event_id == 0` 表示最终消息尚未获得 Logic ACK，撤权可以安全取消 Run。
`final_event_id != 0` 表示相同 `client_msg_id` 的消息已经成为 Logic/Outbox 的 durable
事实。此后即使 Actor 被降权，Reconciler 也必须完成原 Candidate Session CAS 和 Run
终态；它不再调用模型、不再重新写消息，也不能把已展示的事实“撤销”成失败。

这一区分覆盖两个相反的崩溃窗口：

- Candidate 已准备但最终消息未写：撤权后取消并污染旧 Binding；
- 最终消息 ACK 已记录但 Session CAS 未完成：只完成既有提交，不重复发布。

## 回归矩阵

- Ingress 当前 Profile 不再授权时返回永久拒绝，且不入队。
- iam-admin 在启动前降权：Runtime 不启动，active Binding 变 dirty，当前和 queued Run
  被取消。
- iam-admin 在 Runtime settled 后降权：预算先按真实 usage 结算，但 Candidate 与最终
  消息都不提交。
- READY_TO_COMMIT 恢复时降权：冻结结果不能绕过当前授权。
- 最终消息 ACK 已记录后降权：Run 成功完成，`final_event_id` 保持不变，Final Writer
  调用次数为零。
- 普通与管理员最小 Role/Scope、禁用成员、未知 Profile、跨 Tenant/Actor 快照均
  fail closed。
- PostgreSQL Repo 契约覆盖 active + queued 批量取消的 Tenant/Actor/Profile 边界与
  lease fencing；其他 Actor 的 Run 不受影响。

## 可重现命令

```bash
GOCACHE=/tmp/resonance-gocache \
  go test ./pkg/iam ./pilot/identity ./pilot/ingress ./pilot/coordinator ./pilot/stream ./pilot -count=1

GOCACHE=/tmp/resonance-gocache \
  go test -race ./pkg/iam ./pilot/identity ./pilot/ingress ./pilot/coordinator ./pilot/stream -count=10

GOCACHE=/tmp/resonance-gocache \
  go test ./repo \
  -run '^TestAgentRunRepo_ProfileRevocationCancelsClaimedAndPendingRunsOnlyWithinBoundary$' \
  -count=1

GOCACHE=/tmp/resonance-gocache \
  go vet ./pkg/iam ./pilot/identity ./pilot/ingress ./pilot/coordinator ./pilot/stream ./repo
```

本轮功能测试、10 轮 Race 和 Vet 已通过。PostgreSQL 测试已经进入 Repo 契约门禁；
本次本地复跑被 OrbStack Docker API `Ping` 无响应阻断，goroutine 堆栈确认尚未进入
测试 SQL，因此不能把这次环境失败记录成数据库通过。恢复容器运行时后必须重跑上面
的定向命令及完整 `go test ./repo`，再进入候选发布。

## 已知非阻断限制

当前没有订阅 IAM 变更事件去实时中断正在等待 Provider 的进程。安全边界仍然闭合：
特权 Tool 每次重授权、iam-admin Delta 被抑制、settled 与最终提交前会再次检查；代价是
中途撤权的 Run 可能继续消耗 Provider 资源直到 settled/timeout。后续可用成员版本事件
触发 `Runtime.Abort` 缩短该窗口，但不能替代上述提交前权威检查。
