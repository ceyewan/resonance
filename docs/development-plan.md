# Resonance IM 系统开发实施方案

## 1. 系统架构概览

Resonance IM 采用经典的分层架构设计，分离接入层、业务逻辑层和异步处理层，以保证系统的高可用性、可扩展性和职责单一。

### 1.1 架构图 (Mermaid)

```mermaid
graph TD
    Client[客户端 (App/Web)] -->|WebSocket| Gateway[Gateway (接入层)]
    
    subgraph Service Mesh / Internal Network
        Gateway -->|gRPC Stream| Logic[Logic (业务层)]
        Logic -->|gRPC| Gateway
        
        Logic -->|Produce| MQ[Message Queue (Kafka/RocketMQ)]
        MQ -->|Consume| Task[Task (异步推送/写入层)]
        
        Task -->|gRPC| Gateway
    end
    
    subgraph Infrastructure
        Redis[(Redis Cluster)]
        MySQL[(MySQL Cluster)]
        Discovery[Service Discovery (etcd/Consul)]
    end
    
    Gateway -.->|Register/Discover| Discovery
    Logic -.->|Register/Discover| Discovery
    Task -.->|Discover| Discovery
    
    Logic -->|R/W| Redis
    Logic -->|R/W| MySQL
    Task -->|W| MySQL
    
    %% Cache & State interactions
    Gateway -->|Sync State| Logic
    Logic -->|Save Routing| Redis
```

## 2. 核心技术栈选型

| 组件/层级 | 技术选型 | 说明 |
| :--- | :--- | :--- |
| **语言** | Go (Golang) 1.21+ | 高并发、高性能、开发效率高 |
| **微服务基座** | **Genesis** Framework | 统一 DI、Config、Log、Metrics、Tracing |
| **接入层 (Gateway)** | `gorilla/websocket` | 成熟稳定的 WebSocket 库 |
| **RPC 框架** | gRPC + Protobuf | 强类型契约，高性能流式通讯 |
| **Web 框架** | Gin | Gateway 层的 HTTP API 框架，提供 RESTful 接口 |
| **数据库** | MySQL 8.0 | 核心业务数据存储 (User, Session, Message) |
| **ORM** | Genesis DB (GORM) | 增强型 GORM，支持分库分表与统一事务 |
| **缓存/KV** | Genesis Redis | 统一管理的 Redis 客户端 |
| **消息队列** | Kafka / RocketMQ | 削峰填谷，消息异步持久化与写扩散 |
| **配置管理** | Genesis Config | 统一配置加载，支持热更新 |
| **服务发现** | etcd / Consul | (由 Genesis Connector 管理) |

## 3. `im-sdk` 现状评估与改进建议 (基于 Genesis)

结合 **Genesis Framework** 的能力，重新评估 `im-sdk` 现状：

针对您提出的 `im-sdk` 是否满足需求的问题，经过分析接口定义 (`repo.go`) 和业务场景，结论如下：

**现状：**
目前 `im-sdk` 定义了基础的 `User`, `Session`, `Message`, `Router` 的 CRUD 接口，能够满足基本的数据读写需求。

**不足与改进建议 (Gap Analysis)：**

1. **缺少 ID 生成器接口 (Generator)**
    * **问题**: Logic 层不应关注 ID 生成细节。`SessionRepo.UpdateMaxSeqID` 仅提供了底层的 CAS 更新能力，未提供原子性的 "Get Next Sequence ID" 语义。
    * **建议**: 新增 `GeneratorRepo` 接口或在 `Repo` 中集成相关方法。
        * `GetNextSeqID(ctx, sessionID string) (int64, error)`: 基于 Redis Incr 实现原子递增。
        * `GenerateMsgID(ctx) (int64, error)`: 基于 Snowflake 算法生成全局唯一消息 ID。

2. **事务控制 (Transaction) - [已解决]**
    * **现状**: Genesis 的 `app.DB.Transaction(ctx, fn)` 提供了完美的事务闭包支持。
    * **Action**: `im-sdk` 的实现层应直接利用 `Genesis DB` 组件的事务能力，无需重复造轮子。

