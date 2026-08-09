package streaming

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/ceyewan/genesis/clog"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/repo"
	"github.com/ceyewan/resonance/task/observability"
	"github.com/ceyewan/resonance/task/pusher"
)

type Dispatcher struct {
	routers repo.RouterRepo
	pushers pusher.PusherManager
	logger  clog.Logger
}

func NewDispatcher(routers repo.RouterRepo, pushers pusher.PusherManager, logger clog.Logger) (*Dispatcher, error) {
	if routers == nil || pushers == nil || logger == nil {
		return nil, fmt.Errorf("agent stream dispatcher dependencies are incomplete")
	}
	return &Dispatcher{routers: routers, pushers: pushers, logger: logger}, nil
}

// Handle routes an ephemeral stream event directly to online Gateways. It
// intentionally has no MessageRepo dependency and cannot write Inbox facts.
func (d *Dispatcher) Handle(ctx context.Context, event *mqv1.AgentStreamEvent) error {
	if err := ValidateEvent(event, 64<<10); err != nil {
		return err
	}
	routers, err := d.routers.BatchGetUsersGateway(ctx, event.GetTargetUsernames())
	if err != nil {
		return fmt.Errorf("query agent stream routes: %w", err)
	}
	groups := make(map[string][]string)
	traceHeaders := make(map[string]string, 2)
	observability.InjectTraceContext(ctx, traceHeaders)
	for _, router := range routers {
		if router == nil || router.GatewayID == "" || router.Username == "" {
			continue
		}
		groups[router.GatewayID] = append(groups[router.GatewayID], router.Username)
	}
	for gatewayID, usernames := range groups {
		client, err := d.pushers.GetClient(gatewayID)
		if err != nil {
			d.logger.Debug("drop agent stream for unavailable gateway",
				clog.String("gateway_id", gatewayID), clog.String("run_id", event.GetRunId()))
			continue
		}
		request := toGatewayRequest(event)
		if err := client.Enqueue(&pusher.PushTask{ToUsernames: usernames, Stream: request, TraceHeaders: traceHeaders}); err != nil {
			d.logger.Debug("drop agent stream for full gateway queue",
				clog.String("gateway_id", gatewayID), clog.String("run_id", event.GetRunId()))
		}
	}
	return nil
}

func ValidateEvent(event *mqv1.AgentStreamEvent, maxDeltaBytes int) error {
	if event == nil || event.GetTenantId() == "" || event.GetRunId() == "" || event.GetStreamId() == "" ||
		event.GetSessionId() == "" || event.GetFromUsername() == "" || event.GetSourceEventId() <= 0 ||
		event.GetFinalClientMsgId() == "" || len(event.GetTargetUsernames()) == 0 || len(event.GetTargetUsernames()) > 64 {
		return fmt.Errorf("agent stream identity is incomplete")
	}
	for _, value := range []string{event.GetTenantId(), event.GetRunId(), event.GetStreamId(), event.GetSessionId(), event.GetFromUsername(), event.GetFinalClientMsgId()} {
		if len(value) > 128 || strings.TrimSpace(value) == "" || !utf8.ValidString(value) {
			return fmt.Errorf("agent stream identity is invalid")
		}
	}
	seen := make(map[string]struct{}, len(event.GetTargetUsernames()))
	for _, username := range event.GetTargetUsernames() {
		if username == "" || len(username) > 64 || !utf8.ValidString(username) {
			return fmt.Errorf("agent stream target is invalid")
		}
		if _, ok := seen[username]; ok {
			return fmt.Errorf("agent stream target is duplicated")
		}
		seen[username] = struct{}{}
	}
	if len(event.GetTraceHeaders()) > 32 {
		return fmt.Errorf("agent stream trace headers exceed limit")
	}
	for key, value := range event.GetTraceHeaders() {
		if key == "" || len(key) > 128 || len(value) > 1024 {
			return fmt.Errorf("agent stream trace header is invalid")
		}
	}
	switch payload := event.GetPayload().(type) {
	case *mqv1.AgentStreamEvent_Begin:
		if payload.Begin == nil || event.GetSequence() != 0 {
			return fmt.Errorf("agent stream begin is invalid")
		}
	case *mqv1.AgentStreamEvent_Chunk:
		if payload.Chunk == nil || event.GetSequence() == 0 || payload.Chunk.GetDelta() == "" ||
			len(payload.Chunk.GetDelta()) > maxDeltaBytes || !utf8.ValidString(payload.Chunk.GetDelta()) {
			return fmt.Errorf("agent stream chunk is invalid")
		}
	case *mqv1.AgentStreamEvent_End:
		if payload.End == nil || event.GetSequence() == 0 ||
			(payload.End.GetReason() != mqv1.AgentStreamFinishReason_AGENT_STREAM_FINISH_REASON_STOP &&
				payload.End.GetReason() != mqv1.AgentStreamFinishReason_AGENT_STREAM_FINISH_REASON_ERROR) {
			return fmt.Errorf("agent stream end is invalid")
		}
	default:
		return fmt.Errorf("agent stream payload is missing")
	}
	return nil
}

func toGatewayRequest(event *mqv1.AgentStreamEvent) *gatewayv1.PushStreamRequest {
	legacySequence := int32(0)
	if event.GetSequence() <= math.MaxInt32 {
		legacySequence = int32(event.GetSequence())
	}
	request := &gatewayv1.PushStreamRequest{}
	switch payload := event.GetPayload().(type) {
	case *mqv1.AgentStreamEvent_Begin:
		request.Payload = &gatewayv1.PushStreamRequest_StreamBegin{StreamBegin: &gatewayv1.StreamBegin{
			ParentEventId: event.GetSourceEventId(), SessionId: event.GetSessionId(), FromUsername: event.GetFromUsername(),
			RunId: event.GetRunId(), StreamId: event.GetStreamId(), SourceEventId: event.GetSourceEventId(),
			FinalClientMsgId: event.GetFinalClientMsgId(),
		}}
	case *mqv1.AgentStreamEvent_Chunk:
		request.Payload = &gatewayv1.PushStreamRequest_StreamChunk{StreamChunk: &gatewayv1.StreamChunk{
			ParentEventId: event.GetSourceEventId(), Sequence: legacySequence, Delta: payload.Chunk.GetDelta(),
			RunId: event.GetRunId(), StreamId: event.GetStreamId(), StreamSequence: event.GetSequence(),
			SessionId: event.GetSessionId(),
		}}
	case *mqv1.AgentStreamEvent_End:
		reason := gatewayv1.StreamFinishReason_STREAM_FINISH_REASON_STOP
		if payload.End.GetReason() == mqv1.AgentStreamFinishReason_AGENT_STREAM_FINISH_REASON_ERROR {
			reason = gatewayv1.StreamFinishReason_STREAM_FINISH_REASON_ERROR
		}
		request.Payload = &gatewayv1.PushStreamRequest_StreamEnd{StreamEnd: &gatewayv1.StreamEnd{
			ParentEventId: event.GetSourceEventId(), Reason: reason, RunId: event.GetRunId(), StreamId: event.GetStreamId(),
			StreamSequence: event.GetSequence(), SessionId: event.GetSessionId(), FinalClientMsgId: event.GetFinalClientMsgId(),
		}}
	}
	return request
}
