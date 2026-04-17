package pushserver

import (
	"context"
	"time"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/observability"
	"github.com/ceyewan/resonance/gateway/transport/ws"
	"google.golang.org/grpc"
)

// Service 实现 PushService，接收 Task 服务的推送请求
type Service struct {
	gatewayv1.UnimplementedPushServiceServer
	connMgr *ws.Manager
	logger  clog.Logger
}

func NewService(connMgr *ws.Manager, logger clog.Logger) *Service {
	return &Service{connMgr: connMgr, logger: logger}
}

// PushEvent 实现 PushService.PushEvent（一元 RPC）
func (s *Service) PushEvent(ctx context.Context, req *gatewayv1.PushEventRequest) (*gatewayv1.PushEventResponse, error) {
	startTime := time.Now()
	failedUsernames := make([]string, 0)

	packet := &gatewayv1.WsPacket{
		Payload: &gatewayv1.WsPacket_Event{Event: req.Event},
	}

	for _, username := range req.ToUsernames {
		conn, ok := s.connMgr.GetConnection(username)
		if !ok {
			failedUsernames = append(failedUsernames, username)
			continue
		}
		if err := conn.Send(packet); err != nil {
			s.logger.Error("failed to send event to user", clog.String("username", username), clog.Error(err))
			failedUsernames = append(failedUsernames, username)
		}
	}

	successCount := len(req.ToUsernames) - len(failedUsernames)
	duration := time.Since(startTime)
	observability.RecordGRPCRequest(ctx)
	observability.RecordGRPCRequestDuration(ctx, duration)
	observability.RecordMessageSent(ctx, successCount)
	if len(failedUsernames) > 0 {
		observability.RecordPushFailed(ctx, len(failedUsernames))
	}

	return &gatewayv1.PushEventResponse{
		EventId:         req.Event.GetEventId(),
		FailedUsernames: failedUsernames,
	}, nil
}

// PushStream 实现 PushService.PushStream（一元 RPC）
func (s *Service) PushStream(ctx context.Context, req *gatewayv1.PushStreamRequest) (*gatewayv1.PushStreamResponse, error) {
	failedUsernames := make([]string, 0)

	packet := &gatewayv1.WsPacket{}
	switch p := req.Payload.(type) {
	case *gatewayv1.PushStreamRequest_StreamBegin:
		packet.Payload = &gatewayv1.WsPacket_StreamBegin{StreamBegin: p.StreamBegin}
	case *gatewayv1.PushStreamRequest_StreamChunk:
		packet.Payload = &gatewayv1.WsPacket_StreamChunk{StreamChunk: p.StreamChunk}
	case *gatewayv1.PushStreamRequest_StreamEnd:
		packet.Payload = &gatewayv1.WsPacket_StreamEnd{StreamEnd: p.StreamEnd}
	case *gatewayv1.PushStreamRequest_Typing:
		packet.Payload = &gatewayv1.WsPacket_Typing{Typing: p.Typing}
	default:
		return &gatewayv1.PushStreamResponse{}, nil
	}

	for _, username := range req.ToUsernames {
		conn, ok := s.connMgr.GetConnection(username)
		if !ok {
			failedUsernames = append(failedUsernames, username)
			continue
		}
		if err := conn.Send(packet); err != nil {
			failedUsernames = append(failedUsernames, username)
		}
	}

	return &gatewayv1.PushStreamResponse{FailedUsernames: failedUsernames}, nil
}

func (s *Service) RegisterGRPC(server *grpc.Server) {
	gatewayv1.RegisterPushServiceServer(server, s)
}
