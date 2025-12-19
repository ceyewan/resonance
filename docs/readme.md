这是一个关于 **Genesis Resonance** 项目的完整架构与工程指南。这份文档将作为你项目的**“工程白皮书”**。

---

# Genesis Resonance 工程白皮书

## 1. 技术选型 (Tech Stack)

我们采用 **“契约驱动开发 (Schema-First)”** + **“现代 RPC (Modern RPC)”** 的架构模式。

| 领域 | 核心技术 | 选型理由 |
| --- | --- | --- |
| **语言** | **Go (Backend) / TypeScript (Frontend)** | 高并发后端 + 强类型前端，完美契合 Protobuf。 |
| **通信协议** | **Protobuf (v3)** | 唯一的真理来源 (Single Source of Truth)。更小、更快、跨语言。 |
| **API 框架** | **ConnectRPC (Go & Web)** | 同时支持 gRPC (内网) 和 HTTP/JSON (外网/调试)，生成完美的 TS 客户端。 |
| **Web 容器** | **Gin** | Go 生态最成熟的 Web 框架，用于挂载 RPC Handler 和处理中间件。 |
| **实时通讯** | **Gorilla WebSocket + Proto** | 传输层用标准 WS，载荷层用 Proto Binary，解决 WS 缺乏语义的问题。 |
| **工具链** | **Buf** | 替代难用的 protoc，提供 Lint 检查、破坏性变更检测、依赖管理。 |
| **工程化** | **Docker + Makefile** | 实现“单指令交付”，统一开发与部署环境。 |

---

## 2. 核心规范 (Standards)

### 2.1 契约仓库架构

所有 Proto 文件独立管理，不放在业务代码仓库中。

* **仓库名：** `im-contract`
* **目录结构：**
* `api/`：**Gateway 层定义**（BFF）。给前端看，字段经过剪裁，聚合了多个服务。
* `service/`：**Logic 层定义**（Microservices）。后端微服务互调，包含全量字段。
* `common/`：**通用定义**。如分页、错误码、MQ 自定义 Option。
* `gen/`：**产物目录**。包含自动生成的 Go 和 TS 代码。



### 2.2 WebSocket 通信规范

* **信封模式 (Envelope)：** 所有 WS 消息必须被包裹在一个顶层结构 `WsPacket` 中。
* **多态载荷：** 使用 `oneof` 区分消息类型（如 `Chat`, `Heartbeat`, `Ack`）。
* **二进制流：** 前后端传输必须使用 `proto.Marshal/Unmarshal`，禁止传输明文 JSON。

### 2.3 开发工作流

1. **定义：** 在 `im-contract` 修改 `.proto` 文件。
2. **生成：** 运行 `buf generate` 更新 `gen/` 代码。
3. **实现：** 后端更新 Go 代码，前端更新 TS 依赖。

---

## 3. 简要操作步骤与代码模版

### 第一步：构建契约仓库 (`im-contract`)

**1. `buf.yaml` (定义模块)**

```yaml
version: v2
modules:
  - path: proto
    name: buf.build/genesis/resonance # 你的组织名
lint:
  use:
    - STANDARD
  except:
    - PACKAGE_DIRECTORY_MATCH

```

**2. `buf.gen.yaml` (定义生成)**

```yaml
version: v1
plugins:
  - plugin: buf.build/protocolbuffers/go
    out: gen/go
    opt: paths=source_relative
  - plugin: buf.build/connectrpc/go # 生成 Connect 代码
    out: gen/go
    opt: paths=source_relative
  - plugin: buf.build/connectrpc/es # 生成前端 TS Client
    out: gen/ts
    opt: target=ts
  - plugin: buf.build/protocolbuffers/es # 生成前端 TS Message
    out: gen/ts
    opt: target=ts

```

**3. `proto/api/gateway/v1/packet.proto` (WebSocket 定义)**

```protobuf
syntax = "proto3";
package api.gateway.v1;
option go_package = "github.com/your/im-contract/gen/go/api/gateway/v1;gatewayv1";

message WsPacket {
  string seq = 1;
  oneof payload {
    Pulse pulse = 10;       // 心跳 (原 Heartbeat)
    EchoRequest echo = 11;  // 消息 (原 Chat)
    Ack ack = 12;           // 确认
  }
}

message Pulse {}
message EchoRequest { string content = 1; string to = 2; }
message Ack { string ref_seq = 1; }

```

