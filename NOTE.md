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

✅ 已修复的配置
buf.gen.go.yaml 配置：
- gateway/v1/api.proto → 生成 gRPC + ConnectRPC（对外）
- gateway/v1/push.proto → 只生成 gRPC（Task → Gateway）
- logic/v1/*.proto → 只生成 gRPC（服务间调用）
- gateway/v1/packet.proto → 只生成消息类型

buf.gen.ts.yaml 配置：
- gateway/v1/api.proto → 生成 TypeScript + ConnectRPC（前端使用）
- common/*.proto → 生成 TypeScript（共享类型）

代码使用指南：
1. Gateway 暴露给客户端的 API：使用 ConnectRPC Handler
2. Gateway 调用 Logic：使用标准 gRPC Client
3. Task 调用 Gateway：使用标准 gRPC Client
4. 前端调用 Gateway：使用 ConnectRPC Client (TypeScript)