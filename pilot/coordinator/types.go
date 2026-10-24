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
	GetAgentRun(ctx context.Context, tenantID, runID string) (*model.AgentRun, error)
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

// FinalMessageWriter 是 Bot 最终消息的唯一写入口。
//
// CommitFinalMessage 的 Content 必须来自 Run 的 FrozenFinalText，Pilot 侧不存在
// 第二个"再生成一条 Bot 消息"的路径——对 FrozenFinalText 与真实内容的恒等性论证
// 完全依赖这一点。新增写入点前请先读 docs/14 关于提交闸门的说明；
// pilot/contract_test.go 会断言全仓只有一处调用。
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
