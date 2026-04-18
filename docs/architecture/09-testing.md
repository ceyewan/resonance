# 09 - 测试分层与落地设计

> 前置阅读:`00-overview.md`(总体架构)、`03-services.md`(服务职责)、`04-flows.md`(核心业务时序)、`07-deployment.md`(运行与部署形态)。本文聚焦 Resonance 的**测试体系设计**，目标是让系统在“可真实跑通”的前提下，逐步建立稳定的回归能力。

---

## 0. 文档目的

Resonance 不是单体应用，而是一个典型的分层异步系统：

- Web / Client 通过 HTTP + WebSocket 接入 Gateway
- Gateway 通过 gRPC 调 Logic
- Logic 通过 NATS 发布事件
- Task 消费事件后落库 / 写 Inbox / 调 Gateway Push
- 同时依赖 PostgreSQL、Redis、NATS、Etcd

这意味着测试不能只靠一种形式解决：

- 只做单元测试，证明不了链路真的能跑通
- 只做端到端测试，反馈太慢、定位困难、CI 成本高
- 一上来就做压测，也容易在功能尚未稳定时浪费精力

因此，本项目的测试策略采用**分层设计**：

1. 单元测试：验证纯业务规则
2. 组件测试：单服务 + 真实依赖
3. 集成测试：多服务真实联调
4. 端到端 / 压测：验证完整用户场景与容量边界

核心原则只有一句：

> **先验证“对不对”，再验证“扛不扛得住”。**

---

## 1. 设计目标

### T1. 优先真实依赖，而不是过度 Mock

对 PostgreSQL / Redis / NATS / Etcd 这类基础设施，优先使用 Testcontainers 起真实容器，而不是自己造一层假的数据库 / MQ 行为。

### T2. Mock 只用于边界隔离，不用于模拟整套系统

Mock / Fake 的作用，是把“当前不想纳入测试范围的邻接依赖”隔离出去，而不是把整个系统都伪造一遍。

### T3. 分层反馈速度

- 单元测试要足够快，适合高频运行
- 组件测试可以稍慢，但要定位清晰
- 集成测试是主链路兜底
- 压测与 soak test 不进入日常阻塞门禁

### T4. CI 可跑

所有主测试层都应能在 CI 中执行，不依赖人工预置环境。需要外部依赖时，优先使用 Docker / Testcontainers 自举。

---

## 2. 测试金字塔

推荐的测试层级如下：

| 层级 | 目标 | 依赖形态 | 是否阻塞 PR |
|---|---|---|---|
| Unit | 业务规则、权限、参数校验、纯转换逻辑 | Fake / Stub / 少量 Mock | 是 |
| Component | 单服务行为成立 | 真实 PostgreSQL / Redis / NATS / Etcd + 单服务 | 是 |
| Integration | Gateway / Logic / Task 主链路联调 | 真实多服务 + 真实依赖 | 是 |
| E2E / Load | 真实用户场景、吞吐、延迟、并发 | 真实全链路 | 否，建议夜跑 |

测试投入优先级：

1. Unit
2. Component
3. Integration
4. E2E / Load

原因很直接：

- 前三层负责“功能正确性”
- 最后一层负责“系统容量和运行质量”

---

## 3. 各层测试范围

### 3.1 单元测试

单元测试只解决一件事：**某段业务逻辑的判断是否正确**。

适合单元测试的代码：

- `logic/service/`
- `task/dispatcher/`
- `gateway/transport/ws/`
- `pkg/event/`
- 一些不依赖真实连接器的 helper / converter / policy 逻辑

优先补齐的用例：

#### Logic

- `ChatService.SendEvent`
  - 非成员不能发消息
  - 非法 payload 被拒绝
  - 正常消息会生成 event / seq / outbox
- `SessionService`
  - 创建单聊 / 群聊
  - 已读位置更新
  - 历史消息权限校验
  - Inbox Delta 权限与分页边界

#### Task

- `dispatcher`
  - Message / Recall / Edit / Read / SessionUpdate 的分发
  - Inbox 构造逻辑
  - Push 目标用户集计算

#### Gateway

- WS codec
- 连接管理
- 在线 / 离线状态回调
- 请求上下文中的鉴权信息传递

这一层不应该起 Docker，也不应该连真实基础设施。

### 3.2 组件测试

