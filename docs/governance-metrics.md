# Issue: 系统可观测性建设 - Prometheus 指标采集

## 元信息

- **ID**: GOV-001
- **标题**: 接入 Prometheus 指标采集
- **优先级**: P0 - 关键
- **状态**: 待开发
- **负责人**: -
- **创建时间**: 2025-01-25

## 问题描述

当前系统仅有日志输出，缺乏结构化的监控指标，导致：

- 无法实时了解系统运行状态
- 问题定位依赖人工查看日志，效率低
- 无法量化系统性能（QPS、延迟、错误率）
- 缺乏告警机制，问题发生后用户反馈才知道

## 受影响范围

- `gateway`: 网关服务
- `logic`: 逻辑服务
- `task`: 任务服务

## 根因分析

1. Genesis `metrics` 组件已存在但未实际使用
2. 代码中缺少关键埋点
3. 无 Prometheus 采集配置
4. 无 Grafana 可视化面板

## 解决方案

### 1. 核心指标定义

| 指标名                     | 类型      | 标签                    | 说明         |
| -------------------------- | --------- | ----------------------- | ------------ |
| `request_total`            | Counter   | service, method, status | 请求总数     |
| `request_duration_ms`      | Histogram | service, method         | 请求耗时     |
| `connection_count`         | Gauge     | gateway_id              | 当前连接数   |
| `message_send_total`       | Counter   | session_type            | 发送消息总数 |
| `message_push_duration_ms` | Histogram | gateway_id              | 推送耗时     |
| `mq_publish_total`         | Counter   | topic, status           | MQ 发布总数  |
| `mq_consume_duration_ms`   | Histogram | topic                   | MQ 消费耗时  |
| `db_query_duration_ms`     | Histogram | table, operation        | DB 查询耗时  |
| `cache_hit_rate`           | Gauge     | cache_type              | 缓存命中率   |

### 2. Gateway 服务埋点

**埋点位置**：

- `gateway/socket/handler.go`: WebSocket 连接建立/断开
- `gateway/protocol/handler.go`: 消息上行处理
- `gateway/push/service.go`: 消息推送
- `gateway/client/chat.go`: Logic 调用

**示例埋点**（伪代码）：

```go
var (
    connActive = metrics.NewGauge("connection_active")
    msgRecvTotal = metrics.NewCounter("message_recv_total")
)

func onConnect() { connActive.Inc() }
func onDisconnect() { connActive.Dec() }
func onMessage() { msgRecvTotal.Inc() }
```

### 3. Logic 服务埋点

**埋点位置**：

- `logic/service/chat.go`: SendMessage 方法
- `logic/service/session.go`: GetSessionList 方法
- `logic/service/auth.go`: Login/Register 方法

### 4. Task 服务埋点

**埋点位置**：

- `task/consumer/consumer.go`: MQ 消费
- `task/pusher/manager.go`: 推送任务执行

### 5. Prometheus 配置

**采集配置** (`prometheus.yml`):

```yaml
scrape_configs:
    - job_name: "resonance-gateway"
      static_configs:
          - targets: ["gateway:9090"]
    - job_name: "resonance-logic"
      static_configs:
          - targets: ["logic:9090"]
    - job_name: "resonance-task"
      static_configs:
          - targets: ["task:9090"]
```

### 6. 关键告警规则

| 告警名称   | 条件                                                                       | 级别     |
| ---------- | -------------------------------------------------------------------------- | -------- |
| 高错误率   | `rate(request_total{status="error"}[5m]) / rate(request_total[5m]) > 0.05` | Critical |
| 高延迟     | `histogram_quantile(0.99, request_duration_ms) > 1000`                     | Warning  |
| 连接数异常 | `connection_count < 10` (生产环境)                                         | Warning  |
| MQ 积压    | `mq_lag > 10000`                                                           | Critical |

## 验收标准

- [ ] 所有服务暴露 `/metrics` 端点
- [ ] 核心指标已埋点并正常上报
- [ ] Prometheus 成功采集指标
- [ ] Grafana 基础面板已创建
- [ ] 关键告警规则已配置并生效

## 参考链接

- Genesis metrics 组件文档
- Prometheus 最佳实践
- Grafana Dashboard 模板
