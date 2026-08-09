package ingress

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ceyewan/genesis/mq"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func TestHandler_AdmitsTextEventAndPersistsDurableRun(t *testing.T) {
	now := time.Date(2026, 8, 8, 11, 0, 0, 0, time.UTC)
	enqueuer := &fakeEnqueuer{}
	admission := &fakeAdmissionController{admission: testAdmission()}
	handler, err := NewHandler(
		HandlerConfig{TenantID: "tenant-a", BotUsername: "agent-bot", MaxPromptBytes: 1024},
		enqueuer,
		admission,
		WithClock(func() time.Time { return now }),
		WithRunIDSource(func() (string, error) { return "run-ingress-1", nil }),
	)
	require.NoError(t, err)
	event := testMQEvent()

	result, err := handler.Handle(context.Background(), event)
	require.NoError(t, err)
	require.False(t, result.Ignored)
	require.True(t, result.Created)
	require.Equal(t, "run-ingress-1", result.Run.RunID)
	require.Len(t, enqueuer.runs, 1)
	run := enqueuer.runs[0]
	require.Equal(t, "tenant-a", run.TenantID)
	require.Equal(t, event.Event.SessionId, run.ConversationID)
	require.Equal(t, event.Event.EventId, run.SourceEventID)
	require.Equal(t, event.Event.SeqId, run.SourceSeqID)
	require.Equal(t, event.Event.GetMessage().Content, run.Prompt)
	require.Equal(t, now, run.QueuedAt)
	require.Len(t, run.SourceHash, 64)
	require.JSONEq(t, `{"traceparent":"00-test"}`, string(run.TraceContext))

	expectedHash, err := hashSourceEvent("tenant-a", event.Event)
	require.NoError(t, err)
	require.Equal(t, expectedHash, run.SourceHash)

	changedTrace := proto.Clone(event).(*mqv1.MQEvent)
	changedTrace.TraceHeaders["traceparent"] = "00-other"
	sameHash, err := hashSourceEvent("tenant-a", changedTrace.Event)
	require.NoError(t, err)
	require.Equal(t, expectedHash, sameHash, "transport tracing must not change source idempotency")
}

func TestHandler_IgnoresBotStreamAndNonMessageEvents(t *testing.T) {
	enqueuer := &fakeEnqueuer{}
	handler, err := NewHandler(
		HandlerConfig{TenantID: "tenant-a", BotUsername: "agent-bot"},
		enqueuer, &fakeAdmissionController{admission: testAdmission()},
	)
	require.NoError(t, err)

	tests := map[string]func(*mqv1.MQEvent){
		"bot":             func(event *mqv1.MQEvent) { event.Event.FromUsername = "agent-bot" },
		"stream":          func(event *mqv1.MQEvent) { event.Event.GetMessage().Type = commonv1.MessageType_MESSAGE_TYPE_AI_STREAM },
		"agent client id": func(event *mqv1.MQEvent) { event.Event.GetMessage().ClientMsgId = "agent:previous:final" },
		"recall": func(event *mqv1.MQEvent) {
			event.Event.Payload = &commonv1.ChatEvent_Recall{Recall: &commonv1.MessageRecall{TargetEventId: 1}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			event := testMQEvent()
			mutate(event)
			result, handleErr := handler.Handle(context.Background(), event)
			require.NoError(t, handleErr)
			require.True(t, result.Ignored)
		})
	}
	require.Empty(t, enqueuer.runs)
}

func TestHandler_FailsClosedOnAdmissionMismatchAndSourceConflict(t *testing.T) {
	event := testMQEvent()
	mismatch := testAdmission()
	mismatch.ActorUsername = "other-user"
	handler, err := NewHandler(
		HandlerConfig{TenantID: "tenant-a", BotUsername: "agent-bot"},
		&fakeEnqueuer{}, &fakeAdmissionController{admission: mismatch},
	)
	require.NoError(t, err)
	_, err = handler.Handle(context.Background(), event)
	require.ErrorIs(t, err, ErrAdmissionMismatch)
	require.True(t, IsPermanent(err))

	conflictingEnqueuer := &fakeEnqueuer{err: repo.ErrAgentRunSourceConflict}
	handler, err = NewHandler(
		HandlerConfig{TenantID: "tenant-a", BotUsername: "agent-bot"},
		conflictingEnqueuer, &fakeAdmissionController{admission: testAdmission()},
		WithRunIDSource(func() (string, error) { return "run-conflict", nil }),
	)
	require.NoError(t, err)
	_, err = handler.Handle(context.Background(), event)
	require.ErrorIs(t, err, repo.ErrAgentRunSourceConflict)
	require.True(t, IsPermanent(err))
}

