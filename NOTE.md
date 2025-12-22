🎯 正确的架构划分
Gateway 对外（客户端访问）
需要 ConnectRPC ✅
需要生成 TypeScript 代码 ✅（给前端使用）
协议： gateway/v1/api.proto (AuthService, SessionService)
原因：浏览器/移动端客户端通过 HTTP/1.1 + JSON 访问
Logic 对内（服务间调用）
应该用普通 gRPC ✅
不需要生成 TypeScript 代码 ✅
协议：logic/v1/*.proto (AuthService, SessionService, ChatService, GatewayOpsService)
原因：Gateway → Logic 是服务端之间的调用，用原生 gRPC 性能更好
Gateway 的 PushService（Task → Gateway）
应该用普通 gRPC ✅
协议： gateway/v1/push.proto
原因：Task → Gateway 也是服务间调用
📋 当前问题
你的 buf.gen.go.yaml 可能配置有问题，应该：

gateway/v1/api.proto → 生成 ConnectRPC + TypeScript
gateway/v1/push.proto → 生成普通 gRPC（Go only）
logic/v1/*.proto → 生成普通 gRPC（Go only）
gateway/v1/packet.proto → 只生成 Go 消息类型（不需要服务）
所以你现在的代码框架中，Logic 服务使用 ConnectRPC 是不合适的，应该改用标准 gRPC。Gateway 调用 Logic 也应该用 gRPC 客户端，而不是 ConnectRPC 客户端。