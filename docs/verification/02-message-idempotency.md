# Loop 02 验证记录：Logic 消息幂等

- 日期：2026-08-08
- 范围：`model.MessageContent`、`repo.MessageRepo`、`logic/service.ChatService`、`bootstrap.MigrateSchema`
- 目标：为 Pilot 最终 Bot Message 建立可重试的权威提交语义；请求响应丢失或并发重试不能生成重复 Message/Outbox。
- 结论：本切片通过；后续 Pilot durable Run 已使用 `client_msg_id=agent:<run_id>:final` 接通最终提交和响应丢失重试。

## 1. 已实现语义

幂等键为：

```text
(session_id, authenticated_sender_username, client_msg_id)
WHERE client_msg_id <> ''
```

Logic 只对当前已知的 Message 字段构造 canonical protobuf，规范化 `UNSPECIFIED → TEXT`，再以版本化 Domain、Session、权威发送者和确定性 protobuf 计算 SHA-256。Hash 覆盖 type、content、reply、client ID 和 Mention 列表，不覆盖新分配的 event/seq/time、目标列表或 Trace。

- 顺序重试在分配 event/seq 前返回第一次 ACK。
- 并发竞态由 PostgreSQL 部分唯一索引与 `ON CONFLICT ... WHERE client_msg_id <> '' DO NOTHING` 收口。
- 竞态输家读取赢家 Message，不推进 `Session.max_seq_id`、不写 Outbox、不触发异步 MQ Publish。
- 同键不同 Hash 返回 `AlreadyExists`。
- 空 client ID 不参与去重；主键等其他约束冲突不会被误吞。
- `client_msg_id` 超过 64 字节、仅空白或含控制字符时在 Logic 入口拒绝。

## 2. 数据库迁移门禁

`bootstrap.MigrateSchema` 在 AutoMigrate 前检查历史重复键，发现重复时只报告数量并停止，不自动删除消息事实。迁移后不只检查索引名称，还验证：

- `indisunique = true`
- `indisvalid = true`
- `indisready = true`
- 列顺序为 `session_id,sender_username,client_msg_id`
- Predicate 为 `client_msg_id <> ''`

测试会主动移除正确索引、插入历史重复，证明迁移 fail closed；随后创建同名但错误的普通索引，证明不会被 GORM 的名称检查误判为成功。

## 3. 可复现验证

```bash
env GOCACHE=/tmp/resonance-gocache \
  go test ./logic/service -run 'TestChatService_SendEvent' -count=1

env GOCACHE=/tmp/resonance-gocache \
  go test ./repo \
  -run 'TestMessage(IdempotencyMigration|Repo_(SaveMessageWithOutbox|IdempotencyIndexContract))' \
  -count=1 -v

env GOCACHE=/tmp/resonance-gocache go test ./... -count=1

env GOCACHE=/tmp/resonance-gocache \
  go test -race ./logic/service -run 'TestChatService_SendEvent' -count=10

env GOCACHE=/tmp/resonance-gocache \
  go test -race ./repo \
  -run 'TestMessageRepo_SaveMessageWithOutbox_ConcurrentRetryCreatesOneFact' \
  -count=5
```

实测环境为 PostgreSQL 17 Testcontainer。顺序重试、8 路并发、payload 冲突、空 ID、跨 Session/Sender、主键冲突、索引定义、历史重复和错误同名索引测试全部通过；完整仓库测试与上述 Race 重复测试通过。

## 4. 仍需完成

- Pilot durable Run 已调用 Logic 提交 `agent:<run_id>:final`；本文件的数据库契约仍是该路径 exactly-once 的底层依据。
- 现存大表不应依靠普通 AutoMigrate 在线建索引；生产需先按 `06-deployment.md` 使用并发索引运维流程。
- 旧消息没有 `idempotency_hash`，不会从可能已编辑的当前内容推测原请求；旧键重试会 fail closed。
- Gateway 与 Pilot 到 Logic 已使用载荷绑定的服务签名，生产默认拒绝明文 `x-username`；但数据库幂等约束仍不能替代 Actor 授权、租户资源隔离和 Bot act-as 范围控制。
