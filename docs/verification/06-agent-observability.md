# Agent Observability Verification

日期：2026-08-09

## 范围

本切片验证 Pilot 独立 Telemetry 生命周期、durable trace 恢复和 Runtime 事件指标。它不把日志内容或业务 ID 作为 metric label，也不把 best-effort stream 失败提升为最终消息失败。

## 验收行为

- trace provider shutdown 失败时仍关闭 meter 和 Prometheus listener；
- `Close` 幂等，不重复关闭资源；
- AgentRun claim 后从持久化 carrier 恢复 W3C trace；
- 只恢复 `traceparent`/`tracestate`，忽略 baggage、Authorization 和任意 header；
- 记录 queue wait、first token、run duration、active run、runtime failure、Tool outcome、token/cost 和 stream drop；
- 标签中不出现 tenant、user、conversation、run、call 或动态 Tool 名；
- metrics、health 和 Tool Broker 监听端口不能冲突。

## 可复现命令

```bash
GOCACHE=/tmp/resonance-gocache go test ./pilot/observability ./pilot/config ./pilot/coordinator -count=1
GOCACHE=/tmp/resonance-gocache go test -race ./pilot/observability ./pilot/coordinator ./pilot/toolbroker -count=3
GOCACHE=/tmp/resonance-gocache go test ./pilot/... -count=1
```

最后一条命令中的 Tool Broker 测试会绑定 `127.0.0.1:0`；受限沙箱需要允许 loopback listener。

## 结果

- 目标单元测试通过；
- observability/coordinator/toolbroker race 三轮通过；
- Pilot 全包通过；
- metrics 默认暴露在 `:9093/metrics`，与 health `:15093`、Broker `127.0.0.1:15094` 分离。

## 保留门禁

- 指标已具备，但生产告警阈值和 dashboard 仍需按真实流量基线确定；
- Approval 和 Session GC 的专用指标要在对应执行/Reconciler 接入后追加；
- Trace OTLP 与 metrics endpoint 的网络 Egress/Ingress 仍受生产网络策略约束。
