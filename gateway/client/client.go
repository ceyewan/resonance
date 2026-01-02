package client

import (
	"context"
	"fmt"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/registry"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Context 中 trace_id 的键（与 middleware.TraceIDKey 保持一致）
const traceIDKey = "trace_id"

// Client 封装与 Logic 服务的 gRPC 连接和共用组件
type Client struct {
	conn *grpc.ClientConn

	// gRPC 原始客户端
	authClient     logicv1.AuthServiceClient
	sessionClient  logicv1.SessionServiceClient
	chatClient     logicv1.ChatServiceClient
	presenceClient logicv1.PresenceServiceClient

	logger    clog.Logger
	gatewayID string

	chatManager     *chatStreamManager
	presenceManager *presenceStreamManager
}

// 服务配置常量
const (
	// gRPC 重试策略配置
	maxAttempts = 4
)

// gRPC 服务配置（内置重试策略）
const serviceConfigJSON = `{
	"methodConfig": [{
		"name": [{"service": "logic.v1.AuthService"}],
		"retryPolicy": {
			"MaxAttempts": 4,
			"InitialBackoff": "0.5s",
			"MaxBackoff": "3s",
			"BackoffMultiplier": 2.0,
			"RetryableStatusCodes": ["UNAVAILABLE"]
		}
	}, {
		"name": [{"service": "logic.v1.SessionService"}],
		"retryPolicy": {
			"MaxAttempts": 4,
			"InitialBackoff": "0.5s",
			"MaxBackoff": "3s",
			"BackoffMultiplier": 2.0,
			"RetryableStatusCodes": ["UNAVAILABLE"]
		}
	}, {
		"name": [{"service": "logic.v1.ChatService"}],
		"retryPolicy": {
			"MaxAttempts": 4,
			"InitialBackoff": "0.5s",
			"MaxBackoff": "3s",
			"BackoffMultiplier": 2.0,
			"RetryableStatusCodes": ["UNAVAILABLE"]
		}
	}, {
		"name": [{"service": "logic.v1.PresenceService"}],
		"retryPolicy": {
			"MaxAttempts": 4,
			"InitialBackoff": "0.5s",
			"MaxBackoff": "3s",
			"BackoffMultiplier": 2.0,
			"RetryableStatusCodes": ["UNAVAILABLE"]
		}
	}]
}`

// NewClient 创建 Logic 客户端（保持 trace-id 透传）
// logicServiceName: Logic 服务名称（如 "logic-service"），通过 registry 做服务发现
func NewClient(logicServiceName, gatewayID string, logger clog.Logger, reg registry.Registry) (*Client, error) {
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if reg == nil {
		return nil, fmt.Errorf("registry is required for service discovery")
	}

	// 使用 registry.GetConnection 进行服务发现
	// 内部已集成 Resolver 和 Balancer，支持 etcd://schema 解析
	conn, err := reg.GetConnection(context.Background(), logicServiceName,
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(4*1024*1024), // 4MB
			grpc.MaxCallSendMsgSize(4*1024*1024),
		),
		// 配置内置重试策略
		grpc.WithDefaultServiceConfig(serviceConfigJSON),
		grpc.WithMaxCallAttempts(maxAttempts),
		// 注册拦截器（目前仅保留 trace）
		grpc.WithChainUnaryInterceptor(
			traceContextUnaryInterceptor(),
		),
		grpc.WithChainStreamInterceptor(
			traceContextStreamInterceptor(),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to logic via service discovery: %w", err)
	}
	logger.Info("logic client connected via service discovery", clog.String("service", logicServiceName))

	client := &Client{
		conn:           conn,
		authClient:     logicv1.NewAuthServiceClient(conn),
		sessionClient:  logicv1.NewSessionServiceClient(conn),
		chatClient:     logicv1.NewChatServiceClient(conn),
		presenceClient: logicv1.NewPresenceServiceClient(conn),
		logger:         logger,
		gatewayID:      gatewayID,
	}

	client.chatManager = newChatStreamManager(logger, client.chatClient)
	client.presenceManager = newPresenceStreamManager(logger, gatewayID, client.presenceClient)

	return client, nil
}

// traceContextUnaryInterceptor 链路追踪拦截器（一元调用）
// 从 Context 提取 trace_id 并注入到 gRPC metadata
func traceContextUnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if traceID := ctx.Value(traceIDKey); traceID != nil {
			ctx = metadata.AppendToOutgoingContext(ctx, "trace-id", traceID.(string))
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// traceContextStreamInterceptor 链路追踪拦截器（流式调用）
// 从 Context 提取 trace_id 并注入到 gRPC metadata
func traceContextStreamInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if traceID := ctx.Value(traceIDKey); traceID != nil {
			ctx = metadata.AppendToOutgoingContext(ctx, "trace-id", traceID.(string))
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// Close 关闭客户端
func (c *Client) Close() error {
	if c.chatManager != nil {
		c.chatManager.Close()
	}
	if c.presenceManager != nil {
		c.presenceManager.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// 以下方法暴露原始 gRPC 客户端供各服务封装使用

func (c *Client) authSvc() logicv1.AuthServiceClient {
	return c.authClient
}

func (c *Client) sessionSvc() logicv1.SessionServiceClient {
	return c.sessionClient
}
