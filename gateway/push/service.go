package push

import (
	"context"
	"io"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/connection"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Context 中 trace_id 的键（与 client.traceIDKey 保持一致）
const traceIDKey = "trace_id"

// Service 实现 PushService，接收 Task 服务的推送请求
type Service struct {
	gatewayv1.UnimplementedPushServiceServer
	connMgr *connection.Manager
	logger  clog.Logger
}

// NewService 创建推送服务
func NewService(connMgr *connection.Manager, logger clog.Logger) *Service {
	return &Service{
		connMgr: connMgr,
		logger:  logger,
	}
}

// PushMessage 实现 PushService.PushMessage（双向流）
func (s *Service) PushMessage(srv gatewayv1.PushService_PushMessageServer) error {
	s.logger.Info("push message stream established")

	for {
		req, err := srv.Recv()
		if err != nil {
			if err == io.EOF {
				s.logger.Info("push message stream closed by client")
				return nil
			}
			s.logger.Error("failed to receive push request", clog.Error(err))
			return err
		}

		// 批量推送消息
		resp := s.pushBatch(srv.Context(), req)

		// 发送响应
		if err := srv.Send(resp); err != nil {
			s.logger.Error("failed to send push response", clog.Error(err))
			return err
		}
	}
}

// pushBatch 批量推送消息给用户
func (s *Service) pushBatch(ctx context.Context, req *gatewayv1.PushMessageRequest) *gatewayv1.PushMessageResponse {
	message := req.Message
	failedUsernames := make([]string, 0)

	// 1. 构造 WebSocket 包 (只做一次)
	packet := &gatewayv1.WsPacket{
		Payload: &gatewayv1.WsPacket_Push{
			Push: message,
		},
	}

	// 2. 循环分发
	for _, username := range req.ToUsernames {
		conn, ok := s.connMgr.GetConnection(username)
		if !ok {
			// 用户不在线，视为失败（或者忽略，取决于业务需求，这里记录为失败）
			failedUsernames = append(failedUsernames, username)
			continue
		}

		// 发送到 WebSocket 连接
		if err := conn.Send(packet); err != nil {
			s.logger.Error("failed to send message to user",
				clog.String("username", username),
				clog.Error(err))
			failedUsernames = append(failedUsernames, username)
		}
	}

	return &gatewayv1.PushMessageResponse{
		MsgId:           message.MsgId,
		FailedUsernames: failedUsernames,
	}
}

// RegisterGRPC 注册 gRPC 服务
func (s *Service) RegisterGRPC(server *grpc.Server) {
	gatewayv1.RegisterPushServiceServer(server, s)
}

// traceContextServerInterceptor 服务端一元拦截器
// 从 metadata 提取 trace_id 并注入到 Context
func traceContextServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if values := md.Get("trace-id"); len(values) > 0 {
				ctx = context.WithValue(ctx, traceIDKey, values[0])
			}
		}
		return handler(ctx, req)
	}
}

// traceContextStreamServerInterceptor 服务端流式拦截器
// 从 metadata 提取 trace_id 并注入到 Context
func traceContextStreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if values := md.Get("trace-id"); len(values) > 0 {
				ctx = context.WithValue(ctx, traceIDKey, values[0])
				// 包装 ServerStream 以替换 Context
				ss = &tracedServerStream{ServerStream: ss, ctx: ctx}
			}
		}
		return handler(srv, ss)
	}
}

// tracedServerStream 包装 ServerStream 以替换 Context
type tracedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (t *tracedServerStream) Context() context.Context {
	return t.ctx
}

// TraceUnaryServerInterceptor 导出一元拦截器
func TraceUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return traceContextServerInterceptor()
}

// TraceStreamServerInterceptor 导出流式拦截器
func TraceStreamServerInterceptor() grpc.StreamServerInterceptor {
	return traceContextStreamServerInterceptor()
}
