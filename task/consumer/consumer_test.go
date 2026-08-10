package consumer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/task/config"
)

type testMQ struct {
	subscribeFn func(ctx context.Context, topic string, handler mq.Handler, opts ...mq.SubscribeOption) (mq.Subscription, error)
	publishErr  error
}

func (m *testMQ) Publish(ctx context.Context, topic string, data []byte, opts ...mq.PublishOption) error {
	return m.publishErr
}

func (m *testMQ) Subscribe(ctx context.Context, topic string, handler mq.Handler, opts ...mq.SubscribeOption) (mq.Subscription, error) {
	if m.subscribeFn != nil {
		return m.subscribeFn(ctx, topic, handler, opts...)
	}
	return &testSubscription{done: make(chan struct{})}, nil
}

func (m *testMQ) Close() error                { return nil }
func (m *testMQ) Drain(context.Context) error { return nil }

type testSubscription struct {
	unsubscribed bool
	done         chan struct{}
}

func (s *testSubscription) Unsubscribe() error {
	if !s.unsubscribed {
		s.unsubscribed = true
		close(s.done)
	}
	return nil
}

func (s *testSubscription) Done() <-chan struct{} {
	return s.done
}
func (s *testSubscription) Drain(context.Context) error { return s.Unsubscribe() }

type testMessage struct {
	data      []byte
	headers   mq.Headers
	ackErr    error
	nakErr    error
	acked     bool
	nacked    bool
	progress  int
	topic     string
	messageID string
}

func (m *testMessage) Context() context.Context { return context.Background() }
func (m *testMessage) Topic() string            { return m.topic }
func (m *testMessage) Data() []byte             { return m.data }
func (m *testMessage) Headers() mq.Headers      { return m.headers.Clone() }
func (m *testMessage) ID() string               { return m.messageID }
func (m *testMessage) Ack() error {
	m.acked = true
	return m.ackErr
}
func (m *testMessage) Nak() error {
	m.nacked = true
	return m.nakErr
}
func (m *testMessage) NakWithDelay(time.Duration) error { return m.Nak() }
func (m *testMessage) InProgress() error {
	m.progress++
	return nil
}

func TestNewConsumer_DefaultWorkerCount(t *testing.T) {
	c := NewConsumer(
		&testMQ{},
		func(ctx context.Context, event *mqv1.MQEvent) error { return nil },
		config.ConsumerConfig{
			Topic:       "t",
			QueueGroup:  "g",
			WorkerCount: 0,
		},
		clog.Discard(),
	)

	require.Equal(t, 10, c.config.WorkerCount)
	require.Equal(t, 0, cap(c.jobsCh))
	require.Equal(t, 10*time.Second, c.config.ProgressInterval)
}

func TestConsumer_handleMessage_MalformedEventAcked(t *testing.T) {
	c := NewConsumer(
		&testMQ{},
		func(ctx context.Context, event *mqv1.MQEvent) error { return nil },
		config.ConsumerConfig{
			Topic:         "t",
			QueueGroup:    "g",
			WorkerCount:   1,
			MaxRetry:      1,
			RetryInterval: 0,
		},
		clog.Discard(),
	)

	msg := &testMessage{data: []byte("bad-data")}
	err := c.handleMessage(context.Background(), msg)
	require.NoError(t, err)
	require.True(t, msg.acked)
	require.False(t, msg.nacked)
	require.Positive(t, msg.progress)
}

func TestConsumer_handleMessage_MalformedEventDLQFailureIsNacked(t *testing.T) {
	dlqErr := errors.New("dlq unavailable")
	c := NewConsumer(
		&testMQ{publishErr: dlqErr},
		func(context.Context, *mqv1.MQEvent) error { return nil },
		config.ConsumerConfig{
			Topic: "t", QueueGroup: "g", WorkerCount: 1, MaxRetry: 1,
			DLQTopic: "t.dlq", ProgressInterval: time.Millisecond,
		},
		clog.Discard(),
	)

	msg := &testMessage{data: []byte("bad-data")}
	err := c.handleMessage(context.Background(), msg)
	require.ErrorIs(t, err, dlqErr)
	require.False(t, msg.acked)
	require.True(t, msg.nacked)
	require.Positive(t, msg.progress)
}

func TestConsumer_handleMessage_SuccessAck(t *testing.T) {
	callCount := 0
	c := NewConsumer(
		&testMQ{},
		func(ctx context.Context, event *mqv1.MQEvent) error {
			callCount++
			require.Equal(t, int64(101), event.GetEvent().GetEventId())
			return nil
		},
		config.ConsumerConfig{
			Topic:         "t",
			QueueGroup:    "g",
			WorkerCount:   1,
			MaxRetry:      2,
			RetryInterval: 0,
		},
		clog.Discard(),
	)

	payload, err := proto.Marshal(&mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:   101,
			SessionId: "s_1",
		},
	})
	require.NoError(t, err)

	msg := &testMessage{data: payload}
	err = c.handleMessage(context.Background(), msg)
	require.NoError(t, err)
	require.Equal(t, 1, callCount)
	require.True(t, msg.acked)
	require.False(t, msg.nacked)
}