组件测试验证“**单个服务在真实依赖下能否成立**”。

这里的重点是：

- 服务本体尽量真实启动
- 基础设施使用 Testcontainers
- 邻接服务可按需 fake 掉

推荐拆法如下。

#### Repo 组件测试

范围：

- `repo/user.go`
- `repo/session.go`
- `repo/message.go`
- `repo/router.go`

依赖：

- PostgreSQL
- Redis

实现建议：

- 延续当前 `repo/testutil.go` 的 Testcontainers 方案
- 继续补齐批量查询、分页、并发写入、唯一约束、读写一致性用例

#### Logic 组件测试

范围：

- `logic.New()` / `Run()`
- gRPC `Auth / Session / Chat / Presence`
- Outbox 写入与 MQ 发布

依赖：

- PostgreSQL
- Redis
- NATS
- Etcd

邻接服务：

- 不需要启动 Gateway / Task
- 直接以 gRPC client 调 Logic

验证重点：

- 配置是否正确加载
- 资源是否正常初始化
- 会话创建、消息发送、已读更新能否正常落库
- Outbox 记录是否生成

#### Task 组件测试

范围：

- `task.New()` / `Run()`
- MQ 消费
- Inbox 落库
- Push 路由分发

依赖：

- PostgreSQL
- Redis
- NATS
- Etcd

邻接服务：

- Gateway Push 可以先用 fake gRPC server 代替

验证重点：

- 向 NATS 发布 `MQEvent`
- Task 是否正常消费
- Inbox 是否落库
- 是否向目标 Gateway 发起 Push

#### Gateway 组件测试

范围：

- `gateway.New()` / `Run()`
- HTTP / ConnectRPC
- WebSocket 建连
- PushService 到 WS 投递

依赖：

- Redis
- Etcd

邻接服务：

- Logic 可先用 fake gRPC service 代替

验证重点：

- 登录 / 鉴权链路
- HTTP API -> Logic client 调用
- WS 连接建立与消息推送
- Task Push RPC -> Gateway -> WS 的最后一跳

### 3.3 集成测试

集成测试验证“**多个真实服务连起来是否通**”。

这是当前阶段最重要、也最缺的一层。

建议优先只做 3 条黄金链路：

#### 链路 A：在线消息投递

1. 用户登录
2. 双方建立 WebSocket 连接
3. 创建单聊 / 群聊
4. 发送消息
5. 接收方实时收到推送
6. 数据库中存在消息和 outbox / inbox 记录

#### 链路 B：离线补偿

1. 用户 A 在线，用户 B 离线
2. A 发消息
3. Task 正常落 Inbox
4. B 重连
5. B 通过同步接口拉到离线增量

#### 链路 C：已读回执

1. 发送多条消息
2. 更新已读位置
3. `UnreadCount` 与 `LastReadSeq` 正确变化

这一层需要真实启动：

- Logic
- Task
- Gateway
- PostgreSQL
- Redis
- NATS
- Etcd

但**不需要浏览器前端**。建议直接写 Go 测试客户端：

- HTTP / ConnectRPC client
- WebSocket client
- 必要的 DB 查询辅助断言

这样更稳定，也更适合进 CI。

### 3.4 端到端与压测

这里要明确区分两件事：

- E2E：验证真实用户场景
- Load / Benchmark：验证性能边界

它们不是一回事。

#### E2E

更关注：

- 用户是否能登录
- 会话是否能创建
- 消息是否能到达
- 离线后是否能补偿

#### Load / Benchmark

更关注：

- 最大吞吐量
- 并发连接数
- 单群 / 多群消息风暴时延
- P95 / P99 投递延迟
- 错误率和消息丢失率

压测不建议一开始就做大而全。建议分三档：

1. 冒烟压测：10 用户，少量群，持续 1 分钟
2. 中等规模：100 用户，固定发消息速率
3. 场景压测：单大群、多单聊、离线补偿分别测

---

## 4. Mock / Fake / Stub 选型

本项目不建议“到处上自动生成 Mock”。优先级如下：

1. 手写 Fake
2. `testify/require` / `testify/assert`
3. 必要时引入 `gomock`

### 4.1 优先推荐：手写 Fake

适用场景：

- 接口很小
- 只需要记录调用次数 / 输入参数
- 只需要控制少量返回值

