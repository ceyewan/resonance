package push

import (
	"context"
	"io"
	"net/http"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/im-api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/im-api/gen/go/gateway/v1/gatewayv1connect"
	"github.com/ceyewan/resonance/im-gateway/connection"
)

// Service 实现 PushService，接收 Task 服务的推送请求
type Service struct {
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
func (s *Service) PushMessage(
	ctx context.Context,
	stream *connect.BidiStream[gatewayv1.PushMessageRequest, gatewayv1.PushMessageResponse],
) error {
	s.logger.Info("push message stream established")

	for {
		req, err := stream.Receive()
		if err != nil {
			if err == io.EOF {
				s.logger.Info("push message stream closed by client")
				return nil
			}
			s.logger.Error("failed to receive push request", clog.Error(err))
			return err
		}

		// 推送消息到用户
		resp := s.pushToUser(ctx, req)

		// 发送响应
		if err := stream.Send(resp); err != nil {
			s.logger.Error("failed to send push response", clog.Error(err))
			return err
		}
	}
}

// pushToUser 推送消息到指定用户
func (s *Service) pushToUser(ctx context.Context, req *gatewayv1.PushMessageRequest) *gatewayv1.PushMessageResponse {
	username := req.ToUsername
	message := req.Message

	s.logger.Debug("pushing message to user",
		clog.String("username", username),
		clog.Int64("msg_id", message.MsgId))

	// 检查用户是否在线
	conn, ok := s.connMgr.GetConnection(username)
	if !ok {
		s.logger.Warn("user not connected",
			clog.String("username", username))
		return &gatewayv1.PushMessageResponse{
			MsgId: message.MsgId,
			SeqId: message.SeqId,
			Error: "user not connected",
		}
	}

	// 创建推送包
	packet := &gatewayv1.WsPacket{
		Seq: "",
		Payload: &gatewayv1.WsPacket_Push{
			Push: message,
		},
	}

	// 发送到 WebSocket 连接
	if err := conn.Send(packet); err != nil {
		s.logger.Error("failed to send message to user",
			clog.String("username", username),
			clog.Error(err))
		return &gatewayv1.PushMessageResponse{
			MsgId: message.MsgId,
			SeqId: message.SeqId,
			Error: err.Error(),
		}
	}

	s.logger.Debug("message pushed successfully",
		clog.String("username", username),
		clog.Int64("msg_id", message.MsgId))

	return &gatewayv1.PushMessageResponse{
		MsgId: message.MsgId,
		SeqId: message.SeqId,
		Error: "",
	}
}

// GetHandler 返回 ConnectRPC Handler
func (s *Service) GetHandler() (string, http.Handler) {
	return gatewayv1connect.NewPushServiceHandler(s)
}
