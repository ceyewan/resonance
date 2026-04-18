# Logic 服务

Logic 是 Resonance IM 的业务规则中心，负责鉴权后的业务判断、事件生成、主事实写入与 Outbox 投递。

## 职责边界

- 负责：业务规则、权限校验、`ChatEvent` 生成、主表写入、Outbox 事务一致性、同步 RPC 响应
- 不负责：WebSocket 连接管理、在线推送、Inbox 写扩散

当前实现状态：

- `ChatService.SendEvent` 已统一为事件入口，但当前只落地 `Message` payload
- `Recall/Edit` 的协议与 Task handler 已预留，后续在 Phase 5 接通
- MQ 发布辅助已从 `service/` 下沉到 `internal/mqpublish/`

## 目录结构

```text
logic/
├── logic.go
├── config/
├── observability/
├── server/
│   ├── grpc.go
│   └── interceptor_auth.go
├── service/
│   ├── auth.go
│   ├── chat.go
│   ├── session.go
│   ├── history.go
│   ├── contact.go
│   ├── inbox.go
│   ├── presence.go
│   ├── context.go
│   └── interfaces.go
├── internal/
│   └── mqpublish/
│       └── publish.go
├── event/
│   └── doc.go
├── job/
│   └── outbox.go
└── README.md
```

## 核心模块

`service/chat.go`

- `SendEvent` 是统一事件入口
- 当前负责消息事件写入、生成 `event_id/seq_id/timestamp`、触发 Outbox 发布

`service/session.go`

- 只保留会话核心流程：`GetSessionList`、`CreateSession`、`UpdateReadPosition`
- 会话历史、联系人、Inbox 增量已拆到独立文件，避免继续堆积

`service/history.go` / `contact.go` / `inbox.go`

- 分别承接 `GetHistoryEvents`、`GetContactList/SearchUser`、`PullInboxDelta`

`internal/mqpublish/publish.go`

- 封装 MQ 发布、Outbox 构造、异步 look-aside 发布
- 这是 Logic 独有能力，不对外暴露给其它服务依赖

`job/outbox.go`

- 后台扫描待补发 Outbox
- 发布失败按重试次数回退，超过阈值后标记失败

## 关键流程

消息发送：

1. Gateway 通过 gRPC 调用 `ChatService.SendEvent`
2. Logic 校验会话成员关系并分配 `event_id/seq_id`
3. 写 `message_content` 与 `message_outbox` 在同一事务内完成
4. 主流程返回 Ack，同时异步尝试投递 MQ
5. 投递失败由 `job/outbox.go` 兜底补发

会话读取：

1. `GetSessionList` 使用批量查询避免 N+1
2. `GetHistoryEvents` 校验会话权限后返回历史事件
3. `PullInboxDelta` 按游标解码 `InboxEvent`

## 运行与验证

运行：

```bash
make run-logic
```

验证：

```bash
go test ./logic/...
go build ./logic/...
```

## 相关文档

- [架构总览](../docs/architecture/00-overview.md)
- [服务设计](../docs/architecture/03-services.md)
- [布局重构](../docs/architecture/06-layout-refactor.md)
