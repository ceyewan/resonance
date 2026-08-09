// Package stream publishes best-effort Agent deltas on an ephemeral channel.
// The durable final assistant message is committed through Logic and is never
// derived from this package's output.
package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ceyewan/genesis/mq"
	"google.golang.org/protobuf/proto"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

var (
	ErrCapacity     = errors.New("agent stream capacity exceeded")
	ErrDeltaDropped = errors.New("agent stream delta dropped by bounded buffer")
)

type Publisher interface {
	Publish(ctx context.Context, topic string, data []byte, options ...mq.PublishOption) error
}

type Config struct {
	Topic           string
	BotUsername     string
	FlushInterval   time.Duration
	PublishTimeout  time.Duration
	MaxStreams      int
	MaxPendingBytes int
	MaxChunkBytes   int
}

type Sink struct {
	config    Config
	publisher Publisher

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}

	mu       sync.Mutex
	states   map[string]*runState
	closing  bool
	closeErr error
	closeOne sync.Once
}

type runState struct {
	tenantID       string
	runID          string
	streamID       string
	sessionID      string
	actorUsername  string
	sourceEventID  int64
	finalClientID  string
	traceHeaders   map[string]string
	beginPublished bool
	buffer         string
	endPending     bool
	endReason      mqv1.AgentStreamFinishReason
	nextSequence   uint64
	publishing     bool
}

func NewSink(config Config, publisher Publisher) (*Sink, error) {
	if config.Topic == "" || config.BotUsername == "" || publisher == nil || config.FlushInterval <= 0 ||
		config.PublishTimeout <= 0 || config.MaxStreams < 1 || config.MaxPendingBytes < 1 ||
		config.MaxChunkBytes < 1 || config.MaxChunkBytes > config.MaxPendingBytes {
		return nil, fmt.Errorf("agent stream sink configuration is invalid")
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink := &Sink{
		config: config, publisher: publisher, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), done: make(chan struct{}), states: make(map[string]*runState),
	}
	go sink.loop()
	return sink, nil
}

// PublishRuntimeEvent is deliberately non-blocking. Text deltas may be
// dropped under pressure; the final ChatEvent remains authoritative.
func (s *Sink) PublishRuntimeEvent(ctx context.Context, run *model.AgentRun, event pilotruntime.RuntimeEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if run == nil || run.TenantID == "" || run.RunID == "" || run.ConversationID == "" ||
		run.ActorUsername == "" || run.SourceEventID <= 0 {
		return fmt.Errorf("agent stream run identity is incomplete")
	}
	if event.Kind != pilotruntime.EventStarted && event.Kind != pilotruntime.EventTextDelta &&
		event.Kind != pilotruntime.EventSettled && event.Kind != pilotruntime.EventFailed {
		return nil
	}
	// Privileged Session text is withheld until the durable final commit. The
	// Coordinator rechecks current iam-admin authorization immediately before
	// that commit, so a mid-Run downgrade cannot leak old administrator context
	// through best-effort deltas before the revocation is observed.
	if run.ProfileID == model.AgentProfileIAMAdmin && event.Kind == pilotruntime.EventTextDelta {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return context.Canceled
	}
	state, ok := s.states[run.RunID]
	if !ok {
		if len(s.states) >= s.config.MaxStreams {
			return ErrCapacity
		}
		traceHeaders := make(map[string]string)
		if len(run.TraceContext) > 0 {
			if err := json.Unmarshal(run.TraceContext, &traceHeaders); err != nil {
				return fmt.Errorf("decode agent stream trace context: %w", err)
			}
		}
		state = &runState{
			tenantID: run.TenantID, runID: run.RunID, streamID: run.RunID,
			sessionID: run.ConversationID, actorUsername: run.ActorUsername,
			sourceEventID: run.SourceEventID, finalClientID: "agent:" + run.RunID + ":final",
			traceHeaders: traceHeaders,
		}
		s.states[run.RunID] = state
	} else if state.tenantID != run.TenantID || state.sessionID != run.ConversationID ||
		state.actorUsername != run.ActorUsername || state.sourceEventID != run.SourceEventID {
		return fmt.Errorf("agent stream run snapshot changed")
	}

	switch event.Kind {
	case pilotruntime.EventStarted:
		s.signal()
	case pilotruntime.EventTextDelta:
		if event.Text == "" {
			return nil
		}
		if !utf8.ValidString(event.Text) {
			return fmt.Errorf("agent stream delta is invalid UTF-8")
		}
		if len(state.buffer)+len(event.Text) > s.config.MaxPendingBytes {
			return ErrDeltaDropped
		}
		state.buffer += event.Text
	case pilotruntime.EventSettled:
		state.endPending = true
		state.endReason = mqv1.AgentStreamFinishReason_AGENT_STREAM_FINISH_REASON_STOP
		s.signal()
	case pilotruntime.EventFailed:
		state.endPending = true
		state.endReason = mqv1.AgentStreamFinishReason_AGENT_STREAM_FINISH_REASON_ERROR
		s.signal()
	}
	return nil
}

