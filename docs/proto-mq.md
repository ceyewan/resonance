这是一个非常棒的工程化思考。你正在逼近大厂“中台化”或“平台工程”的核心：**统一契约管理（Schema Registry）**。

将 `proto` 和生成的 `gen` 代码独立出来管理，不仅是“好的”方式，而是 **微服务架构下的最佳实践**。

我们将这个独立的仓库称为 **"Schema Repo"**。我来回答你的三个问题：目录结构如何组织、WebSocket 怎么定契约、MQ 怎么定契约。

---

### 1. 目录结构：The Schema Repo

不要把它放在后端项目的子目录下，而是建立一个独立的 Git 仓库，比如叫 `im-contract`。

**推荐的目录结构：**

```text
im-contract/  (独立的 Git 仓库)
├── buf.yaml          (Buf 项目配置)
├── buf.gen.yaml      (代码生成配置)
├── proto/            (所有的源文件，Single Source of Truth)
│   ├── auth/v1/      (HTTP/RPC 业务)
│   │   └── auth.proto
│   ├── gateway/v1/   (WebSocket 消息)
│   │   └── packet.proto
│   ├── events/v1/    (MQ 消息)
│   │   └── user_events.proto
│   └── common/v1/    (通用结构，如分页、错误码)
│       └── pagination.proto
└── gen/              (生成的产物，致力于“开箱即用”)
    ├── go/           (Go 代码，可以直接 go get)
    │   ├── auth/v1/
    │   ├── gateway/v1/
    │   └── events/v1/
    └── ts/           (TS 代码，可以通过 npm 安装或 git submodule)
        ├── auth/v1/
        └── ...

```

**为什么这么做最好？**

1. **解耦：** 前端（Web）、后端（Go）、数据分析（Python）都依赖这个仓库，谁也不隶属于谁。
2. **版本控制：** 你可以给 `im-contract` 打 tag（v1.0.1）。后端的 `go.mod` 可以锁定在这个版本，不会因为你还在改契约就把后端搞挂了。

---

### 2. WebSocket 结构体支持 (The Gateway Pattern)

WebSocket 的特点是双向流，且没有 HTTP 那样的 URL 路由。我们需要在 Proto 中定义一个“信封（Envelope）”结构。

在 `proto/gateway/v1/packet.proto` 中定义：

```protobuf
syntax = "proto3";
package gateway.v1;
option go_package = "github.com/ceyewan/im-contract/gen/go/gateway/v1;gatewayv1";

import "common/v1/model.proto"; // 引用通用模型

// 1. 顶层信封：所有 WS 数据包必须是这个类型
message WsPacket {
  // 请求序列号，用于 Request-Response 模式的追踪
  string seq = 1;
  // 消息类型 (可选，或者通过 oneof 判断)
  string type = 2; 

  // 核心载荷：利用 oneof 实现多态
  oneof payload {
    // 上行指令 (Client -> Server)
    AuthRequest auth = 10;
    ChatRequest chat = 11;
    Heartbeat   ping = 12;

    // 下行推送 (Server -> Client)
    AuthResponse auth_resp = 20;
    NewMessagePush push_msg = 21;
    Ack            ack      = 22;
  }
}

// 2. 具体的业务包定义
message AuthRequest {
  string token = 1;
}

message ChatRequest {
  string to_user_id = 1;
  string content = 2;
  common.v1.MsgType msg_type = 3; 
}

message NewMessagePush {
  string from_user_id = 1;
  string content = 2;
  int64 timestamp = 3;
}

message Heartbeat {}
message Ack { string ref_seq = 1; }

```

**用法：**

* **后端：** 收到 `[]byte` -> `proto.Unmarshal` 成 `WsPacket` -> `switch packet.Payload.(type)` 分发处理。
* **前端：** 生成 TS 代码后，直接 `WsPacket.toBinary(msg)` 发送。

---

### 3. MQ (Message Queue) 的契约化

这绝对是可以的，而且非常高级！不仅能定义消息体（Payload），甚至能把 Topic 定义进 Proto 里。

#### Level 1: 只定义 Payload (基础版)

最简单的做法是只定义消息内容，Topic 靠口头约定或配置文件。

在 `proto/events/v1/user_events.proto`：

```protobuf
syntax = "proto3";
package events.v1;
option go_package = "...";

// 用户注册成功事件
message UserRegistered {
  string user_id = 1;
  string email = 2;
  int64 registered_at = 3;
}

// 消息已读事件
message MessageRead {
  string msg_id = 1;
  string reader_id = 2;
}

```

#### Level 2: 把 Topic 定义进 Proto (进阶版 - 契约驱动架构)

你可以利用 Protobuf 的 **Custom Options (自定义选项)**，把 Topic 绑定在 Message 定义上。这样代码里甚至不用写 Topic 字符串，直接读元数据！

**第一步：定义一个扩展 (extension)**
在 `proto/common/v1/options.proto`：

```protobuf
syntax = "proto3";
package common.v1;
import "google/protobuf/descriptor.proto";

extend google.protobuf.MessageOptions {
  // 定义一个名为 default_topic 的自定义选项
  string default_topic = 50001; 
}

```

**第二步：在业务 Proto 中使用**
在 `proto/events/v1/user_events.proto`：

```protobuf
import "common/v1/options.proto";

// 直接把 Topic 写在注释或 Option 里
message UserRegistered {
  option (common.v1.default_topic) = "im.user.registered.v1"; // <--- 契约！

  string user_id = 1;
  // ...
}

```

**第三步：Go 代码中读取 (黑科技)**
你的 MQ 发布代码可以写成通用的：

```go
func PublishEvent(ctx context.Context, msg proto.Message) error {
    // 1. 反射获取 Proto 描述符
    md := msg.ProtoReflect().Descriptor()
    
    // 2. 读取自定义 Option (伪代码，需要配合 protoreflect)
    topic := GetDefaultTopicFromOptions(md) 
    
    // 3. 序列化并发送
    payload, _ := proto.Marshal(msg)
    return kafkaProducer.Send(topic, payload)
}

```

**收益：**
你再也不用担心“生产者发到了 `user-register`，消费者监听 `user_register`”这种低级错误了。Topic 名就是代码的一部分。

---

### 总结

你现在的架构蓝图已经非常清晰了：

| 领域 | 定义文件位置 | 技术核心 | 作用 |
| --- | --- | --- | --- |
| **RPC/API** | `proto/auth/*.proto` | Service, RPC, Connect | 对外 HTTP 接口，对内微服务调用 |
| **WebSocket** | `proto/gateway/*.proto` | Oneof, Envelope | 解决 WS 缺乏语义的问题，统一解析入口 |
| **MQ 消息** | `proto/events/*.proto` | Message, Custom Options | 统一异步事件格式，防止 Payload 乱改 |
| **产物管理** | `gen/` 目录 | Git Submodule / Go Modules | 这一层屏蔽了 protoc 的复杂性，业务方开箱即用 |

**下一步行动建议：**
建立这个 `im-contract` 仓库，把现有的 proto 移进去，尝试写一个 `buf.gen.yaml` 把这三类代码（RPC、WS、MQ）一次性生成出来。这是你作为架构师思维的重要一步。