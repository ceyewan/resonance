# 协议与服务边界

## Proto 分层

| 分层 | 边界 |
| --- | --- |
| `common/v1` | ChatEvent、消息、会话和跨层视图 |
| `gateway/v1` | 浏览器 ConnectRPC、WebSocket packet、内部 Push |
| `logic/v1` | Gateway/Pilot 到 Logic 的 gRPC |
| `mq/v1` | 持久事件信封与临时 Agent Stream |

共享对象放在 common；入口协议不反向污染业务层，不在 MQ 再定义一套消息主事实。

## ChatEvent

统一表示 message、recall、edit、read_receipt、session_update 等用户可见变化。
消息、撤回、编辑与已读已经形成 Logic → Outbox → NATS → Task → Inbox/Push 的闭环。
新增会话内事实优先扩展 payload，不新增并行的持久推送协议。

## Pilot

- `CreateAgentSessionRequest` 无业务参数，幂等返回当前用户唯一 Pilot 私聊；保留已删除字段编号，禁止复用。
- 前端不选择 AgentProfile、租户、Bot、模型版本或管理角色。
- 用户仍通过既有 SendEvent 发消息，不另建一套 AI 消息主事实。
- Pilot 内部 gRPC 只调用 History 和 SendEvent，最终 Bot 消息使用固定 Run 幂等键。
- `AgentStreamEvent` 是独立可丢失的临时文本通道，不写 Outbox/Inbox；Task/Gateway 转为 WS StreamBegin/Chunk/End。
- 最终 ChatEvent 与 stream end 允许先后乱序，以 `final_client_msg_id` 对账。
- 断线只补持久 Inbox/History，不承诺 delta 重放；浏览器断开不自动取消生成。

内部使用 gRPC 不限制外部使用 WS/SSE。当前复用现有 WS；没有实现独立 SSE 出口，也不声称原生浏览器可直接使用 gRPC。

## 身份

Web → Gateway 使用 JWT。Gateway → Logic 对 method、payload hash、tenant、actor、membership version、timestamp 和 nonce 签名。
Logic 验签、防重放并回查 IM 成员后才构造可信 Principal。Pilot 使用独立服务密钥与固定 Bot Actor。

业务 body 不携带 access_token，不通过 username/roles/scopes 指定调用者。
请求中的 session_id 只是资源定位，不能替代租户和会话成员校验。

## 错误

失败返回标准 gRPC status（`codes.X`），Gateway 映射到 ConnectRPC；响应 message 不增加字符串 error 字段。
生成阶段失败属于 Run 结果，不应伪装为用户消息持久化失败。

更多流程见 [消息链路](20-message-flow.md)、[Pilot](14-ai-service.md)。
