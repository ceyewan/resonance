package remote

import (
	"context"
	"errors"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

const protocolVersion = 1

const (
	frameAccepted = "accepted"
	frameEvent    = "event"
	frameResult   = "result"
	frameError    = "error"
)

type runRequestWire struct {
	ProtocolVersion int                          `json:"protocol_version"`
	RunID           string                       `json:"run_id"`
	ConversationID  string                       `json:"conversation_id"`
	Prompt          string                       `json:"prompt"`
	Session         pilotruntime.SessionSnapshot `json:"session"`
	Profile         pilotruntime.ProfileSnapshot `json:"profile"`
	Actor           pilotruntime.ActorPrincipal  `json:"actor"`
	Capability      string                       `json:"capability"`
	Limits          pilotruntime.ExecutionLimits `json:"limits"`
}

type runFrame struct {
	ProtocolVersion int                        `json:"protocol_version"`
	Type            string                     `json:"type"`
	Event           *pilotruntime.RuntimeEvent `json:"event,omitempty"`
	Result          *pilotruntime.RunResult    `json:"result,omitempty"`
	Error           *runtimeErrorWire          `json:"error,omitempty"`
}

type runtimeErrorWire struct {
	Kind    string              `json:"kind"`
	Message string              `json:"message"`
	Usage   *pilotruntime.Usage `json:"usage"`
}

type runControlWire struct {
	RunID string `json:"run_id"`
}

type statusWire struct {
	OK bool `json:"ok"`
}

type remoteError struct {
	kind    string
	message string
}

func (e *remoteError) Error() string {
	if e == nil || e.message == "" {
		return "remote agent runtime failed"
	}
	return e.message
}

func (e *remoteError) Unwrap() error {
	if e == nil {
		return nil
	}
	switch e.kind {
	case "context_canceled":
		return context.Canceled
	case "context_deadline":
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func errorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline"
	default:
		return "runtime"
	}
}
