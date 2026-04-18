# Task 服务

Task 是 Resonance 的事件消费与分发服务，负责消费 `MQEvent`，先完成存储，再尝试在线推送。

## 职责边界

- 负责：消费 MQ、按 `ChatEvent.payload` 分派处理、写 Inbox、查询在线路由、调用 Gateway Push
- 不负责：业务规则判断、权限校验、消息主事实写入

关键约束：

- `message_content`、撤回标记、编辑主事实必须由 Logic 主事务完成
- Task 只做写扩散与在线推送，不做业务主事实决策

## 目录结构

```text
task/
├── task.go
├── config/
├── consumer/
│   └── consumer.go
├── dispatcher/
│   ├── dispatcher.go
│   ├── inbox.go
│   ├── handler_message.go
│   ├── handler_recall.go
│   ├── handler_edit.go
│   ├── handler_read.go
│   └── handler_session.go
├── pusher/
│   ├── interface.go
│   ├── manager.go
│   └── client.go
├── observability/
└── README.md
```

## 当前架构

Task 当前采用单消费者模型：

1. 消费 `resonance.chat.event.v1`
2. `dispatcher.Handle` 按 `ChatEvent.payload` 分发到对应 handler
3. 先完成 Inbox/派生状态写入
4. 成功后再推送给在线用户

失败语义：

- 存储失败：返回 error，Consumer `NAK`，等待 MQ 重试
- 推送失败：只记录日志与指标，不 `NAK`，依赖 Inbox 与客户端重连补偿

## 核心模块

`dispatcher/dispatcher.go`

- 单入口
- 先存储，后推送
- 未知 payload 直接跳过，避免无意义反复重试

`dispatcher/inbox.go`

- 承担 Inbox 构建辅助逻辑
- 事件类型转换已复用 `pkg/event`

`dispatcher/handler_*.go`

- `handler_message.go`：消息事件写扩散
- `handler_recall.go`：撤回事件写扩散
- `handler_edit.go`：编辑事件写扩散
- `handler_read.go`：已读回执扩散给本用户其它端
- `handler_session.go`：会话更新事件扩散

`pusher/`

- 按网关分组复用 gRPC Push 客户端
- 将在线用户事件发往对应 Gateway 实例

## 运行与验证

运行：

```bash
make run-task
```

验证：

```bash
go test ./task/...
go build ./task/...
```

## 验收重点

- 事件写入 Inbox 成功后，才会触发在线推送
- Gateway 不可达时不影响 ACK，客户端重连后可从 Inbox 拉增量
- DB 瞬时失败时消息会重试直到存储成功

## 相关文档

- [架构总览](../docs/architecture/00-overview.md)
- [服务设计](../docs/architecture/03-services.md)
- [布局重构](../docs/architecture/06-layout-refactor.md)
