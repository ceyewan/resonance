# gateway/client

该目录封装 Gateway 侧所有访问 Logic 的 gRPC 客户端，目标是提供“业务接口优先”的统一入口，避免服务直接依赖底层连接。主要包含以下部分：

1. `client.go`  
   - 负责与注册中心握手并创建通用的 `Client`。  
   - 配置重试/Trace 拦截器，统一透传 `trace_id`。  
   - 暴露 Auth/Session/Chat/Presence 原始 gRPC 客户端给业务封装层使用。

2. `stream_manager.go`  
   - 通用的双向流管理器，负责建流、重连、接收循环以及 trace 上下文继承。  
   - 在流异常时触发上层回调，用于清理待响应的请求。

3. `chat.go` 与 `presence.go`  
   - 基于通用管理器实现聊天消息与在线状态同步。  
   - Chat 以发送顺序维护一个待回执队列；Presence 使用 `seq_id` 追踪 Logic 的 ACK。  
   - 保证每条消息都能得到明确结果，流断开时会向调用方返回错误。

4. `auth.go` / `session.go`  
   - 对一元 RPC 的轻量封装，直接复用 `Client` 初始化出的原生 gRPC 客户端。

5. `governance_config.go`  
   - 预留熔断与限流配置，当前默认使用 Genesis 的配置结构，后续可在 `Client` 初始化中接入真实组件。

## 开发约定

- **保持 trace 透传**：所有对外方法都应继承调用方的 `context.Context`，并通过 `traceContextUnaryInterceptor` / `traceContextStreamInterceptor` 注入 `trace-id`。
- **抽象优先**：新增服务时先在 `Client` 暴露原生 gRPC Client，再在独立文件中做业务封装，避免直接持有连接。
- **治理配置集中**：限流/熔断等治理策略存放在 `governance_config.go`，方便统一调整。