例如当前仓库里的 `logic/service/session_history_permission_test.go` 就属于这一类。

优点：

- 直观
- 可读性高
- 不引入额外生成流程
- 调试成本低

缺点：

- 接口一大，手写会变啰嗦

### 4.2 推荐断言库：`stretchr/testify`

推荐使用：

- `github.com/stretchr/testify/require`
- `github.com/stretchr/testify/assert`

用途：

- 简化断言
- 让失败信息更清楚

建议：

- 关键前置条件用 `require`
- 非关键字段校验用 `assert`

### 4.3 条件推荐：`gomock`

推荐库：

- `go.uber.org/mock/gomock`

适用场景：

- 接口较大
- 调用顺序本身就是业务约束
- 同一接口需要很多场景化返回
- 手写 Fake 成本明显上升

不建议默认用于：

- `repo/` 这种更适合起真实依赖的层
- 很小的 service interface
- 纯数据结构转换

原因：

- 生成代码会增加维护负担
- 对重构不够友好
- 过度使用后，测试会变得“验证调用细节”，而不是“验证业务结果”

### 4.4 不推荐作为默认方案：大规模自动 Mock 生成

例如：

- `mockery`
- `moq`

这些工具不是不能用，但不建议现在作为默认路线。当前仓库还没有大到必须引入独立的 Mock 生成体系。

结论：

- 小接口：手写 Fake
- 复杂交互：`gomock`
- 真实依赖：Testcontainers

---

## 5. 推荐测试工具栈

### 5.1 基础测试

- 标准库 `testing`
- `github.com/stretchr/testify`

### 5.2 容器化依赖

- `github.com/testcontainers/testcontainers-go`

建议所有需要真实外部依赖的测试，都统一走 Testcontainers，而不是要求开发者先手工执行 `docker compose up`。

### 5.3 HTTP / gRPC / WS 客户端

建议直接用项目现有协议栈：

- ConnectRPC / gRPC 官方 client
- Go WebSocket client

可选库：

- `nhooyr.io/websocket`
- 或 Gorilla WebSocket 客户端

建议原则：

- 如果仓库里已有统一客户端实现，优先复用
- 如果没有，就选一个轻量、稳定、API 清晰的库，不要再包一层复杂测试框架

### 5.4 压测工具

推荐两种路线：

#### 路线 A：自写 Go runner

适合当前项目，原因：

- 可以直接复用 proto、鉴权、WS 协议
- 更容易验证“消息真的到了谁”
- 更适合 IM 场景中的双向事件校验

#### 路线 B：`k6`

适合后续补充：

- HTTP API 压测
- 简单 WebSocket 场景压测

但对当前这种“HTTP + WS + gRPC Push + 业务校验”的复合链路，`k6` 不一定是主力工具。

结论：

- 功能集成测试：Go 测试
- 性能压测：优先自写 Go runner，后续可补 `k6`

---

## 6. 测试目录规划

建议目录如下：

```text
repo/
  *_test.go                    # 继续保留 repo 组件测试

logic/
  service/
    *_test.go                  # 单元测试
  integration/
    logic_integration_test.go  # Logic 单服务组件测试

gateway/
  transport/
    ws/
      *_test.go                # WS 编解码 / 管理器单测
  integration/
    gateway_integration_test.go

task/
  dispatcher/
    *_test.go                  # Dispatcher 单测
  integration/
    task_integration_test.go

test/
  integration/
    message_delivery_test.go   # 多服务黄金链路
    offline_sync_test.go
    read_receipt_test.go
  e2e/
    smoke_test.go              # 更贴近用户行为的端到端冒烟
  load/
    README.md
    runner/                    # Go 压测客户端
```

设计意图：

- 单服务组件测试靠近服务自身
- 多服务联调测试统一放在 `test/integration/`
- 压测工具和普通测试分开，不污染日常 `go test ./...`

---

## 7. CI/CD 分层执行

建议把 CI 拆成三层。

### 7.1 快速门禁

目标：

- 3 到 5 分钟反馈

内容：

- `go test` 中的纯单元测试
- `repo/` 以外的快速测试
- lint / format / proto / web check

策略：

- 阻塞 PR

### 7.2 集成门禁

目标：

- 验证真实主链路

内容：

- `repo/` Testcontainers 测试
- Logic / Task / Gateway 组件测试
- `test/integration/` 黄金链路测试