func TestConsumer_handleMessage_HeartbeatRepeatsAndStopsBeforeAck(t *testing.T) {
	c := NewConsumer(
		&testMQ{},
		func(context.Context, *mqv1.MQEvent) error {
			time.Sleep(5 * time.Millisecond)
			return nil
		},
		config.ConsumerConfig{
			Topic: "t", QueueGroup: "g", WorkerCount: 1, MaxRetry: 1,
			ProgressInterval: time.Millisecond,
		},
		clog.Discard(),
	)
	payload, err := proto.Marshal(&mqv1.MQEvent{Event: &commonv1.ChatEvent{EventId: 104}})
	require.NoError(t, err)
	msg := &testMessage{data: payload}

	require.NoError(t, c.handleMessage(context.Background(), msg))
	require.Greater(t, msg.progress, 1)
	progressAfterAck := msg.progress
	time.Sleep(3 * time.Millisecond)
	require.Equal(t, progressAfterAck, msg.progress)
}

func TestConsumer_handleMessage_HandlerFailNak(t *testing.T) {
	c := NewConsumer(
		&testMQ{},
		func(ctx context.Context, event *mqv1.MQEvent) error {
			return errors.New("temporary fail")
		},
		config.ConsumerConfig{
			Topic:         "t",
			QueueGroup:    "g",
			WorkerCount:   1,
			MaxRetry:      2,
			RetryInterval: 0,
		},
		clog.Discard(),
	)

	payload, err := proto.Marshal(&mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:   102,
			SessionId: "s_1",
		},
	})
	require.NoError(t, err)

	msg := &testMessage{data: payload}
	err = c.handleMessage(context.Background(), msg)
	require.Error(t, err)
	require.False(t, msg.acked)
	require.True(t, msg.nacked)
}

func TestConsumer_handleMessage_AckFailedReturnErr(t *testing.T) {
	c := NewConsumer(
		&testMQ{},
		func(ctx context.Context, event *mqv1.MQEvent) error { return nil },
		config.ConsumerConfig{
			Topic:         "t",
			QueueGroup:    "g",
			WorkerCount:   1,
			MaxRetry:      1,
			RetryInterval: 0,
		},
		clog.Discard(),
	)

	payload, err := proto.Marshal(&mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:   103,
			SessionId: "s_1",
		},
	})
	require.NoError(t, err)

	msg := &testMessage{data: payload, ackErr: errors.New("ack failed")}
	err = c.handleMessage(context.Background(), msg)
	require.Error(t, err)
	require.True(t, msg.acked)
}

func TestConsumer_StartAndStop(t *testing.T) {
	sub := &testSubscription{done: make(chan struct{})}
	testMQ := &testMQ{
		subscribeFn: func(ctx context.Context, topic string, handler mq.Handler, opts ...mq.SubscribeOption) (mq.Subscription, error) {
			require.Equal(t, "resonance.chat.event.v1", topic)
			require.NotNil(t, handler)
			return sub, nil
		},
	}

	c := NewConsumer(
		testMQ,
		func(ctx context.Context, event *mqv1.MQEvent) error { return nil },
		config.ConsumerConfig{
			Topic:         "resonance.chat.event.v1",
			QueueGroup:    "g",
			WorkerCount:   1,
			MaxRetry:      1,
			RetryInterval: 0,
		},
		clog.Discard(),
	)

	require.NoError(t, c.Start())
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, c.Stop())
	require.True(t, sub.unsubscribed)
}

func TestConsumer_StopCancelsContextAwareHandler(t *testing.T) {
	started := make(chan struct{})
	var transportHandler mq.Handler
	sub := &testSubscription{done: make(chan struct{})}
	testMQ := &testMQ{subscribeFn: func(_ context.Context, _ string, handler mq.Handler, _ ...mq.SubscribeOption) (mq.Subscription, error) {
		transportHandler = handler
		return sub, nil
	}}
	c := NewConsumer(testMQ, func(ctx context.Context, _ *mqv1.MQEvent) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, config.ConsumerConfig{
		Topic: "t", QueueGroup: "g", WorkerCount: 1, MaxRetry: 1, ProgressInterval: time.Millisecond,
	}, clog.Discard())
	require.NoError(t, c.Start())
	payload, err := proto.Marshal(&mqv1.MQEvent{Event: &commonv1.ChatEvent{EventId: 105}})
	require.NoError(t, err)
	go func() { _ = transportHandler(&testMessage{data: payload}) }()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop waited for a handler before canceling its context")
	}
}
