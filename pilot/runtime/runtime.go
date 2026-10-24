// Package runtime is the boundary between Pilot and Pi's stdio RPC.
package runtime

import "context"

type AgentRuntime interface {
	Run(context.Context, RunRequest) (EventStream, error)
	Abort(context.Context, string) error
	Probe(context.Context) error
	Shutdown(context.Context) error
}
type EventStream interface {
	Events() <-chan RuntimeEvent
	Wait() (RunResult, error)
}
type RunRequest struct {
	RunID           string
	ConversationID  string
	Prompt          string
	Session         SessionSnapshot
	Profile         ProfileSnapshot
	MaxOutputTokens int64
}
type SessionSnapshot struct{ SessionID, FilePath, Directory string }

// ProfileSnapshot is one server-owned model/prompt configuration, never user selectable.
type ProfileSnapshot struct {
	ID                            string
	Version                       int64
	Provider, Model, SystemPrompt string
}
type ActorPrincipal struct{ TenantID, ActorID, Username string }

type EventKind string

const (
	EventStarted           EventKind = "started"
	EventTextDelta         EventKind = "text_delta"
	EventToolStarted       EventKind = "tool_started"
	EventToolUpdated       EventKind = "tool_updated"
	EventToolEnded         EventKind = "tool_ended"
	EventCompactionStarted EventKind = "compaction_started"
	EventCompactionEnded   EventKind = "compaction_ended"
	EventRetryStarted      EventKind = "retry_started"
	EventRetryEnded        EventKind = "retry_ended"
	EventSettled           EventKind = "settled"
	EventFailed            EventKind = "failed"
)

type RuntimeEvent struct {
	Kind     EventKind
	Text     string
	Tool     *ToolEvent
	Usage    *Usage
	Error    *RuntimeError
	Sequence uint64
}
type ToolEvent struct {
	CallID, Name string
	IsError      bool
}
type UsageState string

const (
	UsageStateExact      UsageState = "EXACT"
	UsageStateUnknown    UsageState = "UNKNOWN"
	UsageStateNotStarted UsageState = "NOT_STARTED"
)

type Usage struct {
	State                                                                     UsageState
	InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens, TotalTokens int64
	CostMicros                                                                int64
	Cost                                                                      float64
}
type RunError struct {
	Cause error
	Usage *Usage
}

func (e *RunError) Error() string {
	if e == nil || e.Cause == nil {
		return "agent runtime failed"
	}
	return e.Cause.Error()
}
func (e *RunError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
func NewRunError(cause error, usage *Usage) error {
	if cause == nil {
		return nil
	}
	if usage == nil {
		usage = &Usage{State: UsageStateUnknown}
	}
	return &RunError{Cause: cause, Usage: usage}
}

type RuntimeError struct {
	Code, Message string
	Retryable     bool
}
type RunResult struct {
	FinalText, SessionID, SessionFile, LeafEntryID string
	Usage                                          *Usage
}
