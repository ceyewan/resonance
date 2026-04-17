# Task 服务

Task 是 Resonance 的事件消费与推送服务，负责消费 `MQEvent`，先完成存储，再尝试在线推送。

## 职责边界

- 负责：事件落库（Inbox 写扩散、状态表更新）、在线推送（调用 Gateway PushEvent）
- 不负责：业务规则判断、鉴权、连接管理

## 当前架构（Phase 4）

Task 使用**单消费者串行处理**：

1. 消费 `resonance.chat.event.v1`
2. `dispatcher.Handle` 按 `ChatEvent.payload` 分派到 handler
3. 先做存储/状态变更
4. 存储成功后再推送给在线用户

失败语义：

- 存储阶段失败：返回 error，Consumer `NAK`，消息重试
- 推送阶段失败：只记日志与指标，不 `NAK`，依赖 Inbox 兜底

## 目录结构

```text
task/
├── task.go
├── config/
│   └── config.go
├── consumer/
│   └── consumer.go
├── dispatcher/
│   ├── dispatcher.go
│   ├── helpers.go
│   ├── handler_message.go
│   ├── handler_recall.go
│   ├── handler_edit.go
│   ├── handler_read.go
│   └── handler_session.go
├── pusher/
└── observability/
```

## 配置

`configs/task.yaml` 使用单消费者配置：

```yaml
consumer:
  topic: resonance.chat.event.v1
  queue_group: resonance_group_chat_event
  worker_count: 20
  max_retry: 3
  retry_interval: 5
```

## 运行

```bash
make run-task
```

## 验证重点

- 事件写入 Inbox 成功后，才会触发在线推送
- Gateway 不可达时不影响 ACK，客户端重连可从 Inbox 拉增量
- DB 瞬时失败时消息会重试直到存储成功