3. **缓存策略透明化**
    * **建议**: `GetUserSessionList` 等高频读取接口，在 SDK 实现层应自动处理 "先查 Redis，后查 DB，再回填 Redis" 的逻辑，对上层 Logic 透明。

## 4. 详细开发实施步骤 (Roadmap)

### 第一阶段：基础设施与 SDK 完善 (Foundation)

* **目标**: 夯实地基，确保 Logic 和 Gateway 开发时有趁手的工具。
* [ ] **Proto 契约冻结**: 确认 `im-contract` 所有接口定义无误。
* [ ] **SDK 增强**:
  * 实现 `Generator` (Snowflake & Redis Seq)。
  * 集成 **Genesis Framework**:
    * 使用 `genesis/pkg/container` 进行依赖注入。
    * 使用 `genesis/pkg/clog` 替换标准日志。
    * 使用 `genesis/pkg/config` 管理配置。
  * 完成基于 Genesis DB/Redis 的 `impl` 实现。

### 第二阶段：接入层 (Gateway) 开发

* **目标**: 实现长连接维护与消息转发，同时提供面向前端的 RESTful API。
* [ ] **API Strategy (HTTP + JSON)**:
  * **协议定义**: 沿用 `im-contract` 中的 Proto 定义作为 Source of Truth。
  * **实现方式**: 使用 **Gin** 框架暴露 HTTP 接口 (如 `/api/v1/login`, `/api/v1/sessions`)。
  * **双模兼容**:
    * 支持标准 HTTP JSON 请求，便于调试和普通 HTTP 客户端调用。
    * 支持 TS 前端使用 Proto 生成的类型或 Client 进行强类型调用。
  * **数据流转**: HTTP Request (JSON) -> Gin Handler -> Proto Struct -> gRPC Client -> Logic Service -> Proto Response -> JSON -> HTTP Response.
* [ ] **WebSocket Server**: 启动 WS 服务 (如 `/ws`)，支持握手鉴权 (JWT)。
* [ ] **Connection Manager**: 实现 `ConnMap`，管理 `clientID -> *websocket.Conn`。
* [ ] **Upstream Handler**: 解析 Packet，通过 gRPC Stream (`ChatService.SendMessage`) 转发至 Logic。
* [ ] **Downstream Handler**: 实现 gRPC Server (`PushService.PushMessage`)，接收 Logic 推送并写入 WS。
* [ ] **State Sync**: 维护连接心跳，断连时调用 Logic (`SyncState`) 清除状态。

### 第三阶段：业务层 (Logic) 开发 - 核心链路

* **目标**: 消息处理、路由管理与会话管理。
* [ ] **Auth & Session API**: 实现登录、获取会话列表等 gRPC 接口 (供 Gateway 调用)。
* [ ] **Message Pipeline**:
  * 利用 SDK 生成 MsgID 和 SeqID。
  * 利用 SDK 事务保存消息并更新会话 Seq。
  * ACK 回复 Gateway。
  * Produce 消息到 MQ (Topic: `resonance.push.event.v1`)。
* [ ] **Router Management**: 处理 `SyncState` 请求，维护 Redis 中的 `User -> Gateway` 映射。

### 第四阶段：异步处理层 (Task) 开发

* **目标**: 消息扩散与精准推送。
* [ ] **MQ Consumer**: 消费 `resonance.push.event.v1`。
* [ ] **Inbox Writer**: (可选 MVP) 实现写扩散，将消息写入接收者的 `t_inbox` 表。
* [ ] **Push Dispatcher**:
  * 调用 SDK `BatchGetUsersGateway` 查询目标用户所在的 Gateway。
  * 按 Gateway 实例分组，批量 RPC 调用 `PushMessage`。
  * 处理用户离线情况 (接入第三方推送)。

## 5. 协作与规范

* **Git Flow**: `main` 为稳定分支，`develop` 为开发分支，`feature/*` 为功能分支。
* **Error Code**: 统一制定错误码规范 (im-contract/errors)。
* **Logging**: 统一使用 **Genesis Clog** (`slog` based)，自动注入 TraceID 以便于全链路追踪。