func TestHandler_ProfileAdmissionDenialIsPermanentAndNeverEnqueues(t *testing.T) {
	enqueuer := &fakeEnqueuer{}
	handler, err := NewHandler(
		HandlerConfig{TenantID: "tenant-a", BotUsername: "agent-bot", MaxPromptBytes: 1024},
		enqueuer, &fakeAdmissionController{err: ErrAdmissionDenied},
	)
	require.NoError(t, err)

	_, err = handler.Handle(context.Background(), testMQEvent())
	require.Error(t, err)
	require.True(t, IsPermanent(err))
	require.Empty(t, enqueuer.runs)
}

func TestConsumer_AcksOnlyAfterDurableEnqueueAndRoutesPermanentFailuresToDLQ(t *testing.T) {
	recorder := &ingressRecorder{}
	enqueuer := &fakeEnqueuer{recorder: recorder}
	handler, err := NewHandler(
		HandlerConfig{TenantID: "tenant-a", BotUsername: "agent-bot"},
		enqueuer, &fakeAdmissionController{admission: testAdmission()},
		WithRunIDSource(func() (string, error) { return "run-consumer", nil }),
	)
	require.NoError(t, err)
	mqClient := &fakeMQ{recorder: recorder}
	consumer, err := NewConsumer(ConsumerConfig{
		Topic: "resonance.chat.event.v1", QueueGroup: "resonance_group_agent_ingress",
		DLQTopic: "resonance.chat.event.v1.agent.dlq", MaxInflight: 10,
	}, mqClient, handler)
	require.NoError(t, err)

	payload, err := proto.Marshal(testMQEvent())
	require.NoError(t, err)
	message := &fakeMessage{data: payload, topic: "resonance.chat.event.v1", recorder: recorder}
	require.NoError(t, consumer.handleMessage(message))
	require.Equal(t, []string{"enqueue", "ack"}, recorder.snapshot())
	require.Equal(t, 1, message.acks)
	require.Zero(t, message.naks)

	recorder.reset()
	malformed := &fakeMessage{data: []byte("not protobuf"), topic: "resonance.chat.event.v1", recorder: recorder}
	require.NoError(t, consumer.handleMessage(malformed))
	require.Equal(t, []string{"publish-dlq", "ack"}, recorder.snapshot())
	require.Equal(t, 1, malformed.acks)
}

func TestConsumer_NaksTransientFailureAndRetainsMalformedMessageWhenDLQFails(t *testing.T) {
	recorder := &ingressRecorder{}
	handler, err := NewHandler(
		HandlerConfig{TenantID: "tenant-a", BotUsername: "agent-bot"},
		&fakeEnqueuer{}, &fakeAdmissionController{err: errors.New("authoritative IAM unavailable")},
	)
	require.NoError(t, err)
	consumer, err := NewConsumer(ConsumerConfig{Topic: "topic", QueueGroup: "agent-group", DLQTopic: "dlq", MaxInflight: 1}, &fakeMQ{recorder: recorder}, handler)
	require.NoError(t, err)
	payload, err := proto.Marshal(testMQEvent())
	require.NoError(t, err)
	message := &fakeMessage{data: payload, topic: "topic", recorder: recorder}
	require.Error(t, consumer.handleMessage(message))
	require.Zero(t, message.acks)
	require.Equal(t, 1, message.naks)

	recorder.reset()
	failingMQ := &fakeMQ{recorder: recorder, publishErr: errors.New("DLQ unavailable")}
	consumer, err = NewConsumer(ConsumerConfig{Topic: "topic", QueueGroup: "agent-group", DLQTopic: "dlq", MaxInflight: 1}, failingMQ, handler)
	require.NoError(t, err)
	malformed := &fakeMessage{data: []byte("malformed"), topic: "topic", recorder: recorder}
	require.Error(t, consumer.handleMessage(malformed))
	require.Zero(t, malformed.acks)
	require.Equal(t, 1, malformed.naks)
}

