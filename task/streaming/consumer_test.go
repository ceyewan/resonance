package streaming

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/task/config"
)

func TestStreamConsumer_ValidEventAcked(t *testing.T) {
	client := &fakeMQ{}
	calls := 0
	consumer := newConsumerForTest(t, client, func(_ context.Context, event *mqv1.AgentStreamEvent) error {
		calls++
		require.Equal(t, "run-1", event.GetRunId())
		return nil
	})
	message := &fakeMessage{topic: "stream", data: marshalStream(t, validStreamEvent())}
	require.NoError(t, consumer.handleMessage(message))
	require.Equal(t, 1, calls)
	require.True(t, message.acked)
	require.False(t, message.nacked)
	require.Positive(t, message.progress)
}

func TestStreamConsumerRestoresPersistedTraceContext(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	event := validStreamEvent()
	event.TraceHeaders = map[string]string{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	consumer := newConsumerForTest(t, &fakeMQ{}, func(ctx context.Context, _ *mqv1.AgentStreamEvent) error {
		require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", trace.SpanContextFromContext(ctx).TraceID().String())
		return nil
	})

	require.NoError(t, consumer.handleMessage(&fakeMessage{topic: "stream", data: marshalStream(t, event)}))
}

func TestStreamConsumer_HandlerFailureIsBoundedlyRetriedThenDropped(t *testing.T) {
	client := &fakeMQ{}
	calls := 0
	consumer := newConsumerForTest(t, client, func(context.Context, *mqv1.AgentStreamEvent) error {
		calls++
		return errors.New("router unavailable")
	})
	message := &fakeMessage{topic: "stream", data: marshalStream(t, validStreamEvent())}
	err := consumer.handleMessage(message)
	require.ErrorContains(t, err, "router unavailable")
	require.Equal(t, 2, calls)
	require.True(t, message.acked)
	require.False(t, message.nacked)
}

func TestStreamConsumer_HeartbeatRepeatsAndStopsBeforeAck(t *testing.T) {
	consumer := newConsumerForTest(t, &fakeMQ{}, func(context.Context, *mqv1.AgentStreamEvent) error {
		time.Sleep(5 * time.Millisecond)
		return nil
	})
	message := &fakeMessage{topic: "stream", data: marshalStream(t, validStreamEvent())}

	require.NoError(t, consumer.handleMessage(message))
	require.Greater(t, message.progress, 1)
	progressAfterAck := message.progress
	time.Sleep(3 * time.Millisecond)
	require.Equal(t, progressAfterAck, message.progress)
}

func TestStreamConsumer_MalformedAndInvalidEventsGoToDLQ(t *testing.T) {
	client := &fakeMQ{}
	consumer := newConsumerForTest(t, client, func(context.Context, *mqv1.AgentStreamEvent) error {
		t.Fatal("invalid event must not reach handler")
		return nil
	})
	malformed := &fakeMessage{topic: "stream", data: []byte("bad")}
	require.NoError(t, consumer.handleMessage(malformed))
	require.True(t, malformed.acked)

	invalidEvent := validStreamEvent()
	invalidEvent.RunId = ""
	invalid := &fakeMessage{topic: "stream", data: marshalStream(t, invalidEvent)}
	require.NoError(t, consumer.handleMessage(invalid))
	require.True(t, invalid.acked)
	require.Equal(t, []string{"stream.dlq", "stream.dlq"}, client.publishedTopics())
}

func TestStreamConsumer_DLQAndNakFailuresAreBothReturned(t *testing.T) {
	dlqErr := errors.New("dlq unavailable")
	nakErr := errors.New("nak unavailable")
	client := &fakeMQ{publishErr: dlqErr}
	consumer := newConsumerForTest(t, client, func(context.Context, *mqv1.AgentStreamEvent) error { return nil })
	message := &fakeMessage{topic: "stream", data: []byte("bad"), nakErr: nakErr}

	err := consumer.handleMessage(message)
	require.ErrorIs(t, err, dlqErr)
	require.ErrorIs(t, err, nakErr)
	require.True(t, message.nacked)
}

func TestStreamConsumer_StopCancelsContextAwareHandler(t *testing.T) {
	started := make(chan struct{})
	var transportHandler mq.Handler
	client := &fakeMQ{subscribeFn: func(_ context.Context, _ string, handler mq.Handler, _ ...mq.SubscribeOption) (mq.Subscription, error) {
		transportHandler = handler
		return &fakeSubscription{}, nil
	}}
	consumer, err := NewConsumer(client, func(ctx context.Context, _ *mqv1.AgentStreamEvent) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, config.ConsumerConfig{
		Topic: "stream", QueueGroup: "stream-group", DLQTopic: "stream.dlq",
		WorkerCount: 1, MaxRetry: 1, ProgressInterval: time.Millisecond,
	}, 1024, clog.Discard())
	require.NoError(t, err)
	require.NoError(t, consumer.Start())
	payload := marshalStream(t, validStreamEvent())
	go func() {
		_ = transportHandler(&fakeMessage{topic: "stream", data: payload})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- consumer.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop waited for a handler before canceling its context")
	}
}

func newConsumerForTest(t *testing.T, mqClient *fakeMQ, handler HandlerFunc) *Consumer {
	t.Helper()
	consumer, err := NewConsumer(mqClient, handler, config.ConsumerConfig{
		Topic: "stream", QueueGroup: "stream-group", DLQTopic: "stream.dlq",
		WorkerCount: 1, MaxRetry: 2, RetryInterval: 0, ProgressInterval: time.Millisecond,
	}, 1024, clog.Discard())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, consumer.Stop()) })
	return consumer
}