策略：

- 阻塞 PR
- 允许耗时更长

### 7.3 夜跑 / 非阻塞

目标：

- 稳定性与容量验证

内容：

- E2E 冒烟
- 冒烟压测
- 长时间 soak test

策略：

- 不阻塞普通 PR
- 定时执行，失败告警

---

## 8. 第一阶段落地顺序

建议按下面顺序实施，不要一开始全面铺开。

### Step 1. 扩充单元测试

优先文件：

- `logic/service/chat.go`
- `logic/service/session.go`
- `task/dispatcher/`
- `gateway/transport/ws/`

目标：

- 把最核心业务判断先钉住

### Step 2. 补齐 Repo 组件测试

优先场景：

- 消息存取
- 会话成员查询
- Inbox Delta
- Router 批量读写

目标：

- 把底层数据行为先稳定下来

### Step 3. 建立单服务组件测试

建议先做：

1. Logic
2. Task
3. Gateway

原因：

- Logic 是主业务核心
- Task 负责最终落库和分发
- Gateway 适配层相对更容易 fake 下游

### Step 4. 建立黄金链路集成测试

第一批只做：

1. 在线消息投递
2. 离线补偿
3. 已读回执

### Step 5. 再做 E2E 与 Load

先小规模，再逐步放大。

---

## 9. 第一批建议实现的测试用例

建议优先实现以下 10 个用例：

1. `ChatService.SendEvent` 拒绝非会话成员发消息
2. `ChatService.SendEvent` 正常写消息与 outbox
3. `SessionService.CreateSession` 创建单聊成功
4. `SessionService.CreateSession` 创建群聊成功并写系统消息
5. `SessionService.UpdateReadPosition` 正确更新 unread
6. `repo.MessageRepo` 正确保存与查询历史消息
7. `repo.RouterRepo` 正确读写用户网关映射
8. `Logic` 组件测试：真实依赖下发送消息成功
9. 多服务集成测试：在线消息实时到达
10. 多服务集成测试：离线消息补偿成功

---

## 9.1 当前落地进度（2026-04-17）

已完成 `logic/service` 第一批单元测试落地：

1. `ChatService.SendEvent` 拒绝非会话成员发消息
2. `ChatService.SendEvent` 非法 payload 返回 `InvalidArgument`
3. `ChatService.SendEvent` 序列号生成失败返回 `Unavailable`
4. `ChatService.SendEvent` 正常写消息与 outbox，并校验 MQ 事件内容
5. `SessionService.UpdateReadPosition` 覆盖权限拒绝、仓储异常、未读数回退、成功路径
6. `SessionService.CreateSession` 覆盖单聊参数校验、单聊创建、群聊创建与系统消息落库
7. `SessionService.GetSessionList` 覆盖空列表、直聊昵称回填、未读数计算、最后一条事件回填
8. `SessionService.GetContactList / SearchUser / PullInboxDelta` 覆盖成功路径、认证与仓储异常、分页参数边界与 payload 解码错误

同时新增 `logic/service/testutil_test.go` 作为公共测试模块，统一复用：

- `testSessionRepo` / `testMessageRepo` / `testUserRepo`
- `testGenerator` / `testSequencer` / `testMQ`
- `newTestIncomingContext` / `testLogger`

后续 `logic/service` 其他单测可直接复用该模块，减少重复样板代码。

已完成 `task/dispatcher` 第一批单元测试落地：

1. `Dispatcher.Handle` 空事件/未知 payload 安全跳过（避免反复 NAK）
2. `Dispatcher.Handle` message 事件写扩散入库（`SaveInboxBatch`）正确
3. `Dispatcher.Handle` read_receipt 入库失败时返回错误（触发重试）
4. `Dispatcher.Handle` recall 非法 payload 返回错误
5. `Dispatcher.Handle` 推送路由查询失败仅记日志，不影响 ACK（入库成功后返回 nil）
6. `Dispatcher.Handle` edit / session_update 事件写扩散入库正确
7. `buildInboxesForEvent` 覆盖空输入与 payload->`InboxEventType` 映射校验

同时新增 `task/dispatcher/testutil_test.go` 作为公共测试模块，统一复用 fake `MessageRepo/RouterRepo/PusherManager`。

已完成 `task/consumer` 第一批单元测试落地：

