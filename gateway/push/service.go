package push

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/connection"
	"google.golang.org/grpc"
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

// Push 实现 PushService.Push（一元 RPC）
// 接收 Task 推送的消息，分发给本 Gateway 的在线用户
func (s *Service) Push(ctx context.Context, req *gatewayv1.PushRequest) (*gatewayv1.PushResponse, error) {
	message := req.Message
	failedUsernames := make([]string, 0)

	s.logger.Debug("received push request",
		clog.Int64("msg_id", message.MsgId),
		clog.Int("user_count", len(req.ToUsernames)))

	// 1. 构造 WebSocket 包
	packet := &gatewayv1.WsPacket{
		Payload: &gatewayv1.WsPacket_Push{
			Push: message,
		},
	}

	// 2. 循环分发
	for _, username := range req.ToUsernames {
		conn, ok := s.connMgr.GetConnection(username)
		if !ok {
			// 用户不在线
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

	successCount := len(req.ToUsernames) - len(failedUsernames)
	s.logger.Debug("push completed",
		clog.Int64("msg_id", message.MsgId),
		clog.Int("success_count", successCount),
		clog.Int("failed_count", len(failedUsernames)))

	return &gatewayv1.PushResponse{
		MsgId:           message.MsgId,
		FailedUsernames: failedUsernames,
	}, nil
}

// RegisterGRPC 注册 gRPC 服务
func (s *Service) RegisterGRPC(server *grpc.Server) {
	gatewayv1.RegisterPushServiceServer(server, s)
}

// TraceUnaryServerInterceptor 导出一元拦截器
func TraceUnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// trace_id 注入逻辑可在此处添加
		return handler(ctx, req)
	}
}