---

### 第二步：后端实现 (`im-server`)

**`main.go` (Gin + Connect + WebSocket)**

```go
package main

import (
	"log"
	"net/http"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	// 引入生成的代码
	gatewayv1 "github.com/your/im-contract/gen/go/api/gateway/v1"
	"github.com/your/im-contract/gen/go/api/auth/v1/authv1connect"
)

// --- ConnectRPC Handler (处理 HTTP API) ---
type AuthServer struct{}

func (s *AuthServer) Login(ctx context.Context, req *connect.Request[...]) (*connect.Response[...], error) {
    // 业务逻辑...
    return connect.NewResponse(...), nil
}

// --- WebSocket Handler (处理长连接) ---
var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func WsHandler(c *gin.Context) {
	conn, _ := upgrader.Upgrade(c.Writer, c.Request, nil)
	defer conn.Close()

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil { break }

		if mt == websocket.BinaryMessage {
			// 1. 反序列化契约
			var packet gatewayv1.WsPacket
			if err := proto.Unmarshal(data, &packet); err != nil {
				continue
			}

			// 2. 处理 Payload
			switch p := packet.Payload.(type) {
			case *gatewayv1.WsPacket_Echo:
				log.Printf("收到消息: %s", p.Echo.Content)
			case *gatewayv1.WsPacket_Pulse:
				// 回复心跳...
			}
		}
	}
}

func main() {
	r := gin.Default()

	// 1. 挂载 ConnectRPC (HTTP 接口)
	path, handler := authv1connect.NewAuthServiceHandler(&AuthServer{})
	r.POST(path+"/*action", gin.WrapH(handler))

	// 2. 挂载 WebSocket
	r.GET("/ws", WsHandler)

	r.Run(":8080")
}

```

---

### 第三步：前端实现 (Web)

**`client.ts`**

```typescript
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "./gen/ts/api/auth/v1/auth_connect";
import { WsPacket, EchoRequest } from "./gen/ts/api/gateway/v1/packet_pb";

// 1. API 调用 (ConnectRPC)
const transport = createConnectTransport({ baseUrl: "http://localhost:8080" });
const authClient = createPromiseClient(AuthService, transport);

async function login() {
  const res = await authClient.login({ username: "user" });
  console.log("Token:", res.token);
}

// 2. WebSocket 调用 (Protobuf)
const ws = new WebSocket("ws://localhost:8080/ws");

ws.onopen = () => {
  // 构造消息
  const msg = new WsPacket({
    payload: {
      case: "echo",
      value: new EchoRequest({ content: "Hello Resonance!", to: "room1" }),
    },
  });
  
  // 发送二进制
  ws.send(msg.toBinary());
};

ws.onmessage = async (event) => {
  // 解析消息
  const buffer = new Uint8Array(await event.data.arrayBuffer());
  const packet = WsPacket.fromBinary(buffer);
  
  if (packet.payload.case === "ack") {
    console.log("消息已送达:", packet.payload.value.refSeq);
  }
};

```

---

### 第四步：自动化交付 (`Makefile`)

在项目根目录放置 `Makefile`，实现一键启动。

```makefile
.PHONY: gen run

# 1. 生成代码 (调用 buf)
gen:
	@echo "🔧 Generating contract code..."
	@buf generate proto

# 2. 启动服务 (本地调试)
run: gen
	@echo "🚀 Starting Resonance..."
	@go run server/main.go

# 3. 压测 (Go Script)
bench:
	@go run tools/bench/main.go -c 1000 -d 30s

```

### 总结 Checklist

1. [ ] **契约仓库**建立了吗？(`im-contract`)
2. [ ] **buf** 配置好了吗？能成功执行 `buf generate` 吗？
3. [ ] **后端**引入了生成的 `gen/go` 包吗？
4. [ ] **前端**安装了 `@connectrpc/connect-web` 和生成的 `gen/ts` 吗？
5. [ ] **Postman** 测试时，Header 加了 `Content-Type: application/json` 吗？

这就是 **Genesis Resonance** 的完整技术蓝图。这就去开工吧！