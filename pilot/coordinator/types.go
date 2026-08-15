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

var (
	ErrNoWork              = errors.New("no agent run work available")
	ErrProfileMismatch     = errors.New("resolved agent profile does not match queued snapshot")
	ErrPrincipalMismatch   = errors.New("resolved actor principal does not match queued identity")
	ErrProfileAccessDenied = errors.New("actor is not currently authorized for agent profile")
)

type RunStore interface {
	GetAgentSessionBinding(ctx context.Context, tenantID, conversationID string) (*model.AgentSessionBinding, error)
	MarkAgentSessionBindingDirty(ctx context.Context, tenantID, conversationID string, expectedGeneration int64, now time.Time) (*model.AgentSessionBinding, error)
	ClaimNextAgentRun(ctx context.Context, claim repo.AgentRunClaim) (*model.AgentRun, error)
	HeartbeatAgentRun(ctx context.Context, lease repo.AgentRunLease) (*model.AgentRun, error)
	AdvanceAgentRun(ctx context.Context, transition repo.AgentRunTransition) (*model.AgentRun, error)
	FailAgentRun(ctx context.Context, failure repo.AgentRunFailure) (*model.AgentRun, error)
	CancelAgentRun(ctx context.Context, cancellation repo.AgentRunCancellation) (*model.AgentRun, error)
	CancelPendingAgentRuns(ctx context.Context, cancellation repo.AgentPendingRunCancellation) (int64, error)
	PrepareAgentRun(ctx context.Context, prepared repo.AgentRunPreparedResult) (*model.AgentRun, error)
	BeginAgentRunCommit(ctx context.Context, lease repo.AgentRunLease) (*model.AgentRun, error)
	ClaimPreparedAgentRun(ctx context.Context, claim repo.AgentRunClaim) (*model.AgentRun, error)
	RecordAgentRunFinalMessage(ctx context.Context, result repo.AgentRunFinalMessage) (*model.AgentRun, error)
	CompleteAgentRun(ctx context.Context, completion repo.AgentRunCompletion) (*model.AgentRun, error)
	RecoverExpiredAgentRuns(ctx context.Context, tenantID string, now time.Time) (repo.AgentRunRecoveryResult, error)
	ReserveAgentBudget(ctx context.Context, reservation repo.AgentBudgetReservation) (*model.AgentBudgetAttempt, error)
	SettleAgentBudget(ctx context.Context, settlement repo.AgentBudgetSettlement) (*model.AgentBudgetAttempt, error)
	RecoverExpiredAgentBudgetAttempts(ctx context.Context, tenantID string, now time.Time) (int64, error)
}

type ProfileResolver interface {
	ResolveProfile(ctx context.Context, tenantID, profileID string, version int64) (pilotruntime.ProfileSnapshot, error)
}

type PrincipalResolver interface {
	ResolvePrincipal(ctx context.Context, tenantID, actorID string) (pilotruntime.ActorPrincipal, error)
}

type CapabilityIssuer interface {
	IssueCapability(ctx context.Context, run *model.AgentRun, principal pilotruntime.ActorPrincipal) (pilotruntime.Secret, error)
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
	Profiles      ProfileResolver
	Principals    PrincipalResolver
	Capabilities  CapabilityIssuer
	History       HistoryResolver
	FinalMessages FinalMessageWriter
	Events        EventSink
}

type Config struct {
	TenantID          string
	ProfileID         string
	ProfileVersion    int64
	WorkerID          string
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	RunTimeout        time.Duration
	RetryBackoff      time.Duration
	MaxProviderCalls  int
}