func marshalStream(t *testing.T, event *mqv1.AgentStreamEvent) []byte {
	t.Helper()
	payload, err := proto.Marshal(event)
	require.NoError(t, err)
	return payload
}

type fakeMQ struct {
	mu          sync.Mutex
	published   []string
	publishErr  error
	subscribeFn func(context.Context, string, mq.Handler, ...mq.SubscribeOption) (mq.Subscription, error)
}

func (m *fakeMQ) Publish(_ context.Context, topic string, _ []byte, _ ...mq.PublishOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = append(m.published, topic)
	return m.publishErr
}
func (m *fakeMQ) Subscribe(ctx context.Context, topic string, handler mq.Handler, opts ...mq.SubscribeOption) (mq.Subscription, error) {
	if m.subscribeFn != nil {
		return m.subscribeFn(ctx, topic, handler, opts...)
	}
	return &fakeSubscription{}, nil
}
func (m *fakeMQ) Close() error                { return nil }
func (m *fakeMQ) Drain(context.Context) error { return nil }
func (m *fakeMQ) publishedTopics() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.published...)
}

type fakeSubscription struct{}

func (*fakeSubscription) Unsubscribe() error          { return nil }
func (*fakeSubscription) Done() <-chan struct{}       { return make(chan struct{}) }
func (*fakeSubscription) Drain(context.Context) error { return nil }

type fakeMessage struct {
	topic    string
	data     []byte
	acked    bool
	nacked   bool
	progress int
	ackErr   error
	nakErr   error
}

func (m *fakeMessage) Context() context.Context         { return context.Background() }
func (m *fakeMessage) Topic() string                    { return m.topic }
func (m *fakeMessage) Data() []byte                     { return m.data }
func (m *fakeMessage) Headers() mq.Headers              { return nil }
func (m *fakeMessage) ID() string                       { return "id" }
func (m *fakeMessage) Ack() error                       { m.acked = true; return m.ackErr }
func (m *fakeMessage) Nak() error                       { m.nacked = true; return m.nakErr }
func (m *fakeMessage) NakWithDelay(time.Duration) error { return m.Nak() }
func (m *fakeMessage) InProgress() error                { m.progress++; return nil }
