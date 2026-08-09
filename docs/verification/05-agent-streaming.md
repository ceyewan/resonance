# Agent Stream 验证记录

日期：2026-08-09

## 验证边界

- `parent_event_id/int32 sequence` 只作兼容，新协议使用 `run_id/stream_id/uint64 stream_sequence`。
- Pilot 只发布 `text_delta`，按时间和字节合并，并限制并发 Stream、pending bytes 与 chunk bytes。
- `AgentStreamEvent` 使用独立 NATS topic；Task Stream Consumer 没有 `MessageRepo`，不写 Inbox。
- Gateway 拒绝缺少新关联字段的 Stream 请求。
- Web 临时气泡做序号去重、乱序丢弃、容量/TTL 限制，并在最终 ChatEvent 落本地库后对账删除。
- 最终 Bot Message 始终由 Logic ChatEvent/Inbox 提供；Delta 丢失不触发模型重放。

## 可复现命令

```bash
cd /Users/ceyewan/CodeField/resonance

env GOCACHE=/tmp/resonance-gocache go test -race ./pilot/stream -count=20
env GOCACHE=/tmp/resonance-gocache go test -race ./task/streaming -count=10
env GOCACHE=/tmp/resonance-gocache go test ./task/pusher -count=1
env GOCACHE=/tmp/resonance-gocache go test ./gateway/pushserver -count=1
env GOCACHE=/tmp/resonance-gocache go test ./task/integration -count=1

cd web
npm run type-check
npm run lint
npx vitest run
npm run build
```

Gateway WebSocket 单测需要允许回环监听；Task integration 使用 Testcontainers 启动 PostgreSQL、Redis 和 NATS。

## 结果

- Pilot Stream race 20 轮通过。
- Task Stream race 10 轮通过。
- Task/Pusher/Gateway 目标测试通过。
- 真实 NATS 集成测试通过，并断言发布 Stream 前后 `t_inbox` 行数不变。
- Web type-check、lint、39 个 Vitest 用例和生产 build 通过。
- Compose 展开验证通过；Pilot 有非 root、只读 rootfs、cap-drop、no-new-privileges 和 CPU/内存/PID/tmp 限额。

## 未关闭项

- Runtime/Provider 网络隔离和严格 Egress 已在 `12-provider-egress-proxy.md`、`13-agent-runtime-isolation.md` 完成。
- Session CAS/对象回收与 per-tenant Token/Cost budget 已完成；跨主机 Store 和正式容量压测仍是部署环境门禁。
