# Agent 历史失效验证

日期：2026-08-09

## 目标

证明用户 Edit/Recall 已存在消息后，Pi 的追加式旧上下文不能继续产生或提交过期答案；
同时，已经由 Logic 持久化的最终消息是不可回滚事实，恢复器必须完成或安全跳过
Session Candidate，而不能重复发布或重新推理。

## 事务边界

`RecallMessageWithOutbox` 与 `EditMessageWithOutbox` 在保存控制事件的同一个数据库事务中：

1. 锁定目标 Message 并应用 Recall/Edit；
2. 读取权威 Session，只对 AI Conversation 执行 Agent 失效；
3. 以 Agent Run → Session Binding 的固定顺序处理非终态 Run；
4. 没有 durable 最终消息的 Run 变为 `CANCELLED`，清租约并记录
   `history_invalidated`；
5. 已存在最终消息的 Run 保持 `COMMITTING`，只写
   `session_invalidated_at`，不破坏当前 ACK fence；
6. Binding 变为 `DIRTY`，然后写入 Edit/Recall Outbox。

任何步骤失败都会回滚消息变更、Run/Binding 失效和 Outbox，不存在只完成一半的异步窗口。

## Agent 最终消息门禁

Logic 保存 `agent:<run_id>:final` 前校验匹配的 COMMITTING Run、Tenant、AI Session
Profile/version、AgentBot 成员、冻结文本、base Binding generation 和历史失效标记。
因此普通用户不能伪造 Agent client ID，失效 Run 的迟到输出也不能成为新事实。

若最终消息已先持久化，幂等重试继续返回原 ACK。`CompleteAgentRun` 发现
`session_invalidated_at` 后把 Run 结束为 `SUCCEEDED`，但写
`committed_generation=0`，不提升过期 Candidate；Binding 保持 DIRTY，Candidate 由 GC
回收。这个分支不调用模型，也不创建第二条消息。

## 回归矩阵

- Recall 取消同 Conversation 的 active/prepared 与 queued Run，并污染 Binding；
- Edit 具有相同失效语义；
- 失效后新建 Agent final 返回 `ErrAgentFinalMessageNotCommittable`，Logic 映射为
  gRPC `Aborted`；
- 最终事实先落库时，Edit 不改变 lease/version fence，ACK 与完成可恢复；
- 恢复完成后 final 消息仍只有一条、Binding generation 不提升；
- SQLite 本地契约同时覆盖“无最终事实”和“最终事实已存在”两个崩溃窗口；
- PostgreSQL 契约覆盖相同语义与真实行锁/事务。

## 可重现命令

```bash
GOCACHE=/tmp/resonance-gocache \
  go test ./repo -run 'TestMessageHistoryInvalidation.*SQLiteContract' -count=1 -v

GOCACHE=/tmp/resonance-gocache \
  go test ./logic/service \
  -run 'TestChatService_SendEvent_(ReportsInvalidatedAgentFinalAsAborted|IdempotencyConflict)' \
  -count=1

GOCACHE=/tmp/resonance-gocache \
  go test ./repo \
  -run 'TestMessageRepo_HistoryMutation(CancelsUncommittedAgentRunsAndDirtiesBinding|AfterFinalFactKeepsMessageAndSkipsStaleSessionCommit)' \
  -count=1

GOCACHE=/tmp/resonance-gocache go test ./... -run '^$'
GOCACHE=/tmp/resonance-gocache go vet ./...
```

本地 SQLite 两个契约、Logic 定向测试、全仓编译和 Vet 已通过。本轮 PostgreSQL
用例已经写入 Repo 契约，但 OrbStack Docker API `Ping` 无响应，测试未进入 SQL；不能
把环境阻断写成数据库通过。容器运行时恢复后必须重跑上面的 PostgreSQL 命令和完整
`go test ./repo`。

## 尚未扩大范围

管理员删除/隐私删除除了上下文失效，还需要按数据保留策略处理旧不可变 Session
对象。当前 Edit/Recall 闭合的是运行与提交一致性，不把“对象已不再引用”误写成
“已完成合规物理删除”。