1. `NewConsumer` 默认 `WorkerCount` 兜底
2. `handleMessage` 覆盖解析失败 Ack、处理成功 Ack、处理失败 Nak、Ack 失败返回
3. `Start/Stop` 覆盖订阅建立与优雅停止（取消订阅）

已完成 `task/pusher/manager` 第一批单元测试落地：

1. `GetClient` 未命中返回错误
2. `syncServices` 对 `ErrServiceNotFound` 兼容返回 nil
3. `syncServices` 获取服务失败返回错误
4. `addClient` 无 endpoint 跳过
5. `Start` 透传首次同步错误

已完成 `task/pusher/client` 第一批单元测试落地：

1. `Enqueue` 成功入队、队列满、closing/canceled 分支
2. `EnqueueBlocking` 在 closing/canceled 下跳过
3. `QueueSize` 基本行为校验
4. `Close` 幂等调用

已完成 `task/task.go` 第一批单元测试落地：

1. `Run` 覆盖 health/pusher/consumer 启动失败路径与成功路径
2. `Close` 覆盖就绪位切换、组件停止、资源逆序关闭与 consumer stop 错误容忍
3. 通过窄接口抽象（不改业务逻辑）提升生命周期测试可测性

已完成 `logic` 单服务组件测试首条链路：

1. 新增 `logic/integration/logic_integration_test.go`
2. 使用 Testcontainers 启动 PostgreSQL/Redis/NATS
3. 进程内启动 Logic gRPC 组件（Auth/Session/Chat）
4. 覆盖“注册 -> 建会话 -> 发消息”并断言 `message_content` 与 `t_message_outbox` 写入

已完成 `task` 单服务组件测试首条链路：

1. 新增 `task/integration/task_integration_test.go`
2. 使用 Testcontainers 启动 PostgreSQL/Redis/NATS
3. 启动 Task Consumer + Dispatcher，发布真实 `MQEvent`
4. 断言目标用户 Inbox 落库，并验证“push client 不可用时不影响消费 ACK”

已完成 `gateway` 第一批关键单测 + 组件测试：

1. 新增 `gateway/transport/ws/codec_test.go`（编码解码、默认分发器分支）
2. 新增 `gateway/pushserver/service_test.go`（PushEvent/PushStream 核心分支）
3. 新增 `gateway/integration/gateway_integration_test.go`
4. 覆盖 gRPC PushService -> WS 客户端投递链路（进程内组件测试）

已完成多服务黄金链路第一条（在线消息投递）：

1. 新增 `test/integration/message_delivery_test.go`
2. 使用 Testcontainers 启动 PostgreSQL/Redis/NATS
3. 进程内组装 `Logic + Task + Gateway Push + WebSocket` 联调
4. 覆盖“注册 -> 建会话 -> 发消息 -> Task 消费 -> Gateway Push -> WS 收包”
5. 同时断言接收方 Inbox 落库

已补齐剩余两条黄金链路：

1. 新增 `test/integration/offline_sync_test.go`
2. 覆盖“用户离线 -> 对端发消息 -> Task 落 Inbox -> 重连后 PullInboxDelta 拉取增量”
3. 新增 `test/integration/read_receipt_test.go`
4. 覆盖“发送多条消息 -> 分步 UpdateReadPosition -> UnreadCount 与 LastReadSeq 正确变化”

---

## 10. 明确不做的事情

当前阶段不建议做这些事：

- 为所有接口统一生成 Mock
- 一开始就做非常大的 benchmark 平台
- 在前端浏览器里做主链路回归测试
- 把所有测试都塞进一个超长 CI job

原因：

- 成本高
- 反馈慢
- 定位困难
- 容易把测试体系本身做成负担

---

## 11. 最终结论

Resonance 的测试体系应该围绕一句话建设：

> **用真实依赖验证主链路，用小成本隔离非关键边界。**

落地上，推荐采用：

- 单元测试：手写 Fake + `testify`
- 组件测试：`testcontainers-go`
- 集成测试：真实多服务联调
- 压测：后置，优先自写 Go runner

如果只能先做一件事，优先做：

> **多服务黄金链路集成测试。**

因为它最能回答当前阶段真正关键的问题：

> 这套 IM 系统从 Gateway 到 Logic 再到 Task，消息到底能不能真实跑通。
