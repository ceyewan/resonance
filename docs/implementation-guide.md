# Resonance IM 开发实施指南

本指南旨在指导开发者完成 Resonance IM 三大核心组件（Gateway, Logic, Task）的具体开发与落地。

---

## 1. Gateway (接入层) 实施指南

Gateway 是系统的门面，负责长连接管理及协议转换。

### 核心技术栈

- **WebSocket**: 推荐使用 `github.com/gorilla/websocket`。
- **gRPC Client**: 与 Logic 通讯。

### 关键实现步骤

1. **连接管理**:
    - 内部维护 `map[string]*Connection`。
    - 用户连接成功后，异步调用 Logic 的 `GatewayOpsService.SyncState` 流，发送 `UserOnline` 事件。
    - 连接断开时，确保发送 `UserOffline` 事件。
2. **协议转换 (Upstream)**:
    - 接收 WS 的二进制数据 -> 反序列化为 `WsPacket`。
    - 如果是 `ChatRequest`，则将其转发到 Logic 的 `ChatService.SendMessage` (双向流)。
    - **优化点**: Gateway 应该持有与 Logic 的长连接流，而不是每条消息都创建新 RPC。
3. **消息下行 (Downstream)**:
    - 实现 `PushService` Server 端。
    - 接收 Logic 推送的消息，根据 `to_username` 找到本地连接并 `WriteMessage`。
    - 写入成功后，通过 `PushMessageResponse` 回复 Ack。

---

## 2. Logic (业务层) 实施指南

Logic 是系统的核心控制器，处理业务逻辑并协调数据流向。

### 核心技术栈

- **gRPC Server**: 响应 Gateway 和外部请求。
- **Redis**: 存储路由信息（`RouterRepo`）。
- **MQ Producer**: 发送业务事件。

### 关键实现步骤

1. **路由维护**:
    - 实现 `GatewayOpsService.SyncState`。
    - 收到 `UserOnline` 时，将 `username -> gateway_id` 写入 Redis，并设置合理的过期时间（Heartbeat 机制）。
2. **消息处理**:
    - 实现 `ChatService.SendMessage`。
    - **验证**: 校验发送者身份及会话权限。
    - **流式回复**: 处理完业务逻辑后（如通过 ID 生成器获取 `msg_id`），立即回传 Ack 给 Gateway。
    - **入派发队列**: 异步将消息封装成 `PushEvent` 投递到 MQ。
3. **会话管理**:
    - 实现 `SessionService`。
    - `GetSessionList` 应从 MySQL/Repo 拉取会话列表，并从 Redis 或缓存中聚合最新消息摘要。

---

## 3. Task (异步处理层) 实施指南

Task 负责沉重的异步任务，如写扩散（Inbox）和消息推送路由。

### 核心技术栈

- **MQ Consumer**: 监听 `resonance.push.event.v1`。
- **MySQL/Repo**: 消息持久化（后期开启）。
- **gRPC Client**: 调用 Gateway 的 `PushService`。

### 关键实现步骤

1. **MQ 消费**:
    - 解析 `PushEvent`。
2. **写扩散 (后期实现)**:
    - 根据会话成员列表，为每个成员生成一条 `Inbox` 记录。
3. **推送路由**:
    - 通过 `RouterRepo.BatchGetUsersGateway` 查询接收者所在的网关实例。
    - **分批推送**: 将属于同一网关实例的消息归类，批量调用对应网关的 `PushMessage` 流。
4. **离线处理**:
    - 如果发现用户不在任何网关上，则转为推送系统通知（如 APNs/FCM）或仅存储为未读消息。

---

## 4. 协作开发建议

- **ID 生成**: 统一使用 Logic 生成 `msg_id`。
- **流式心跳**: Gateway 与 Logic 之间的双向流需自带心跳检测，防止连接假死。
- **优雅关闭**: 任何组件退出时，必须先停止接收新连接/消息，处理完存量流后再关闭。
