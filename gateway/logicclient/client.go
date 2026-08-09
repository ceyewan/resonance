package logicclient

import (
	"context"
	"fmt"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/gateway/observability"
	"github.com/ceyewan/resonance/pkg/grpctrace"
	"github.com/ceyewan/resonance/pkg/serviceauth"
	"github.com/ceyewan/resonance/pkg/userauth"
)

// Context 中 trace_id 的键（值为 "trace_id"，与 middleware.TraceIDKey 一致）
const traceIDKey = "trace_id"

// Client 封装与 Logic 服务的 gRPC 连接
type Client struct {
	conn *grpc.ClientConn

	// gRPC 原始客户端
	authClient     logicv1.AuthServiceClient
	sessionClient  logicv1.SessionServiceClient
	chatClient     logicv1.ChatServiceClient
	presenceClient logicv1.PresenceServiceClient
	approvalClient logicv1.AgentApprovalServiceClient

	logger        clog.Logger
	gatewayID     string
	statusBatcher *StatusBatcher
	userSigner    *serviceauth.Signer
}

type ClientOption func(*Client)

func WithUserServiceSigner(signer *serviceauth.Signer) ClientOption {
	return func(client *Client) { client.userSigner = signer }
}

// 服务配置常量
const (
	maxAttempts = 4
)

// gRPC 服务配置（内置重试策略）。用户态 Session/Chat/Approval 不做
// transparent retry，因为客户端拦截器只生成一次 nonce；调用方若按业务
// 幂等语义显式重试，会重新进入拦截器并获得新签名。
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
func NewClient(
	logicServiceName, gatewayID string,
	logger clog.Logger,
	reg registry.Registry,
	options ...ClientOption,
) (*Client, error) {
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if reg == nil {
		return nil, fmt.Errorf("registry is required for service discovery")
	}

	client := &Client{logger: logger, gatewayID: gatewayID}
	for _, option := range options {
		option(client)
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
		// 使用不安全连接 (内部通信)
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		// 注册 trace 与用户请求的 Gateway serviceauth 拦截器。
		grpc.WithChainUnaryInterceptor(
			traceContextUnaryInterceptor(),
			userServiceAuthUnaryInterceptor(client.userSigner),
		),
		grpc.WithChainStreamInterceptor(
			traceContextStreamInterceptor(),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to logic via service discovery: %w", err)
	}
	logger.Info("logic client connected via service discovery", clog.String("service", logicServiceName))

	client.conn = conn
	client.authClient = logicv1.NewAuthServiceClient(conn)
	client.sessionClient = logicv1.NewSessionServiceClient(conn)
	client.chatClient = logicv1.NewChatServiceClient(conn)
	client.presenceClient = logicv1.NewPresenceServiceClient(conn)
	client.approvalClient = logicv1.NewAgentApprovalServiceClient(conn)

	return client, nil
}

// userServiceAuthUnaryInterceptor converts a locally verified JWT identity
// into a short-lived, method-and-payload-bound Gateway service credential.
// The end-user bearer token never crosses the Gateway -> Logic boundary.
func userServiceAuthUnaryInterceptor(signer *serviceauth.Signer) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		principal, ok := userauth.PrincipalFromContext(ctx)
		if !ok {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		if signer == nil {
			return status.Error(codes.Unauthenticated, "gateway service authentication is unavailable")
		}
		message, ok := req.(proto.Message)
		if !ok || message == nil {
			return status.Error(codes.Internal, "grpc request is not a protobuf message")
		}
		payloadHash, err := serviceauth.PayloadHash(message)
		if err != nil {
			return status.Error(codes.Internal, "failed to bind gateway service credential")
		}
		signedCtx, err := signer.AuthenticateUserCall(
			ctx,
			principal.TenantID,
			principal.Username,
			principal.MembershipVersion,
			method,
			payloadHash,
		)
		if err != nil {
			return status.Error(codes.Unauthenticated, "failed to authenticate gateway service call")
		}
		return invoker(signedCtx, method, req, reply, cc, opts...)
	}
}

// traceContextUnaryInterceptor 链路追踪拦截器（一元调用）
// 从 Context 提取 trace_id 并注入到 gRPC metadata，同时注入 OTEL TraceContext
func traceContextUnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// 优先使用 Context 中的 trace_id
		var traceID string
		if val := ctx.Value(traceIDKey); val != nil {
			traceID = val.(string)
		}
		// 如果没有，使用 OTEL 生成的 TraceID
		if traceID == "" {
			traceID = observability.GetTraceID(ctx)
		}

		// 注入到 gRPC metadata（用于 Logic 服务日志）
		if traceID != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "trace-id", traceID)
		}

		return invoker(grpctrace.InjectOutgoing(ctx), method, req, reply, cc, opts...)
	}
}

// traceContextStreamInterceptor 链路追踪拦截器（流式调用）
// 从 Context 提取 trace_id 并注入到 gRPC metadata，同时注入 OTEL TraceContext
func traceContextStreamInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		// 优先使用 Context 中的 trace_id
		var traceID string
		if val := ctx.Value(traceIDKey); val != nil {
			traceID = val.(string)
		}
		// 如果没有，使用 OTEL 生成的 TraceID
		if traceID == "" {
			traceID = observability.GetTraceID(ctx)
		}

		// 注入到 gRPC metadata（用于 Logic 服务日志）
		if traceID != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "trace-id", traceID)
		}

		return streamer(grpctrace.InjectOutgoing(ctx), desc, cc, method, opts...)
	}
}

// Close 关闭客户端
func (c *Client) Close() error {
	c.stopStatusBatcher()
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

func (c *Client) approvalSvc() logicv1.AgentApprovalServiceClient {
	return c.approvalClient
}

func (c *Client) PresenceSvc() logicv1.PresenceServiceClient {
	return c.presenceClient
}

// SetStatusBatcher 设置状态批量同步器（由 Gateway 初始化时调用）
func (c *Client) SetStatusBatcher(batcher *StatusBatcher) {
	c.statusBatcher = batcher
}

// StartStatusBatcher 启动状态批量同步器
func (c *Client) StartStatusBatcher() {
	if c.statusBatcher != nil {
		c.statusBatcher.Start()
	}
}

// stopStatusBatcher 停止状态批量同步器
func (c *Client) stopStatusBatcher() {
	if c.statusBatcher != nil {
		c.statusBatcher.Stop()
	}
}
