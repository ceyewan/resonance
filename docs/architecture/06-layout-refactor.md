# 06 - 服务目录重组设计

> 前置阅读:`03-services.md`(各服务职责)。本文给出 `gateway/` `logic/` `task/` 三服务**目录结构**的重组方案与执行步骤。
>
> 目标:一次性把命名歧义、职责混杂、单文件过大三类问题解决,避免后续(AI 服务、撤回/编辑、离线补偿)在烂目录上叠加。

---

## 0. 不改动的东西(先圈起来,避免误伤)

- **包名不改**:Go 包名沿用目录名,重命名目录会连带改所有 import,但**不动对外 proto 包** (`commonv1 / gatewayv1 / logicv1 / mqv1`)。
- **对外接口签名不改**:所有 gRPC/ConnectRPC method 签名保持原样,只是 handler 文件在哪变了。
- **数据库表和 MQ Topic 不改**。
- **`repo/`、`model/`、`api/gen/`、`pkg/`、`genesis/` 不动**。

---

## 1. 现状问题(按严重度)

### 1.1 Gateway — 四处命名/职责分裂

| # | 现状 | 问题 |
|---|---|---|
| G1 | `middleware/` 放原语,`api/middleware.go` 又叫 middleware(聚合器) | 双份 middleware,新人看到会犯晕 |
| G2 | `protocol/` 只装了一个 WS 二进制 codec(`codec.go` 约 30 行 `Handler/Connection` 接口 + 编解码) | 名字太泛,实际是 WS 专用 |
| G3 | `client/` 是**出站**(→Logic),`push/` 是**入站**(←Task 的 gRPC) | 两者平铺在顶层,方向看不出来 |
| G4 | `api/` 只覆盖 HTTP,但 `api` 名字本应涵盖"对外 API"(含 WS) | 和 `ws/` 不对称,命名误导 |

### 1.2 Logic — 一个胖文件 + 一堆工具杂货

| # | 现状 | 问题 |
|---|---|---|
| L1 | `service/session.go` 476 行,塞了 Session / History / Contact / Search / ReadPosition / InboxDelta 六块 | 单文件职责过多,后续 Phase 5 加 event 会更厚 |
| L2 | `service/helpers.go` 190 行,混了 MQ 发布 + Inbox 构造 + MessageType 枚举转换 + EventType 派生 | 工具函数没分类,查找成本高 |
| L3 | `logic/event/` 目录规划了但还没建(03-services.md 已写入目标结构) | Phase 5 才做,本次不建实体,但**目录占位要留** |

### 1.3 Task — 最干净,只有一处微调

| # | 现状 | 问题 |
|---|---|---|
| T1 | `dispatcher/helpers.go` 里的 `eventTypeFromChatEvent` 与 `logic/service/helpers.go` 中同名函数完全重复 | 小重复,未来新增 event 类型需两处同步 |

### 1.4 跨服务 — 共性轻微重复

三个服务各自有 `config/`、`observability/`,代码高度相似。本次**不动**,维持每服务自治,避免过早抽象。

---

## 2. 目标目录结构

### 2.1 Gateway(目标态)

```
gateway/
├── gateway.go                  # lifecycle(不动)
├── config/
├── observability/
├── server/                     # HTTP + gRPC server 骨架(不动)
│   ├── http.go
│   └── grpc.go
│
├── transport/                  # ★新增:对外接入层(HTTP + WS 对称归位)
│   ├── httpapi/                # ← 原 gateway/api/
│   │   ├── handler.go          # ← 原 httpapi.go
│   │   ├── routes.go
│   │   ├── errors.go
│   │   └── factory.go          # ← 原 api/middleware.go,改名避免歧义
│   └── ws/                     # ← 原 gateway/ws/ + gateway/protocol/ + gateway/connection/
│       ├── upgrader.go
│       ├── dispatcher.go
│       ├── codec.go            # ← 原 gateway/protocol/codec.go
│       ├── conn.go             # ← 原 gateway/connection/conn.go
│       ├── manager.go          # ← 原 gateway/connection/manager.go
│       └── presence.go         # ← 原 gateway/connection/callback.go(改名)
│
├── middleware/                 # 中间件原语(不动)
│   ├── auth.go
│   ├── cors.go
│   ├── logger.go
│   ├── ratelimit.go
│   ├── recovery.go
│   └── trace.go
│
├── logicclient/                # ← 原 gateway/client/,改名体现"出站到 Logic"
│   ├── client.go
│   ├── services.go
│   ├── batcher.go
│   └── config.go
│
└── pushserver/                 # ← 原 gateway/push/,改名体现"入站 gRPC server"
    └── service.go
```

