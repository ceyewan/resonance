package stream

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ceyewan/genesis/mq"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

func TestSink_CoalescesAndPublishesOrderedStream(t *testing.T) {
	publisher := &recordingPublisher{}
	sink := newTestSink(t, publisher, Config{})
	run := testRun()
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventStarted}))
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventTextDelta, Text: "你"}))
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventTextDelta, Text: "好"}))
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventSettled}))

	require.Eventually(t, func() bool { return len(publisher.events()) == 3 }, time.Second, time.Millisecond)
	events := publisher.events()
	require.NotNil(t, events[0].GetBegin())
	require.Equal(t, uint64(0), events[0].GetSequence())
	require.Equal(t, "你好", events[1].GetChunk().GetDelta())
	require.Equal(t, uint64(1), events[1].GetSequence())
	require.Equal(t, mqv1.AgentStreamFinishReason_AGENT_STREAM_FINISH_REASON_STOP, events[2].GetEnd().GetReason())
	require.Equal(t, uint64(2), events[2].GetSequence())
	for _, event := range events {
		require.Equal(t, "tenant-1", event.GetTenantId())
		require.Equal(t, "run-1", event.GetRunId())
		require.Equal(t, "run-1", event.GetStreamId())
		require.Equal(t, "session-1", event.GetSessionId())
		require.Equal(t, "resonance-agent", event.GetFromUsername())
		require.Equal(t, []string{"alice"}, event.GetTargetUsernames())
		require.Equal(t, int64(41), event.GetSourceEventId())
		require.Equal(t, "agent:run-1:final", event.GetFinalClientMsgId())
		require.Equal(t, "00-test", event.GetTraceHeaders()["traceparent"])
	}
}

func TestSink_PublishFailureRetriesSameSequence(t *testing.T) {
	publisher := &recordingPublisher{failures: 1}
	sink := newTestSink(t, publisher, Config{})
	run := testRun()
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventStarted}))
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventSettled}))
	require.Eventually(t, func() bool { return len(publisher.events()) == 2 }, time.Second, time.Millisecond)
	require.Equal(t, uint64(0), publisher.events()[0].GetSequence())
	require.Equal(t, uint64(1), publisher.events()[1].GetSequence())
}

func TestSink_DeltaBackpressureDoesNotLoseEnd(t *testing.T) {
	publisher := &recordingPublisher{}
	sink := newTestSink(t, publisher, Config{MaxPendingBytes: 4, MaxChunkBytes: 4})
	run := testRun()
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventStarted}))
	err := sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventTextDelta, Text: "oversized"})
	require.ErrorIs(t, err, ErrDeltaDropped)
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventSettled}))
	require.Eventually(t, func() bool { return len(publisher.events()) == 2 }, time.Second, time.Millisecond)
	require.NotNil(t, publisher.events()[0].GetBegin())
	require.NotNil(t, publisher.events()[1].GetEnd())
}

func TestSink_IgnoresToolEvents(t *testing.T) {
	publisher := &recordingPublisher{}
	sink := newTestSink(t, publisher, Config{})
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), testRun(), pilotruntime.RuntimeEvent{
		Kind: pilotruntime.EventToolStarted, Tool: &pilotruntime.ToolEvent{CallID: "secret", Name: "tool"},
	}))
	time.Sleep(20 * time.Millisecond)
	require.Empty(t, publisher.events())
}

func TestSink_PrivilegedProfileWithholdsTextUntilAuthorizedFinalCommit(t *testing.T) {
	publisher := &recordingPublisher{}
	sink := newTestSink(t, publisher, Config{})
	run := testRun()
	run.ProfileID = model.AgentProfileIAMAdmin
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventStarted}))
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventTextDelta, Text: "privileged session text"}))
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), run, pilotruntime.RuntimeEvent{Kind: pilotruntime.EventSettled}))

	require.Eventually(t, func() bool { return len(publisher.events()) == 2 }, time.Second, time.Millisecond)
	events := publisher.events()
	require.NotNil(t, events[0].GetBegin())
	require.NotNil(t, events[1].GetEnd())
	for _, event := range events {
		require.Nil(t, event.GetChunk())
	}
}

func TestSink_CloseForcesErrorEnd(t *testing.T) {
	publisher := &recordingPublisher{}
	sink := newTestSink(t, publisher, Config{})
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), testRun(), pilotruntime.RuntimeEvent{Kind: pilotruntime.EventStarted}))
	closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, sink.Close(closeContext))
	events := publisher.events()
	require.Len(t, events, 2)
	require.Equal(t, mqv1.AgentStreamFinishReason_AGENT_STREAM_FINISH_REASON_ERROR, events[1].GetEnd().GetReason())
}

func TestSink_CloseIsConcurrentAndIdempotent(t *testing.T) {
	publisher := &recordingPublisher{}
	sink := newTestSink(t, publisher, Config{})
	require.NoError(t, sink.PublishRuntimeEvent(context.Background(), testRun(), pilotruntime.RuntimeEvent{Kind: pilotruntime.EventStarted}))

	var wait sync.WaitGroup
	errorsFound := make(chan error, 8)
	for range 8 {
		wait.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			errorsFound <- sink.Close(ctx)
		})
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}
}

func newTestSink(t *testing.T, publisher *recordingPublisher, overrides Config) *Sink {
	t.Helper()
	config := Config{
		Topic: "resonance.agent.stream.v1", BotUsername: "resonance-agent",
		FlushInterval: 5 * time.Millisecond, PublishTimeout: time.Second,
		MaxStreams: 8, MaxPendingBytes: 1024, MaxChunkBytes: 1024,
	}
	if overrides.MaxPendingBytes != 0 {
		config.MaxPendingBytes = overrides.MaxPendingBytes
	}
	if overrides.MaxChunkBytes != 0 {
		config.MaxChunkBytes = overrides.MaxChunkBytes
	}
	sink, err := NewSink(config, publisher)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := sink.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("close sink: %v", err)
		}
	})
	return sink
}

func testRun() *model.AgentRun {
	return &model.AgentRun{
		TenantID: "tenant-1", RunID: "run-1", ConversationID: "session-1",
		ActorUsername: "alice", SourceEventID: 41,
		TraceContext: []byte(`{"traceparent":"00-test"}`),
	}
}

type recordingPublisher struct {
	mu       sync.Mutex
	values   []*mqv1.AgentStreamEvent
	failures int
}

func (p *recordingPublisher) Publish(_ context.Context, topic string, data []byte, _ ...mq.PublishOption) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if topic != "resonance.agent.stream.v1" {
		return errors.New("unexpected topic")
	}
	if p.failures > 0 {
		p.failures--
		return errors.New("transient")
	}
	event := &mqv1.AgentStreamEvent{}
	if err := proto.Unmarshal(data, event); err != nil {
		return err
	}
	p.values = append(p.values, event)
	return nil
}

func (p *recordingPublisher) events() []*mqv1.AgentStreamEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*mqv1.AgentStreamEvent(nil), p.values...)
}
