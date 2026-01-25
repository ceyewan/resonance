# Issue: 分布式链路追踪 - OpenTelemetry 集成

## 元信息
- **ID**: GOV-002
- **标题**: 接入 OpenTelemetry 分布式链路追踪
- **优先级**: P1 - 高
- **状态**: 待开发
- **负责人**: -
- **创建时间**: 2025-01-25

## 问题描述

当前系统只有 `trace_id` 在日志中传递，缺乏完整的链路追踪能力：
- 无法追踪跨服务调用的完整路径
- 无法定位性能瓶颈在哪个服务
- 问题排查需要手动关联多个服务的日志

## 受影响范围

- `gateway` → `logic` 的 gRPC 调用
- `logic` → NATS → `task` 的异步调用
- 各服务内部的数据库、Redis 调用

## 根因分析

1. `trace.Discard()` 仅生成 TraceID，未实际上报
2. 缺少 Span 概念，无法记录调用栈
3. 无链路追踪后端（Jaeger/Tempo）

## 解决方案

### 1. 技术选型

| 组件 | 选型 | 理由 |
|------|------|------|
| 追踪标准 | OpenTelemetry | 行业标准， vendor-agnostic |
| 上报协议 | OTLP gRPC | 高效、标准化 |
| 后端存储 | Jaeger | 成熟、轻量 |
| 可视化 | Jaeger UI | 开箱即用 |

### 2. 架构设计

```
┌─────────┐     ┌─────────┐     ┌─────────┐
│ Gateway │ ──→ │  Logic  │ ──→ │   NATS  │
└────┬────┘     └────┬────┘     └────┬────┘
     │               │               │
     └───────────────┴───────────────┘
                     │
                     ▼
              ┌─────────────┐
              │   Jaeger    │
              │  (Collector)│
              └─────────────┘
```

### 3. 核心Span设计

#### Gateway 服务
| Span名称 | 说明 | 父Span |
|----------|------|--------|
| `gateway.ws.on_message` | 收到WebSocket消息 | - |
| `gateway.grpc.send_message` | 调用Logic发送消息 | `ws.on_message` |
| `gateway.ws.send_response` | 发送响应给客户端 | `ws.on_message` |

#### Logic 服务
| Span名称 | 说明 | 属性 |
|----------|------|------|
| `logic.chat.send_message` | 发送消息处理 | session_id, from_user |
| `logic.db.save_message` | 数据库保存消息 | db.table, db.rows |
| `logic.mq.publish` | 发布MQ消息 | mq.topic |

#### Task 服务
| Span名称 | 说明 | 属性 |
|----------|------|------|
| `task.mq.consume` | 消费MQ消息 | mq.topic |
| `task.push.gateway` | 推送到Gateway | gateway_id, user_count |

### 4. 传播格式

使用 `traceparent` HTTP header 传播：

```
traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
             └─┬─┘ └──────────────┬──────────────┘ └─┬──┘ └┬┘
               │         trace-id              span-id  flags
               version
```

### 5. gRPC拦截器

```go
// 需要实现的拦截器接口
type TracingInterceptor struct {
    tracer trace.Tracer
}

func (i *TracingInterceptor) Unary() grpc.UnaryClientInterceptor {
    return func(ctx context.Context, method string, req, reply interface{},
        cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
        // 1. 提取父 SpanContext
        // 2. 创建子 Span
        // 3. 调用 invoker
        // 4. 结束 Span
    }
}
```

### 6. MQ消息传递（异步场景）

在 MQ Message 中注入 trace context：

```go
type MQMessage struct {
    // ... 原有字段
    TraceParent string // 新增：traceparent 格式
}

// 发送时注入
msg.TraceParent = traceparent.StringFromContext(ctx)

// 消费时提取
ctx = traceparent.ContextFromString(msg.TraceParent)
```

### 7. Jaeger配置

```yaml
# docker-compose.yml
jaeger:
  image: jaegertracing/all-in-one:latest
  ports:
    - "5775:5775/udp"   # accept zipkin.thrift over compact thrift protocol
    - "6831:6831/udp"   # accept jaeger.thrift over compact thrift protocol
    - "6832:6832/udp"   # accept jaeger.thrift over binary thrift protocol
    - "5778:5778"       # serve configs
    - "16686:16686"     # serve frontend
    - "14268:14268"     # serve grpc
    environment:
    - COLLECTOR_OTLP_ENABLED=true
```

### 8. 关键采样策略

| 场景 | 采样率 | 理由 |
|------|--------|------|
| 正常流量 | 10% | 降低存储压力 |
| 错误请求 | 100% | 必须全量采集 |
| 慢请求 (>1s) | 100% | 性能分析需要 |
| Debug 头部 | 100% | 用户主动触发 |

## 验收标准

- [ ] Jaeger 已部署并正常运行
- [ ] 各服务已集成 OpenTelemetry SDK
- [ ] gRPC 调用链路可追踪
- [ ] MQ 异步调用链路可追踪
- [ ] Jaeger UI 可查看完整调用链
- [ ] 慢请求自动全量采集

## 参考链接

- OpenTelemetry Go SDK 文档
- Jaeger 官方文档
- W3C Trace Context 标准
