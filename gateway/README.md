# Gateway 服务

Gateway 是 Resonance IM 的接入层，负责 HTTP/ConnectRPC、WebSocket 连接、内部 Push RPC，以及到 Logic 的出站调用。

## 职责边界

- 负责：协议适配、连接管理、JWT 鉴权、中间件、转发到 Logic、接收 Task 推送并下发到客户端
- 不负责：业务规则、主事实持久化、Inbox 写扩散

## 目录结构

```text
gateway/
├── gateway.go
├── config/
├── middleware/
├── observability/
├── server/
│   ├── http.go
│   └── grpc.go
├── transport/
│   ├── httpapi/
│   │   ├── handler.go
│   │   ├── routes.go
│   │   ├── errors.go
│   │   └── factory.go
│   └── ws/
│       ├── upgrader.go
│       ├── dispatcher.go
│       ├── codec.go
│       ├── conn.go
│       ├── manager.go
│       └── presence.go
├── logicclient/
│   ├── client.go
│   ├── services.go
│   ├── batcher.go
│   └── config.go
├── pushserver/
│   └── service.go
└── README.md
```

## 核心模块

`transport/httpapi/`
- 负责 ConnectRPC 对外 API
- `handler.go` 承接 Auth/Session 相关接口
- `factory.go` 负责 HTTP 中间件装配，避免与顶层 `middleware/` 命名混淆

`transport/ws/`
- 负责 WS 握手、连接读写、包编解码与 packet 分发
- `codec.go`、`conn.go`、`manager.go`、`presence.go` 已合并到同一 `ws` 包，避免原先 `protocol/`、`connection/` 的职责分裂

`logicclient/`
- 封装 Gateway 到 Logic 的所有 gRPC 调用
- 统一注入 `x-username` metadata
- `batcher.go` 负责 Presence 状态批量同步

`pushserver/service.go`
- 提供内部 `PushService`
- 接收 Task 的 `PushEvent` / `PushStream` 并投递到本机在线连接

## 关键流程

HTTP/ConnectRPC：

1. 请求进入 `server/http.go`
2. `middleware/auth.go` 解析 JWT
3. `transport/httpapi/handler.go` 调用 `logicclient`
4. Logic 返回结果后再回给客户端

WebSocket：

1. `/ws` 入口由 `transport/ws/upgrader.go` 处理
2. 握手后创建 `ws.Conn` 并注册到 `ws.Manager`
3. 客户端上行包由 `ws.Dispatcher` 按 `WsPacket.payload` 分发
4. Chat 请求经 `logicclient.SendEvent` 发往 Logic

内部推送：

1. Task 调用 Gateway `PushService`
2. `pushserver/service.go` 查找本机连接
3. 命中连接则直接写入 `WsPacket`
4. 未命中用户返回到失败列表，由上层决定是否重试

## 与文档要求的一致性

- 已按 `docs/architecture/06-layout-refactor.md` 完成 `transport/`、`logicclient/`、`pushserver/` 重组
- `protocol/` 与 `connection/` 已并入 `transport/ws/`
- `api/` 已改为 `transport/httpapi/`，避免名称误导

## 运行与验证

运行：

```bash
make run-gateway
```

验证：

```bash
go test ./gateway/...
go build ./gateway/...
```

## 相关文档

- [架构总览](../docs/architecture/00-overview.md)
- [服务设计](../docs/architecture/03-services.md)
- [布局重构](../docs/architecture/06-layout-refactor.md)