**为什么这么摆**:

1. **`transport/` 是对外接入层的总称**,把 HTTP API 和 WS 两种协议适配收在一起,和"出站 `logicclient/`"、"入站 `pushserver/`"形成三分:
   - 入站对客户端 = `transport/`
   - 出站到 Logic = `logicclient/`
   - 入站从 Task = `pushserver/`
2. **`ws/` 吞并 `protocol/` 和 `connection/`**:这两个子目录的代码**只有 WS 用**,没有任何第二个消费方,平铺在顶层只是"怕 ws/ 变太大",但 ws/ 合并后也只有 ~700 行,完全可控。
3. **`httpapi/factory.go`** 把原来叫 `api/middleware.go` 的中间件聚合器改名,彻底解决 "middleware 在哪"的双份命名。
4. **`logicclient/` / `pushserver/`** 名字直白,调用方向一眼能看出来。

### 2.2 Logic(目标态)

```
logic/
├── logic.go                    # lifecycle(不动)
├── config/
├── observability/
├── server/                     # gRPC server + interceptor(不动)
│   ├── grpc.go
│   └── interceptor_auth.go
│
├── service/                    # RPC handler 层(拆薄)
│   ├── auth.go                 # 不动
│   ├── chat.go                 # 不动
│   ├── presence.go             # 不动
│   ├── session.go              # ★拆小:只保留 CreateSession/GetSessionList/UpdateReadPosition
│   ├── history.go              # ★新增:GetHistoryEvents
│   ├── contact.go              # ★新增:GetContactList/SearchUser
│   ├── inbox.go                # ★新增:PullInboxDelta
│   ├── context.go              # 不动(MustUsernameFromCtx)
│   └── interfaces.go           # 不动(mock 用接口)
│
├── internal/                   # ★新增:包内工具,不对外
│   ├── mqpublish/
│   │   └── publish.go          # ← 原 helpers.go::PublishMessageToMQ/Async/BuildInboxItems
│   └── eventconv/
│       └── conv.go             # ← 原 helpers.go::parseMessageType/formatMessageType/
│                                #   eventTypeFromChatEvent/buildMessageEventFromModel
│
├── event/                      # ★留空目录(带 doc.go 说明),Phase 5 再填
│   └── doc.go
│
└── job/
    └── outbox.go               # 不动
```

**关键说明**:

1. **`service/` 的拆分原则**:一个 RPC 方法一到两个相关方法一个文件。session.go 只留"会话本体的 CRUD 和读位置",其它全搬走。
2. **`internal/mqpublish` 和 `internal/eventconv`** 替代 `helpers.go`:按工具类别分家,名字直接说明职责。放 `internal/` 下避免被其他服务误依赖。
3. **`event/doc.go`** 留一个空包,里面只有一段注释说明"Phase 5 将承载 ChatEvent Builder / Persister / Handler,当前空置",避免未来改动时再建目录有 import 漂移问题。

### 2.3 Task(目标态)

```
task/
├── task.go
├── config/
├── observability/
├── consumer/consumer.go
├── dispatcher/
│   ├── dispatcher.go
│   ├── handler_message.go
│   ├── handler_recall.go
│   ├── handler_edit.go
│   ├── handler_read.go
│   ├── handler_session.go
│   └── inbox.go                # ← 原 helpers.go 改名,只剩 buildInboxesForEvent
└── pusher/
    ├── interface.go
    ├── manager.go
    └── client.go
```

**关键说明**:

1. `dispatcher/helpers.go` 里的 `eventTypeFromChatEvent` 删除,改为从 `logic/internal/eventconv` 引用……等等,**Task 不应依赖 Logic 的 internal 包**。正确做法:把 `eventTypeFromChatEvent` 下沉到 `model/` 或新建 `pkg/event/conv.go`。
2. 两服务都会用的枚举转换,**放 `pkg/event/` 最干净**(provider-free,只依赖 `model` 和生成的 proto)。
3. `dispatcher/helpers.go` → `dispatcher/inbox.go`:去掉 "helpers" 这个垃圾桶名字,按实际语义命名。

### 2.4 跨服务抽取