func (s *Sink) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Sink) loop() {
	defer close(s.done)
	ticker := time.NewTicker(s.config.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.flushAll()
		case <-s.wake:
			s.flushAll()
		}
	}
}

func (s *Sink) flushAll() {
	s.mu.Lock()
	runIDs := make([]string, 0, len(s.states))
	for runID := range s.states {
		runIDs = append(runIDs, runID)
	}
	s.mu.Unlock()
	for _, runID := range runIDs {
		s.flushRun(runID)
	}
}

func (s *Sink) flushRun(runID string) {
	for {
		event, action, chunk := s.nextEvent(runID)
		if event == nil {
			return
		}
		payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
		if err == nil {
			publishContext, cancel := context.WithTimeout(s.ctx, s.config.PublishTimeout)
			err = s.publisher.Publish(publishContext, s.config.Topic, payload, mq.WithHeaders(mq.Headers(event.TraceHeaders)))
			cancel()
		}
		if !s.finishPublish(runID, action, chunk, err == nil) || err != nil {
			return
		}
	}
}

type publishAction uint8

const (
	publishBegin publishAction = iota + 1
	publishChunk
	publishEnd
)

func (s *Sink) nextEvent(runID string) (*mqv1.AgentStreamEvent, publishAction, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.states[runID]
	if state == nil || state.publishing {
		return nil, 0, ""
	}
	event := &mqv1.AgentStreamEvent{
		TenantId: state.tenantID, RunId: state.runID, StreamId: state.streamID,
		SessionId: state.sessionID, FromUsername: s.config.BotUsername,
		TargetUsernames: []string{state.actorUsername}, SourceEventId: state.sourceEventID,
		FinalClientMsgId: state.finalClientID, TraceHeaders: cloneHeaders(state.traceHeaders),
	}
	var action publishAction
	var chunk string
	switch {
	case !state.beginPublished:
		action = publishBegin
		event.Sequence = 0
		event.Payload = &mqv1.AgentStreamEvent_Begin{Begin: &mqv1.AgentStreamBegin{}}
	case state.buffer != "":
		action = publishChunk
		chunk = utf8Prefix(state.buffer, s.config.MaxChunkBytes)
		event.Sequence = state.nextSequence
		event.Payload = &mqv1.AgentStreamEvent_Chunk{Chunk: &mqv1.AgentStreamChunk{Delta: chunk}}
	case state.endPending:
		action = publishEnd
		event.Sequence = state.nextSequence
		event.Payload = &mqv1.AgentStreamEvent_End{End: &mqv1.AgentStreamEnd{Reason: state.endReason}}
	default:
		return nil, 0, ""
	}
	state.publishing = true
	return event, action, chunk
}

func (s *Sink) finishPublish(runID string, action publishAction, chunk string, succeeded bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.states[runID]
	if state == nil {
		return false
	}
	state.publishing = false
	if !succeeded {
		return false
	}
	switch action {
	case publishBegin:
		state.beginPublished = true
		state.nextSequence = 1
	case publishChunk:
		if !strings.HasPrefix(state.buffer, chunk) {
			return false
		}
		state.buffer = strings.TrimPrefix(state.buffer, chunk)
		state.nextSequence++
	case publishEnd:
		delete(s.states, runID)
	}
	return true
}

func (s *Sink) Close(ctx context.Context) error {
	s.closeOne.Do(func() {
		s.mu.Lock()
		s.closing = true
		for _, state := range s.states {
			if !state.endPending {
				state.endPending = true
				state.endReason = mqv1.AgentStreamFinishReason_AGENT_STREAM_FINISH_REASON_ERROR
			}
		}
		s.mu.Unlock()
		s.signal()
	})

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		empty := len(s.states) == 0
		closeErr := s.closeErr
		s.mu.Unlock()
		if empty {
			s.cancel()
			<-s.done
			return closeErr
		}
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.closeErr = errors.Join(s.closeErr, ctx.Err())
			s.mu.Unlock()
			s.cancel()
			<-s.done
			return ctx.Err()
		case <-ticker.C:
			s.signal()
		}
	}
}

func utf8Prefix(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	if end == 0 {
		_, size := utf8.DecodeRuneInString(value)
		return value[:size]
	}
	return value[:end]
}

func cloneHeaders(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