func testMQEvent() *mqv1.MQEvent {
	return &mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId: 1001, SeqId: 3, SessionId: "conversation-a", FromUsername: "user-1", TimestampMs: 1786154000000,
			Payload: &commonv1.ChatEvent_Message{Message: &commonv1.Message{
				Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "who am I?", ClientMsgId: "client-1",
			}},
		},
		TargetUsernames: []string{"agent-bot"},
		TraceHeaders:    map[string]string{"traceparent": "00-test"},
	}
}

func testAdmission() Admission {
	return Admission{
		Trigger: true, TenantID: "tenant-a", ConversationID: "conversation-a",
		ActorID: "user-1", ActorUsername: "user-1",
		ProfileID: "user-assistant", ProfileVersion: 1,
		RuntimeKind: "pi", RuntimeVersion: "0.50.1", BridgeVersion: "1.0.0",
		ModelProvider: "anthropic", ModelID: "claude-sonnet-4-5", MaxAttempts: 3,
	}
}

type fakeEnqueuer struct {
	mu       sync.Mutex
	runs     []*model.AgentRun
	err      error
	recorder *ingressRecorder
}

func (e *fakeEnqueuer) EnqueueAgentRun(_ context.Context, run *model.AgentRun) (*repo.AgentRunEnqueueResult, error) {
	if e.recorder != nil {
		e.recorder.add("enqueue")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	clone := *run
	e.runs = append(e.runs, &clone)
	if e.err != nil {
		return nil, e.err
	}
	return &repo.AgentRunEnqueueResult{Run: &clone, Created: true}, nil
}

type fakeAdmissionController struct {
	admission Admission
	err       error
}

func (c *fakeAdmissionController) Admit(context.Context, string, *commonv1.ChatEvent) (Admission, error) {
	return c.admission, c.err
}

type fakeMQ struct {
	recorder   *ingressRecorder
	publishErr error
	handler    mq.Handler
}

func (m *fakeMQ) Publish(_ context.Context, topic string, _ []byte, _ ...mq.PublishOption) error {
	m.recorder.add("publish-" + map[bool]string{true: "dlq", false: topic}[topic == "dlq" || topic == "resonance.chat.event.v1.agent.dlq"])
	return m.publishErr
}

func (m *fakeMQ) Subscribe(_ context.Context, _ string, handler mq.Handler, _ ...mq.SubscribeOption) (mq.Subscription, error) {
	m.handler = handler
	return &fakeSubscription{done: make(chan struct{})}, nil
}
func (*fakeMQ) Close() error                { return nil }
func (*fakeMQ) Drain(context.Context) error { return nil }

type fakeSubscription struct {
	once sync.Once
	done chan struct{}
}

func (s *fakeSubscription) Unsubscribe() error {
	s.once.Do(func() { close(s.done) })
	return nil
}
func (s *fakeSubscription) Done() <-chan struct{}       { return s.done }
func (s *fakeSubscription) Drain(context.Context) error { return s.Unsubscribe() }

type fakeMessage struct {
	data     []byte
	topic    string
	headers  mq.Headers
	recorder *ingressRecorder
	acks     int
	naks     int
}

func (*fakeMessage) Context() context.Context { return context.Background() }
func (m *fakeMessage) Topic() string          { return m.topic }
func (m *fakeMessage) Data() []byte           { return m.data }
func (m *fakeMessage) Headers() mq.Headers    { return m.headers.Clone() }
func (m *fakeMessage) Ack() error {
	m.acks++
	m.recorder.add("ack")
	return nil
}
func (m *fakeMessage) Nak() error {
	m.naks++
	m.recorder.add("nak")
	return nil
}
func (m *fakeMessage) NakWithDelay(time.Duration) error { return m.Nak() }
func (*fakeMessage) ID() string                         { return "test:1" }

type ingressRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *ingressRecorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}
func (r *ingressRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}
func (r *ingressRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
}
