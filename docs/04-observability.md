# 可观测性设计

> 本文档描述 Resonance 的日志、指标和分布式追踪设计。阅读完本文后，应该能回答三个问题：各服务的日志和指标分别暴露在哪里；Trace 如何在 Logic 和 Task 之间跨服务传播；以及排查问题时应该先看哪里。

---

## 1. 可观测性目标

Resonance 的可观测性设计服务于两个目标：一是让开发者在本地调试时能快速定位问题，二是让生产环境在出现异常时有足够的信息进行排查。Gateway、Logic、Task 和 Pilot 均使用服务级 TraceProvider/MeterProvider，并完成关键跨进程链路传播。

---

## 2. 日志

### 2.1 日志库

所有服务统一使用 Genesis 的 `clog` 包，底层基于 Go 标准库 `slog`。日志格式和级别通过各服务的 `configs/*.yaml` 配置：

```yaml
log:
  level: debug        # 本地开发
  format: console     # 本地开发使用 console 格式，便于直接阅读
  output: stdout
```

生产环境建议切换为 `format: json`，便于日志采集系统解析。

### 2.2 日志规范

日志使用结构化字段，不拼接字符串：

```go
// 正确
logger.Error("保存消息失败",
    clog.String("session_id", msg.SessionID),
    clog.Int64("event_id", msg.EventID),
    clog.Error(err))

// 避免
logger.Error("保存消息失败: " + err.Error())
```

关键字段约定：

| 字段名 | 类型 | 含义 |
| ------ | ---- | ---- |
| `event_id` | int64 | 事件全局唯一 ID |
| `session_id` | string | 会话 ID |
| `from_username` | string | 发送者用户名 |
| `trace_id` | string | 跨服务追踪 ID |
| `gateway_id` | string | Gateway 实例 ID |

### 2.3 日志级别使用约定

| 级别 | 使用场景 |
| ---- | -------- |
| `Debug` | 正常业务流程的详细信息（消息保存成功、推送入队等） |
| `Info` | 服务启动/停止、重要配置加载 |
| `Warn` | 可恢复的异常（推送失败、路由未找到、未知 payload 跳过） |
| `Error` | 需要关注的错误（事务失败、Inbox 写入失败、组件初始化失败） |

---

## 3. 指标（Metrics）

### 3.1 暴露方式

各服务通过 Prometheus HTTP 端点暴露指标：

| 服务 | 指标端口 | 路径 |
| ---- | -------- | ---- |
| Logic | `:9091` | `/metrics` |
| Gateway | `:9092` | `/metrics` |
| Task | `:9090`（可配置） | `/metrics` |
| Pilot | `:9093` | `/metrics` |

所有服务都启用了 Go runtime 指标（`enable_runtime: true`），包括 goroutine 数量、GC 耗时、内存使用等。

每个服务只创建一个 Meter。该 Meter 同时用于 Resonance 业务指标，并通过 Genesis 的 `connector.WithMeter`、`mq.WithMeter`、`registry.WithMeter`、`auth.WithMeter`、`idgen.WithMeter` 和 `ratelimit.WithMeter` 注入该进程内实际使用的基础设施组件。组件不得回退到 `metrics.Discard()`，也不得额外创建 MeterProvider 或监听端口。

### 3.2 Logic 业务指标

Logic 在 `logic/observability/observability.go` 中定义了以下业务指标：

| 指标名 | 类型 | 含义 |
| ------ | ---- | ---- |
| `logic_login_duration_seconds` | Histogram | 登录请求处理耗时 |
| `logic_register_duration_seconds` | Histogram | 注册请求处理耗时 |
| `logic_send_message_duration_seconds` | Histogram | 发消息请求处理耗时 |
| `logic_create_session_duration_seconds` | Histogram | 创建会话请求处理耗时 |
| `logic_default_agent_session_provision_total` | Counter | 默认 Bot 会话 best-effort provisioning/lazy repair 结果；仅使用固定 trigger/outcome 标签 |

### 3.3 Task 业务指标

