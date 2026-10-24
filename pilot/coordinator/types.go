// Package coordinator 负责 durable Agent Run 的领取、执行和 prepare-then-commit。
package coordinator

import (
	"context"
	"errors"
	"time"

	"github.com/ceyewan/genesis/clog"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/session"
	"github.com/ceyewan/resonance/repo"
)

var ErrNoWork = errors.New("no agent run work available")

type RunStore interface {
	GetAgentSessionBinding(ctx context.Context, tenantID, conversationID string) (*model.AgentSessionBinding, error)
	ClaimNextAgentRun(ctx context.Context, claim repo.AgentRunClaim) (*model.AgentRun, error)
	HeartbeatAgentRun(ctx context.Context, lease repo.AgentRunLease) (*model.AgentRun, error)
	AdvanceAgentRun(ctx context.Context, transition repo.AgentRunTransition) (*model.AgentRun, error)
	FailAgentRun(ctx context.Context, failure repo.AgentRunFailure) (*model.AgentRun, error)
	CancelAgentRun(ctx context.Context, cancellation repo.AgentRunCancellation) (*model.AgentRun, error)
	PrepareAgentRun(ctx context.Context, prepared repo.AgentRunPreparedResult) (*model.AgentRun, error)
	BeginAgentRunCommit(ctx context.Context, lease repo.AgentRunLease) (*model.AgentRun, error)
	ClaimPreparedAgentRun(ctx context.Context, claim repo.AgentRunClaim) (*model.AgentRun, error)
	RecordAgentRunFinalMessage(ctx context.Context, result repo.AgentRunFinalMessage) (*model.AgentRun, error)
	CompleteAgentRun(ctx context.Context, completion repo.AgentRunCompletion) (*model.AgentRun, error)
	RecoverExpiredAgentRuns(ctx context.Context, tenantID string, now time.Time) (repo.AgentRunRecoveryResult, error)
}

// HistoryResolver rebuilds a fresh prompt from Logic's authoritative chat
// history when an opaque Pi Session cannot be resumed safely.
type HistoryResolver interface {
	RebuildPrompt(ctx context.Context, run *model.AgentRun) (string, error)
}

type FinalMessageWriter interface {
	CommitFinalMessage(ctx context.Context, request FinalMessageRequest) (FinalMessageAck, error)
}

type EventSink interface {
	PublishRuntimeEvent(ctx context.Context, run *model.AgentRun, event pilotruntime.RuntimeEvent) error
}

type FinalMessageRequest struct {
	TenantID       string
	RunID          string
	ConversationID string
	ClientMsgID    string
	Content        string
}

type FinalMessageAck struct {
	EventID     int64
	SeqID       int64
	TimestampMs int64
}

type Dependencies struct {
	Logger        clog.Logger
	Runs          RunStore
	Runtime       pilotruntime.AgentRuntime
	Sessions      session.Manager
	History       HistoryResolver
	FinalMessages FinalMessageWriter
	Events        EventSink
}

type Config struct {
	// Assistant is the server-owned frozen model/prompt snapshot written into
	// each Run. There is exactly one configuration, so it is data, not a
	// resolver.
	Assistant         pilotruntime.ProfileSnapshot
	ProfileID         string
	ProfileVersion    int64
	WorkerID          string
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	RunTimeout        time.Duration
	RetryBackoff      time.Duration
	MaxOutputTokens   int64
}