```
pkg/
└── event/
    └── conv.go                 # ★新增:parseMessageType / formatMessageType /
                                #   eventTypeFromChatEvent / buildMessageEventFromModel
```

- Logic 的 `internal/eventconv` 改为引用 `pkg/event`;
- Task 的 `dispatcher/inbox.go` 也引用 `pkg/event`;
- 所有 event_type 枚举的来源点只此一家,未来新增 event payload 只改一处。

> 修正:2.2 中的 `logic/internal/eventconv/` 应去掉,统一用 `pkg/event/`。`logic/internal/mqpublish/` 保留(MQ 发布与 Outbox 写是 Logic 独有)。

---

## 3. 执行步骤(按顺序,每步独立可验证)

每一步结束跑:`go build ./... && go vet ./... && go test ./...`。任一步失败立即回滚该步,不要跳步。

### Step 1 - 抽 `pkg/event/`(无破坏,纯新增 + 引用切换)

1. 新建 `pkg/event/conv.go`,搬以下函数(来源 `logic/service/helpers.go`):
   - `ParseMessageType`(原 `parseMessageType`,首字母大写对外)
   - `FormatMessageType`
   - `EventTypeFromChatEvent`
   - `BuildMessageEventFromModel`(来源 `logic/service/session.go` 底部的 `buildMessageEventFromModel`)
2. 更新引用:
   - `logic/service/session.go` / `chat.go` / `helpers.go` 内部调用改 `event.ParseMessageType` 等
   - `task/dispatcher/helpers.go` 的 `eventTypeFromChatEvent` 删除,import `pkg/event`
3. `logic/service/helpers.go` 的对应函数删除
4. 验证:全量 `go build/vet/test` 通过

### Step 2 - 拆 `logic/service/session.go`

1. 新建 `logic/service/history.go`,搬 `GetHistoryEvents` 方法
2. 新建 `logic/service/contact.go`,搬 `GetContactList` / `SearchUser` 方法
3. 新建 `logic/service/inbox.go`,搬 `PullInboxDelta` 方法
4. `session.go` 只留 `NewSessionService` 构造器 + `GetSessionList` + `CreateSession` + `UpdateReadPosition` + `sendSessionCreatedSystemMessage` / `buildSystemMessageContent` / `generateSingleChatID` / `generateGroupChatID`
5. 验证:`go build/vet` 通过;`logic/service/session_history_permission_test.go` 仍能跑过

### Step 3 - 把 `logic/service/helpers.go` 拆到 `internal/mqpublish/`

1. 新建 `logic/internal/mqpublish/publish.go`,搬 `PublishMessageToMQ` / `PublishMessageToMQAsync` / `BuildInboxItems` / `PublishMessageToMQResult`
2. 函数维持 package-level 导出(首字母大写)
3. 更新 `logic/service/chat.go` / `session.go` 引用
4. 删除 `logic/service/helpers.go`(此时应为空)
5. 验证

### Step 4 - Logic 加 `event/doc.go` 占位

1. 新建 `logic/event/doc.go`,内容就是包声明 + 说明性 doc comment:
   ```go
   // Package event 预留给 Phase 5 的 ChatEvent 统一处理层:
   // Builder / Persister / Handler(Message/Recall/Edit/...)。当前为空。
   package event
   ```
2. 验证

### Step 5 - Task 重命名 `dispatcher/helpers.go` → `dispatcher/inbox.go`

1. `git mv task/dispatcher/helpers.go task/dispatcher/inbox.go`
2. 若 Step 1 已删除 `eventTypeFromChatEvent`,文件此时只剩 `buildInboxesForEvent`,保持不动
3. 验证

### Step 6 - Gateway:合并 `protocol/` 和 `connection/` 进 `ws/`

**注意:这一步修改 import 路径最多,单独一步做**。

1. `git mv gateway/protocol/codec.go gateway/ws/codec.go`
2. `git mv gateway/connection/conn.go gateway/ws/conn.go`
3. `git mv gateway/connection/manager.go gateway/ws/manager.go`
4. `git mv gateway/connection/callback.go gateway/ws/presence.go`
5. 统一所有新文件的 `package` 声明为 `ws`
6. 全仓 import 路径替换:
   - `gateway/protocol` → `gateway/ws`(其中 `protocol.Handler` 等类型名可能和 ws 包内冲突,若冲突则改名 `PacketHandler` / `Connection`→`WsConnection`)
   - `gateway/connection` → `gateway/ws`