Task 在 `task/observability/observability.go` 中定义了以下业务指标：

| 指标名 | 类型 | 含义 |
| ------ | ---- | ---- |
| `task_storage_process_duration` | Histogram | Inbox 写扩散处理耗时 |
| `task_push_enqueue_total` | Counter | 推送入队成功次数（按 gateway_id） |
| `task_push_enqueue_failed` | Counter | 推送入队失败次数（按 gateway_id + reason） |
| `task_push_process_duration` | Histogram | 推送处理耗时 |
| `task_gateway_queue_depth` | Gauge | 各 Gateway 推送队列深度 |
| `task_gateway_connected` | Gauge | 已连接的 Gateway 实例数 |

`task_push_enqueue_failed` 的 `reason` 标签区分了两种失败原因：`client_not_found`（Gateway 实例不在路由表中）和 `queue_full`（推送队列已满）。

### 3.4 Pilot 业务指标

Pilot 使用实例化的 Telemetry 资源，同时关闭 trace provider 和 meter；不会因为 trace shutdown 失败而遗留 Prometheus listener。指标标签只允许事件种类、结果和 token category 等有限枚举，禁止 `run_id`、`tenant_id`、username、conversation、call_id 和 Tool 原始名称。

| 指标名 | 类型 | 含义 |
| ------ | ---- | ---- |
| `pilot_run_queue_wait_seconds` | Histogram | Run 从 durable queue 到 Runtime start 的等待时间 |
| `pilot_first_token_seconds` | Histogram | Runtime start 到第一个文本 Delta 的耗时 |
| `pilot_run_duration_seconds` | Histogram | Runtime 到 settled/failed 的耗时 |
| `pilot_active_runs` | Gauge | 当前进程的 Active Runtime 数量 |
| `pilot_runtime_events_total` | Counter | 按有限事件种类统计 Runtime 事件 |
| `pilot_runtime_failures_total` | Counter | Runtime 失败终态数量 |
| `pilot_tool_calls_total` | Counter | Tool 成功/失败终态数量，不标记 Tool 原名 |
| `pilot_model_tokens_total` | Counter | input/output/cache token 分类用量 |
| `pilot_model_cost_total` | Counter | Provider 返回的模型成本 |
| `pilot_stream_publish_dropped_total` | Counter | 有界流式 egress 丢弃/失败次数 |

---

## 4. 分布式追踪（Trace）

### 4.1 接入方式

Logic、Task 和 Pilot 都通过 OpenTelemetry 接入分布式追踪，使用 OTLP gRPC 协议上报到 Collector（默认地址 `localhost:4317`）。

配置示例（`configs/logic.yaml`）：

```yaml
observability:
  trace:
    disable: false
    endpoint: localhost:4317
    insecure: true
    sampler: 1.0   # 开发环境 100% 采样
```

生产环境建议降低采样率（如 `0.1`），避免 Trace 数据量过大。

### 4.2 Trace 跨服务传播

Gateway/Pilot 到 Logic，以及 Task 到 Gateway 的 gRPC 调用统一使用 `pkg/grpctrace` 注入和提取 W3C `traceparent`/`tracestate`。Logic 在发布 MQ 事件时，会把当前 Trace 上下文注入到 `MQEvent.trace_headers` 中：

```go
// logic/internal/mqpublish/publish.go
observability.InjectTraceContext(ctx, event.TraceHeaders)
```

Task 消费到持久消息或 Agent stream 后，从 `trace_headers` 恢复 Trace 上下文。Dispatcher 随后把当前受限 W3C carrier 保存进内部 `PushTask`；异步 Pusher 在调用 Gateway 前恢复 carrier 并创建子 Span。因此队列调度不会把 `Gateway → Logic → MQ → Task → Gateway` 拆成多条 Trace。

Pilot Ingress 会把上游 trace carrier 与 AgentRun 一起持久化。Worker 在数秒或重启后 claim Run 时，只恢复受限的 W3C `traceparent`/`tracestate`，不恢复任意 header 或用户提供的 baggage，再创建 `agent.run`/`agent.commit_prepared` Span。这样排队不会丢失父链路，也不会把租户或用户值变成高基数属性。

