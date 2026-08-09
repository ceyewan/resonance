// Package runtime 定义 Pilot 与具体 Agent Harness 之间的稳定边界。
//
// 业务层只能依赖本包，不能依赖 Pi 的 RPC 事件、命令或 Session 文件格式。
package runtime

import (
	"context"
)

// AgentRuntime 是可替换的 Agent Harness 运行时。
type AgentRuntime interface {
	Run(ctx context.Context, req RunRequest) (EventStream, error)
	Abort(ctx context.Context, runID string) error
	Probe(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

// EventStream 表示一次正在执行或已经完成的 Run。
// Events 必须被持续消费；Wait 可以被重复调用并返回同一结果。
type EventStream interface {
	Events() <-chan RuntimeEvent
	Wait() (RunResult, error)
}

// RunRequest 是启动一次 Runtime Run 所需的不可变快照。
type RunRequest struct {
	RunID          string
	ConversationID string
	Prompt         string
	Session        SessionSnapshot
	Profile        ProfileSnapshot
	Actor          ActorPrincipal
	Capability     Secret
	Limits         ExecutionLimits
}

// ExecutionLimits are the authoritative per-attempt ceilings reserved before
// the provider can be called. Runtime and Bridge must fail closed when they
// cannot enforce them; post-hoc OVERDRAWN accounting is not a substitute.
type ExecutionLimits struct {
	MaxTotalTokens   int64
	MaxCostMicros    int64
	MaxProviderCalls int
}

const maxJavaScriptSafeInteger int64 = 9_007_199_254_740_991

// Valid guarantees the limits can be represented exactly by the trusted
// TypeScript Bridge. Accepting a larger int64 would silently round a Provider
// ceiling before the first request and invalidate the budget reservation.
func (limits ExecutionLimits) Valid() bool {
	return limits.MaxTotalTokens >= 1 && limits.MaxTotalTokens <= maxJavaScriptSafeInteger &&
		limits.MaxCostMicros >= 1 && limits.MaxCostMicros <= maxJavaScriptSafeInteger &&
		limits.MaxProviderCalls >= 1 && limits.MaxProviderCalls <= 128
}

// SessionSnapshot 描述本次 Run 的 staging Session。
// FilePath 和 Directory 必须由 Pilot Session Manager 准备，Runtime 不负责提交快照。
type SessionSnapshot struct {
	SessionID string
	FilePath  string
	Directory string
}

// ProfileSnapshot 固化本次 Run 使用的模型和 Prompt 版本。
type ProfileSnapshot struct {
	ID           string
	Version      int64
	Provider     string
	Model        string
	SystemPrompt string
}

// ActorPrincipal 是发起 Run 的最终用户身份快照。
// Tool Broker 仍需在每次执行时重新读取当前权限。
type ActorPrincipal struct {
	TenantID string
	ActorID  string
	Username string
	Roles    []string
	Scopes   []string
}

// Secret 避免 Capability 被 fmt.Stringer 或普通日志直接输出。
// Reveal 只允许在受控的 Runtime 进程组装边界调用。
type Secret struct {
	value string
}

// NewSecret 创建一个默认脱敏的 Secret。
func NewSecret(value string) Secret {
	return Secret{value: value}
}

// IsZero 返回 Secret 是否为空。
func (s Secret) IsZero() bool {
	return s.value == ""
}

// Reveal 返回 Secret 原文。调用方不得记录返回值。
func (s Secret) Reveal() string {
	return s.value
}

// String 始终返回脱敏值。
func (Secret) String() string {
	return "[REDACTED]"
}

// EventKind 是 Runtime-neutral 事件类型。
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

// RuntimeEvent 是 Pilot 上层可以观察到的受控事件。
type RuntimeEvent struct {
	Kind     EventKind
	Text     string
	Tool     *ToolEvent
	Usage    *Usage
	Error    *RuntimeError
	Sequence uint64
}

// ToolEvent 只暴露可安全关联的 Tool 进度。
// 原始参数和结果只能保留在 Tool Broker 私有边界，不能进入通用事件或日志。
type ToolEvent struct {
	CallID  string
	Name    string
	IsError bool
}

// UsageState 描述一次 Run 的用量是否可以安全入账。
// UNKNOWN 不能按零用量处理；NOT_STARTED 只用于已确认 prompt 未发送的路径。
type UsageState string

const (
	UsageStateExact      UsageState = "EXACT"
	UsageStateUnknown    UsageState = "UNKNOWN"
	UsageStateNotStarted UsageState = "NOT_STARTED"
)

// Usage 是一次 Run 的模型用量。
type Usage struct {
	State            UsageState
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	TotalTokens      int64
	// CostMicros 是由 Runtime 报告成本换算并向上取整到 micro-USD 的值，
	// 适合避免 float 累加误差，但不替代 Provider 最终账单。
	CostMicros int64
	// Cost 保留给现有调用方；新计费代码应优先使用 CostMicros。
	Cost float64
}

// RunError 让 Run 在无法返回 EventStream 时仍明确携带用量状态。
// Cause 必须已经过 Runtime 边界脱敏；Usage 不得为 nil。
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

// NewRunError 创建携带明确用量状态的启动错误。
func NewRunError(cause error, usage *Usage) error {
	if cause == nil {
		return nil
	}
	if usage == nil {
		usage = &Usage{State: UsageStateUnknown}
	}
	return &RunError{Cause: cause, Usage: usage}
}

// RuntimeError 是可以持久化分类、但不暴露底层敏感诊断的错误。
type RuntimeError struct {
	Code      string
	Message   string
	Retryable bool
}

// RunResult 在成功时包含 settled 结果；失败时仍必须携带明确的 Usage.State。
type RunResult struct {
	FinalText   string
	SessionID   string
	SessionFile string
	LeafEntryID string
	Usage       *Usage
}
