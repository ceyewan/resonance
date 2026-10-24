// Package ingress 把符合条件的 ChatEvent 持久化成 Agent Run；它不执行模型调用。
package ingress

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

var (
	ErrAdmissionMismatch = errors.New("agent ingress admission snapshot does not match chat event")
	ErrAdmissionDenied   = errors.New("chat event does not belong to a pilot private conversation")
)

type RunEnqueuer interface {
	EnqueueAgentRun(ctx context.Context, run *model.AgentRun) (*repo.AgentRunEnqueueResult, error)
}

// AdmissionController 判断事件能否触发 Agent。MQ 中的 from_username 只能在
// 通行会话事实被核对后使用，不能自行成为 Principal。
type AdmissionController interface {
	Admit(ctx context.Context, event *commonv1.ChatEvent) (Conversation, error)
}

type HandlerConfig struct {
	BotUsername    string
	MaxPromptBytes int
	Assistant      Assistant
}

type Handler struct {
	config    HandlerConfig
	runs      RunEnqueuer
	admission AdmissionController
	now       func() time.Time
	runID     func() (string, error)
}

type HandlerOption func(*Handler)

func WithClock(now func() time.Time) HandlerOption {
	return func(handler *Handler) {
		if now != nil {
			handler.now = now
		}
	}
}

func WithRunIDSource(source func() (string, error)) HandlerOption {
	return func(handler *Handler) {
		if source != nil {
			handler.runID = source
		}
	}
}

func NewHandler(config HandlerConfig, runs RunEnqueuer, admission AdmissionController, options ...HandlerOption) (*Handler, error) {
	if config.BotUsername == "" || runs == nil || admission == nil {
		return nil, fmt.Errorf("agent ingress configuration and dependencies are incomplete")
	}
	if config.MaxPromptBytes == 0 {
		config.MaxPromptBytes = 32 << 10
	}
	if config.MaxPromptBytes < 1 {
		return nil, fmt.Errorf("max prompt bytes must be positive")
	}
	if err := validateAssistant(config.Assistant); err != nil {
		return nil, err
	}
	handler := &Handler{config: config, runs: runs, admission: admission, now: time.Now, runID: randomRunID}
	for _, option := range options {
		option(handler)
	}
	return handler, nil
}

type HandleResult struct {
	Ignored bool
	Run     *model.AgentRun
	Created bool
}

func (h *Handler) Handle(ctx context.Context, envelope *mqv1.MQEvent) (HandleResult, error) {
	if envelope == nil || envelope.Event == nil {
		return HandleResult{}, permanent(fmt.Errorf("chat event is missing"))
	}
	event := envelope.Event
	// Bot 不是这条事件的目标，说明它不是 Pilot 私聊消息。这一步不查库，
	// 让全系统的普通聊天完全不经过 Pilot 的数据库。
	if !slices.Contains(envelope.TargetUsernames, h.config.BotUsername) {
		return HandleResult{Ignored: true}, nil
	}
	message := event.GetMessage()
	if message == nil || message.Type != commonv1.MessageType_MESSAGE_TYPE_TEXT ||
		event.FromUsername == h.config.BotUsername {
		return HandleResult{Ignored: true}, nil
	}
	if event.EventId <= 0 || event.SeqId <= 0 || event.TimestampMs <= 0 || event.SessionId == "" || event.FromUsername == "" {
		return HandleResult{}, permanent(fmt.Errorf("chat event identity is incomplete"))
	}
	if message.ClientMsgId != "" && strings.HasPrefix(message.ClientMsgId, "agent:") {
		return HandleResult{Ignored: true}, nil
	}
	if message.Content == "" || len(message.Content) > h.config.MaxPromptBytes || !utf8.ValidString(message.Content) {
		return HandleResult{}, permanent(fmt.Errorf("prompt is empty, oversized or invalid UTF-8"))
	}

	conversation, err := h.admission.Admit(ctx, event)
	if err != nil {
		if errors.Is(err, ErrAdmissionDenied) || errors.Is(err, ErrAdmissionMismatch) {
			return HandleResult{}, permanent(err)
		}
		return HandleResult{}, err
	}
	if !conversation.Found {
		return HandleResult{Ignored: true}, nil
	}

	if conversation.ConversationID != event.SessionId {
		return HandleResult{}, permanent(ErrAdmissionMismatch)
	}
	runID, err := h.runID()
	if err != nil {
		return HandleResult{}, fmt.Errorf("generate agent run id: %w", err)
	}
	if runID == "" || len(runID) > 52 {
		return HandleResult{}, fmt.Errorf("generated agent run id is invalid")
	}
	sourceHash, err := hashSourceEvent(conversation.TenantID, event)
	if err != nil {
		return HandleResult{}, permanent(err)
	}
	traceContext, err := json.Marshal(envelope.TraceHeaders)
	if err != nil || len(traceContext) > 16*1024 {
		return HandleResult{}, permanent(fmt.Errorf("trace context is invalid or oversized"))
	}
	now := h.now().UTC()
	assistant := h.config.Assistant
	run := &model.AgentRun{
		RunID: runID, TenantID: conversation.TenantID, ConversationID: conversation.ConversationID,
		SourceEventID: event.EventId, SourceSeqID: event.SeqId, SourceTimestampMs: event.TimestampMs,
		SourceHash: sourceHash, Prompt: message.Content, TraceContext: traceContext,
		ActorID: conversation.ActorUsername, ActorUsername: conversation.ActorUsername,
		ProfileID: assistant.ProfileID, ProfileVersion: assistant.ProfileVersion,
		RuntimeVersion: assistant.RuntimeVersion, BridgeVersion: assistant.BridgeVersion,
		ModelID: assistant.ModelID,
		Status:  model.AgentRunStatusQueued, MaxAttempts: assistant.MaxAttempts,
		QueuedAt: now, AvailableAt: now,
	}
	enqueued, err := h.runs.EnqueueAgentRun(ctx, run)
	if err != nil {
		if errors.Is(err, repo.ErrAgentRunSourceConflict) {
			return HandleResult{}, permanent(err)
		}
		return HandleResult{}, err
	}
	return HandleResult{Run: enqueued.Run, Created: enqueued.Created}, nil
}

func hashSourceEvent(tenantID string, event *commonv1.ChatEvent) (string, error) {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("marshal source chat event: %w", err)
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("resonance-agent-run-source-v1\x00"))
	_, _ = hasher.Write([]byte(tenantID))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(payload)
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func randomRunID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

func permanent(err error) error { return permanentError{error: err} }

func IsPermanent(err error) bool {
	var target permanentError
	return errors.As(err, &target)
}
