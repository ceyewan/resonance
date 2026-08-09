package streaming

import (
	"context"
	"errors"
	"testing"

	"github.com/ceyewan/genesis/clog"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/task/pusher"
)

func TestDispatcher_RoutesStreamWithoutInboxDependency(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	client := &fakeClient{}
	dispatcher, err := NewDispatcher(&fakeRouterRepo{routers: []*model.Router{{Username: "alice", GatewayID: "gateway-1"}}},
		&fakePusherManager{client: client}, clog.Discard())
	require.NoError(t, err)
	event := validStreamEvent()
	event.Sequence = 7
	event.Payload = &mqv1.AgentStreamEvent_Chunk{Chunk: &mqv1.AgentStreamChunk{Delta: "hello"}}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{3}, SpanID: trace.SpanID{4}, TraceFlags: trace.FlagsSampled,
	})
	require.NoError(t, dispatcher.Handle(trace.ContextWithSpanContext(context.Background(), spanContext), event))
	require.Len(t, client.tasks, 1)
	request := client.tasks[0].Stream
	require.Equal(t, []string{"alice"}, client.tasks[0].ToUsernames)
	require.Equal(t, "run-1", request.GetStreamChunk().GetRunId())
	require.Equal(t, "stream-1", request.GetStreamChunk().GetStreamId())
	require.Equal(t, uint64(7), request.GetStreamChunk().GetStreamSequence())
	require.Equal(t, int32(7), request.GetStreamChunk().GetSequence()) //nolint:staticcheck // Intentional legacy compatibility assertion.
	require.Equal(t, "hello", request.GetStreamChunk().GetDelta())
	require.NotEmpty(t, client.tasks[0].TraceHeaders["traceparent"])
}

func TestDispatcher_DropsWhenGatewayQueueIsFull(t *testing.T) {
	client := &fakeClient{enqueueErr: errors.New("full")}
	dispatcher, err := NewDispatcher(&fakeRouterRepo{routers: []*model.Router{{Username: "alice", GatewayID: "gateway-1"}}},
		&fakePusherManager{client: client}, clog.Discard())
	require.NoError(t, err)
	require.NoError(t, dispatcher.Handle(context.Background(), validStreamEvent()))
}

func TestValidateEventRejectsMalformedAndOversizedDelta(t *testing.T) {
	event := validStreamEvent()
	event.Sequence = 1
	event.Payload = &mqv1.AgentStreamEvent_Chunk{Chunk: &mqv1.AgentStreamChunk{Delta: "too-large"}}
	require.Error(t, ValidateEvent(event, 4))
	event.Payload = nil
	require.Error(t, ValidateEvent(event, 1024))
	event = validStreamEvent()
	event.Sequence = 1
	require.Error(t, ValidateEvent(event, 1024), "begin must use sequence zero")
}

func validStreamEvent() *mqv1.AgentStreamEvent {
	return &mqv1.AgentStreamEvent{
		TenantId: "tenant-1", RunId: "run-1", StreamId: "stream-1", SessionId: "session-1",
		FromUsername: "resonance-agent", TargetUsernames: []string{"alice"}, SourceEventId: 41,
		FinalClientMsgId: "agent:run-1:final", Sequence: 0,
		Payload: &mqv1.AgentStreamEvent_Begin{Begin: &mqv1.AgentStreamBegin{}},
	}
}

type fakeClient struct {
	tasks      []*pusher.PushTask
	enqueueErr error
}

func (c *fakeClient) Enqueue(task *pusher.PushTask) error {
	c.tasks = append(c.tasks, task)
	return c.enqueueErr
}
func (c *fakeClient) QueueSize() int { return len(c.tasks) }

type fakePusherManager struct{ client pusher.Client }

func (m *fakePusherManager) Start() error { return nil }
func (m *fakePusherManager) GetClient(string) (pusher.Client, error) {
	if m.client == nil {
		return nil, errors.New("missing")
	}
	return m.client, nil
}
func (m *fakePusherManager) Close() {}

type fakeRouterRepo struct {
	routers []*model.Router
	err     error
}

func (r *fakeRouterRepo) BatchGetUsersGateway(context.Context, []string) ([]*model.Router, error) {
	return r.routers, r.err
}
func (r *fakeRouterRepo) SetUserGateway(context.Context, *model.Router) error { return nil }
func (r *fakeRouterRepo) GetUserGateway(context.Context, string) (*model.Router, error) {
	return nil, errors.New("not found")
}
func (r *fakeRouterRepo) DeleteUserGateway(context.Context, string) error            { return nil }
func (r *fakeRouterRepo) BatchSetUserGateway(context.Context, []*model.Router) error { return nil }
func (r *fakeRouterRepo) BatchDeleteUserGateway(context.Context, []string) error     { return nil }
func (r *fakeRouterRepo) Close() error                                               { return nil }
