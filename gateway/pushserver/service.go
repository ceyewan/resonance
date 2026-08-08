package pushserver

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/ceyewan/genesis/clog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/observability"
	"github.com/ceyewan/resonance/gateway/transport/ws"
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
	if err := validatePushStream(req); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
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
		return nil, status.Error(codes.InvalidArgument, "stream payload is unsupported")
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

func validatePushStream(req *gatewayv1.PushStreamRequest) error {
	if req == nil || len(req.GetToUsernames()) == 0 || len(req.GetToUsernames()) > 64 {
		return fmt.Errorf("stream targets are invalid")
	}
	seen := make(map[string]struct{}, len(req.GetToUsernames()))
	for _, username := range req.GetToUsernames() {
		if username == "" || len(username) > 64 || !utf8.ValidString(username) {
			return fmt.Errorf("stream target is invalid")
		}
		if _, ok := seen[username]; ok {
			return fmt.Errorf("stream target is duplicated")
		}
		seen[username] = struct{}{}
	}
	validIdentity := func(runID, streamID, sessionID string) bool {
		return runID != "" && len(runID) <= 128 && streamID != "" && len(streamID) <= 128 &&
			sessionID != "" && len(sessionID) <= 128
	}
	switch payload := req.GetPayload().(type) {
	case *gatewayv1.PushStreamRequest_StreamBegin:
		begin := payload.StreamBegin
		if begin == nil || !validIdentity(begin.GetRunId(), begin.GetStreamId(), begin.GetSessionId()) ||
			begin.GetFromUsername() == "" || begin.GetSourceEventId() <= 0 || begin.GetFinalClientMsgId() == "" {
			return fmt.Errorf("stream begin is invalid")
		}
	case *gatewayv1.PushStreamRequest_StreamChunk:
		chunk := payload.StreamChunk
		if chunk == nil || !validIdentity(chunk.GetRunId(), chunk.GetStreamId(), chunk.GetSessionId()) ||
			chunk.GetStreamSequence() == 0 || chunk.GetDelta() == "" || len(chunk.GetDelta()) > 64<<10 ||
			!utf8.ValidString(chunk.GetDelta()) {
			return fmt.Errorf("stream chunk is invalid")
		}
	case *gatewayv1.PushStreamRequest_StreamEnd:
		end := payload.StreamEnd
		if end == nil || !validIdentity(end.GetRunId(), end.GetStreamId(), end.GetSessionId()) ||
			end.GetStreamSequence() == 0 || end.GetFinalClientMsgId() == "" ||
			(end.GetReason() != gatewayv1.StreamFinishReason_STREAM_FINISH_REASON_STOP &&
				end.GetReason() != gatewayv1.StreamFinishReason_STREAM_FINISH_REASON_ERROR) {
			return fmt.Errorf("stream end is invalid")
		}
	case *gatewayv1.PushStreamRequest_Typing:
		if payload.Typing == nil || payload.Typing.GetSessionId() == "" || payload.Typing.GetFromUsername() == "" {
			return fmt.Errorf("typing signal is invalid")
		}
	default:
		return fmt.Errorf("stream payload is unsupported")
	}
	return nil
}

func (s *Service) RegisterGRPC(server *grpc.Server) {
	gatewayv1.RegisterPushServiceServer(server, s)
}
