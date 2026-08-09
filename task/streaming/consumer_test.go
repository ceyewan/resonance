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

func newConsumerForTest(t *testing.T, mqClient *fakeMQ, handler HandlerFunc) *Consumer {
	t.Helper()
	consumer, err := NewConsumer(mqClient, handler, config.ConsumerConfig{
		Topic: "stream", QueueGroup: "stream-group", DLQTopic: "stream.dlq",
		WorkerCount: 1, MaxRetry: 2, RetryInterval: 0,
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
	mu        sync.Mutex
	published []string
}

func (m *fakeMQ) Publish(_ context.Context, topic string, _ []byte, _ ...mq.PublishOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = append(m.published, topic)
	return nil
}
func (m *fakeMQ) Subscribe(context.Context, string, mq.Handler, ...mq.SubscribeOption) (mq.Subscription, error) {
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
	topic  string
	data   []byte
	acked  bool
	nacked bool
}

func (m *fakeMessage) Context() context.Context         { return context.Background() }
func (m *fakeMessage) Topic() string                    { return m.topic }
func (m *fakeMessage) Data() []byte                     { return m.data }
func (m *fakeMessage) Headers() mq.Headers              { return nil }
func (m *fakeMessage) ID() string                       { return "id" }
func (m *fakeMessage) Ack() error                       { m.acked = true; return nil }
func (m *fakeMessage) Nak() error                       { m.nacked = true; return nil }
func (m *fakeMessage) NakWithDelay(time.Duration) error { return m.Nak() }