Task 的 Dispatcher 在处理每个事件时会创建 Span：

```go
ctx, endSpan := observability.StartSpan(ctx, "dispatcher.handle",
    attribute.Int64("event_id", ev.GetEventId()),
    attribute.String("session_id", ev.GetSessionId()),
    attribute.String("from_username", ev.GetFromUsername()),
)
defer endSpan()
```

### 4.3 当前 Trace 覆盖范围

| 服务 | 已覆盖 |
| ---- | ------ |
| Logic | gRPC ingress 提取、MQ publish 和业务 Span |
| Task | MQ/stream consumer、`dispatcher.handle`、异步 Gateway push |
| Gateway | HTTP/WS ingress、Logic client 和 Push gRPC ingress；本地配置可关闭导出但仍保留传播 |
| Pilot | durable ingress/run carrier、Runtime/Tool/commit Span 和 Logic client |

---

## 5. 排查问题的入口

### 5.1 消息发不出去

1. 看 Logic 日志：`failed to get session members` / `not a session member` / `failed to save message`
2. 看 `t_message_outbox` 表：`status=0` 的记录是否在增加
3. 看 Outbox 补偿任务日志：`logic/job/outbox.go`

### 5.2 消息发出去了但对方收不到

1. 看 Task 日志：`failed to query online routes` / `client_not_found` / `queue_full`
2. 看 `t_inbox` 表：目标用户的 Inbox 是否有对应记录
3. 看 Gateway 日志：Push 是否到达，WS 连接是否存在

### 5.3 重连后消息没有补回来

1. 看前端 `InboxSyncManager` 的日志（浏览器 console）
2. 看 `PullInboxDelta` 请求的响应：`cursor_id` 是否正确，`has_more` 是否为 true
3. 看 `t_inbox` 表：目标用户的记录是否存在

### 5.4 未读数不对

1. 看 `t_session_member.last_read_seq`：是否已经更新
2. 看 `GetUnreadMessageCount` 的查询：只统计 `event_type = 1`（Message）

---

## 6. 当前实现结构

| 文件 | 内容 |
| ---- | ---- |
| `logic/observability/observability.go` | Logic 指标与 Trace 初始化 |
| `task/observability/observability.go` | Task 指标与 Trace 初始化 |
| `pilot/observability/observability.go` | Pilot 实例化 Telemetry、Run 指标和 durable trace 恢复 |
| `pkg/grpctrace/grpctrace.go` | gRPC W3C Trace Context 客户端/服务端统一传播 |
| `logic/internal/mqpublish/publish.go` | Trace 上下文注入到 MQEvent |
| `task/dispatcher/dispatcher.go` | `dispatcher.handle` Span 与 PushTask carrier |
| `task/pusher/client.go` | PushTask carrier 恢复与 Gateway client 传播 |
| `logic/server/grpc.go` | gRPC Trace Context 提取、鉴权与请求日志拦截器 |
| `configs/logic.yaml` | Logic 可观测性配置 |
| `configs/gateway.yaml` | Gateway 可观测性配置 |
| `configs/pilot.yaml` | Pilot trace 与 `:9093/metrics` 配置 |

---

## 7. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `06-deployment.md` | 各服务端口规划（含 metrics 端口） |
| `05-reliability.md` | 失败场景与补偿机制 |
| `20-message-flow.md` | 主链路各阶段的日志关键字 |

---

## 8. 小结

Resonance 的可观测性以结构化日志和 Prometheus 指标为主，Trace 已经接入 OpenTelemetry 并在 Logic → Task 的 MQ 链路上实现了跨服务传播。排查问题时，日志是第一入口，指标用于趋势判断，Trace 用于跨服务链路关联。当前最值得关注的指标是 `task_push_enqueue_failed`（推送失败率）和 `logic_send_message_duration_seconds`（发消息延迟）。
