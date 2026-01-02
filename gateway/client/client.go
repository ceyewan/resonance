package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/breaker"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/ratelimit"
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

	// 治理组件
	breaker breaker.Breaker
	limiter ratelimit.Limiter

	logger    clog.Logger
	gatewayID string

	// 双向流连接
	chatStream     *chatStreamWrapper
	presenceStream *presenceStreamWrapper
	streamMu       sync.Mutex
	seqID          int64
}

// 服务配置常量
const (
	// gRPC 重试策略配置
	maxAttempts = 4
)

// 服务限流配置
// 不同服务有不同的流量特征：Auth 低频、Chat 高频、Session 中等、Presence 低频
var serviceRateLimits = map[string]ratelimit.Limit{
	"logic.v1.AuthService": {
		Rate:  100, // 登录/注册频率低，防刷
		Burst: 200,
	},
	"logic.v1.SessionService": {
		Rate:  500, // 会话操作中等频率
		Burst: 1000,
	},
	"logic.v1.ChatService": {
		Rate:  5000, // 聊天消息高频发送
		Burst: 10000,
	},
	"logic.v1.PresenceService": {
		Rate:  200, // 用户上下线低频
		Burst: 500,
	},
}

// 默认限流配置（当服务名未匹配时使用）
var defaultLimit = ratelimit.Limit{
	Rate:  500,
	Burst: 1000,
}

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

// NewClient 创建 Logic 客户端（包含 breaker 和 ratelimit）
// logicServiceName: Logic 服务名称（如 "logic-service"），通过 registry 做服务发现
func NewClient(logicServiceName, gatewayID string, logger clog.Logger, reg registry.Registry) (*Client, error) {
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if reg == nil {
		return nil, fmt.Errorf("registry is required for service discovery")
	}

	// 创建熔断器
	brk, err := breaker.New(&breaker.Config{
		MaxRequests:     5,                // 半开状态允许通过的最大请求数
		Interval:        60 * time.Second, // 统计周期
		Timeout:         30 * time.Second, // 打开状态持续时间
		FailureRatio:    0.6,              // 失败率阈值 60%
		MinimumRequests: 10,               // 触发熔断的最小请求数
	}, breaker.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("failed to create breaker: %w", err)
	}

	// 创建单机限流器
	limiter, err := ratelimit.NewStandalone(&ratelimit.StandaloneConfig{
		CleanupInterval: 1 * time.Minute,
		IdleTimeout:     5 * time.Minute,
	}, ratelimit.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("failed to create ratelimiter: %w", err)
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
		// 注册拦截器（顺序：trace -> 限流 -> 熔断 -> 实际调用）
		grpc.WithChainUnaryInterceptor(
			traceContextUnaryInterceptor(),
			ratelimitUnaryInterceptor(limiter),
			brk.UnaryClientInterceptor(),
		),
		grpc.WithChainStreamInterceptor(
			traceContextStreamInterceptor(),
			brk.StreamClientInterceptor(),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to logic via service discovery: %w", err)
	}
	logger.Info("logic client connected via service discovery", clog.String("service", logicServiceName))

	return &Client{
		conn:           conn,
		authClient:     logicv1.NewAuthServiceClient(conn),
		sessionClient:  logicv1.NewSessionServiceClient(conn),
		chatClient:     logicv1.NewChatServiceClient(conn),
		presenceClient: logicv1.NewPresenceServiceClient(conn),
		breaker:        brk,
		limiter:        limiter,
		logger:         logger,
		gatewayID:      gatewayID,
	}, nil
}

// extractServiceName 从 gRPC 方法全名中提取服务名
// 例如: "logic.v1.AuthService/Login" -> "logic.v1.AuthService"
func extractServiceName(method string) string {
	// gRPC method 格式: "/package.Service/Method"
	// 例如: "/logic.v1.AuthService/Login"
	for i := len(method) - 1; i >= 0; i-- {
		if method[i] == '/' {
			return method[1:i] // 去掉前导 "/"
		}
	}
	return method
}

// ratelimitUnaryInterceptor 限流拦截器（按服务分级限流）
func ratelimitUnaryInterceptor(limiter ratelimit.Limiter) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// 从方法名提取服务名
		serviceName := extractServiceName(method)

		// 获取该服务的限流配置
		limit, ok := serviceRateLimits[serviceName]
		if !ok {
			limit = defaultLimit
		}

		// 按服务名独立限流
		allowed, err := limiter.Allow(ctx, serviceName, limit)
		if err != nil {
			return fmt.Errorf("ratelimit check failed: %w", err)
		}
		if !allowed {
			return fmt.Errorf("rate limit exceeded for service: %s", serviceName)
		}

		return invoker(ctx, method, req, reply, cc, opts...)
	}
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

func (c *Client) chatSvc() logicv1.ChatServiceClient {
	return c.chatClient
}

func (c *Client) presenceSvc() logicv1.PresenceServiceClient {
	return c.presenceClient
}