7. 删除空目录 `gateway/protocol` `gateway/connection`
8. 验证:`go build/vet` 通过;启动 Gateway 做一次 WS 连接冒烟(可选,但建议)

### Step 7 - Gateway:新建 `transport/` 包裹 `httpapi/` 和 `ws/`

**注意:再一次 import 路径大迁移,单独一步**。

1. `git mv gateway/api gateway/transport/httpapi`
2. `git mv gateway/ws gateway/transport/ws`
3. `httpapi/httpapi.go` → `httpapi/handler.go`(文件名改)
4. `httpapi/middleware.go` → `httpapi/factory.go`(文件名改,避免和 `gateway/middleware/` 混)
5. 全仓替换 import:
   - `gateway/api` → `gateway/transport/httpapi`
   - `gateway/ws` → `gateway/transport/ws`
6. 包名保持 `httpapi` / `ws`(无需改代码内部引用)
7. 验证

### Step 8 - Gateway:重命名 `client/` → `logicclient/`,`push/` → `pushserver/`

1. `git mv gateway/client gateway/logicclient`
2. `git mv gateway/push gateway/pushserver`
3. 包名可保持 `client` / `push`(外部引用体验差),建议也改成 `logicclient` / `pushserver`
4. 全仓 import + 包名引用替换
5. 验证

### Step 9 - 更新文档

1. `docs/architecture/03-services.md` 的目录树小节同步更新成 2.1/2.2/2.3 的样子
2. 本文档(06)在执行完成后加一个"落地记录"小节,写实际完成的步骤和有无偏差

---

## 4. 风险与回滚

| 风险 | 可能性 | 缓解 |
|---|---|---|
| Step 6 中 `protocol.Handler` / `protocol.Connection` 与 ws 包内名字冲突 | 中 | 冲突则改名 `PacketHandler` / `WsConnection`,在该步内完成 |
| Step 7/8 import 大面积改动漏网 | 中 | 用 `grep -rn "old/path"` 逐个确认;go build 会报漏改 |
| 前端仍在用旧 proto 路径 | 低 | 目录改动不影响 proto,只影响 Go 源码 |
| 多个 Claude Code 会话交叉改同一文件 | 低 | 重组期间暂停其它并行任务 |

**回滚策略**:每步一个 commit。任一步失败,`git reset --hard HEAD~1` 回到上一步;不允许半步。

---

## 5. 预算

- 影响行数:预计改动 Go 源码文件 ~25 个,import 替换 ~50 处,新建文件 5 个,删除目录 2 个
- 预计工时:9 步串联,每步独立验证,顺利情况 **2-3 小时**
- 非阻塞项(可推迟):
  - 跨服务 `pkg/observability/` 抽取 — 本轮不做
  - Logic `event/` 填充 — Phase 5 做
  - Gateway 单测骨架 — 目录稳定后再补

---

## 6. 验收标准

重组结束,满足以下 5 条即视为完成:

1. [ ] `go build ./... && go vet ./... && go test ./...` 全绿
2. [ ] `logic/service/session.go` 行数 < 250
3. [ ] `gateway/` 顶层目录只剩 `config/ gateway.go middleware/ observability/ server/ transport/ logicclient/ pushserver/`
4. [ ] 全仓 `grep -r "gateway/protocol\|gateway/connection\|gateway/api\|gateway/ws\|gateway/client\|gateway/push"` 无遗漏引用(除本文档)
5. [ ] 03-services.md 目录树小节与实际一致

---

## 7. 附:命名决策备忘(给未来自己)

- **`transport/` vs `inbound/`**:选 `transport/` 因为它涵盖"运输协议适配",语义更中性;`inbound/` 会让人纠结"pushserver 也是 inbound,怎么没放里面"。
- **`logicclient/` vs `client/logic/`**:选扁平化,因为 Gateway 除了 Logic 目前无其它外部 gRPC 依赖。未来若加 AI Service,再加 `aiclient/` 平铺即可。
- **`pushserver/` vs `server/push/`**:选扁平化,与 `logicclient/` 对仗;`server/` 目录已经被 HTTP/gRPC 骨架占用,避免歧义。
- **`pkg/event/`**:跨服务共享的 "ChatEvent 纯函数工具"。严格无状态、无依赖注入,只做类型转换。未来如果 event 本身需要构建逻辑(builder 模式等),放 `logic/event/` 而非 `pkg/event/`。
