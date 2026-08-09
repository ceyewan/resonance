package model

import (
	"time"
)

// ============================================================================
// 非持久化模型（Redis）
// ============================================================================

// Router 存储用户与网关实例的映射关系，通常存储在 Redis 中
type Router struct {
	Username  string `json:"username"`
	GatewayID string `json:"gateway_id"`
	RemoteIP  string `json:"remote_ip"`
	Timestamp int64  `json:"timestamp"`
}

// ============================================================================
// 持久化模型（PostgreSQL）
// 以下结构体的 GORM tag 是数据库表结构的唯一真相来源 (Single Source of Truth)。
// 表结构通过 `go run main.go -module init` 调用 GORM AutoMigrate 自动创建/更新。
// ============================================================================

// User 用户表
// 索引：PK(username)
type User struct {
	Username  string `gorm:"primaryKey;column:username;type:varchar(64);not null"`
	Nickname  string `gorm:"column:nickname;type:varchar(64)"`
	Password  string `gorm:"column:password;type:varchar(128);not null"`
	Avatar    string `gorm:"column:avatar;type:varchar(255)"`
	Kind      int    `gorm:"column:kind;type:smallint;not null;default:0"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TenantMembership 是用户名在租户中的权威成员关系。
// User.Username 目前仍全局唯一，但所有 IAM 读写都必须同时携带 tenant_id。
type TenantMembership struct {
	TenantID  string `gorm:"primaryKey;column:tenant_id;type:varchar(64);not null"`
	Username  string `gorm:"primaryKey;column:username;type:varchar(64);not null;index:idx_tenant_membership_username"`
	Status    string `gorm:"column:status;type:varchar(16);not null;index:idx_tenant_membership_status"`
	Version   int64  `gorm:"column:version;type:bigint;not null;default:1"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SystemRoleBinding 是租户级系统角色绑定，不得与 SessionMember.Role 混用。
type SystemRoleBinding struct {
	TenantID  string `gorm:"primaryKey;column:tenant_id;type:varchar(64);not null"`
	Username  string `gorm:"primaryKey;column:username;type:varchar(64);not null;index:idx_system_role_binding_username"`
	Role      string `gorm:"primaryKey;column:role;type:varchar(32);not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Session 会话表（单聊/群聊）
// 索引：PK(session_id)
type Session struct {
	SessionID      string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
	Type           int    `gorm:"column:type;type:smallint;not null"` // 1-单聊, 2-群聊
	Kind           int    `gorm:"column:kind;type:smallint;not null;default:0"`
	TenantID       string `gorm:"column:tenant_id;type:varchar(64);not null;default:'default';index:idx_session_tenant_profile,priority:1;uniqueIndex:uniq_ai_session_profile,priority:1,where:kind = 1"`
	ProfileID      string `gorm:"column:profile_id;type:varchar(64);not null;default:'';index:idx_session_tenant_profile,priority:2;uniqueIndex:uniq_ai_session_profile,priority:3,where:kind = 1"`
	ProfileVersion int64  `gorm:"column:profile_version;type:bigint;not null;default:0;uniqueIndex:uniq_ai_session_profile,priority:4,where:kind = 1"`
	Name           string `gorm:"column:name;type:varchar(128)"`
	AvatarURL      string `gorm:"column:avatar_url;type:varchar(255)"`
	OwnerUsername  string `gorm:"column:owner_username;type:varchar(64);uniqueIndex:uniq_ai_session_profile,priority:2,where:kind = 1"`
	MaxSeqID       int64  `gorm:"column:max_seq_id;type:bigint;default:0"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SessionMember 会话成员表
// 索引：PK(session_id, username) + idx_member_username(username)
//   - PK 复合主键：按会话查成员列表 / 快速判断某用户是否在某会话中
//   - idx_member_username：反查某用户加入的所有会话（联系人列表、会话列表）
type SessionMember struct {
	SessionID   string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
	Username    string `gorm:"primaryKey;column:username;type:varchar(64);not null;index:idx_member_username"`
	Role        int    `gorm:"column:role;type:smallint;default:0"` // 0-成员, 1-管理员
	LastReadSeq int64  `gorm:"column:last_read_seq;type:bigint;default:0"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MessageContent 消息内容表
// 索引：PK(event_id) + idx_sess_seq(session_id, seq_id)
//   - uniq_message_client_id(session_id, sender_username, client_msg_id WHERE client_msg_id <> ”)
//   - idx_sess_seq：按会话拉取历史消息，支持 seq_id 游标分页
//     典型查询: WHERE session_id = ? AND seq_id > ? ORDER BY seq_id LIMIT ?
//   - uniq_message_client_id：发送端幂等键；空 client_msg_id 不参与唯一约束
type MessageContent struct {
	EventID         int64      `gorm:"primaryKey;column:event_id;type:bigint;autoIncrement:false"`
	SessionID       string     `gorm:"column:session_id;type:varchar(64);not null;index:idx_sess_seq,priority:1;uniqueIndex:uniq_message_client_id,priority:1"`
	SenderUsername  string     `gorm:"column:sender_username;type:varchar(64);not null;uniqueIndex:uniq_message_client_id,priority:2"`
	SeqID           int64      `gorm:"column:seq_id;type:bigint;not null;index:idx_sess_seq,priority:2"`
	Content         string     `gorm:"column:content;type:text"`
	MsgType         int        `gorm:"column:msg_type;type:smallint;not null;default:1"`
	ReplyToEventID  int64      `gorm:"column:reply_to_event_id;type:bigint"`
	ClientMsgID     string     `gorm:"column:client_msg_id;type:varchar(64);index:idx_client_msg_id;uniqueIndex:uniq_message_client_id,where:client_msg_id <> '',priority:3"`
	IdempotencyHash string     `gorm:"column:idempotency_hash;type:char(64);not null;default:''"`
	RecalledAt      *time.Time `gorm:"column:recalled_at"`
	EditedAt        *time.Time `gorm:"column:edited_at"`
	EditCount       int        `gorm:"column:edit_count;type:int;default:0"`
	CreatedAt       time.Time
}

// Inbox 用户信箱表（写扩散）
// 索引：PK(id) + uniq_owner_sess_seq(owner_username, session_id, seq_id) + idx_owner_id(owner_username, id)
type Inbox struct {
	ID            int64  `gorm:"primaryKey;column:id;autoIncrement"`
	OwnerUsername string `gorm:"column:owner_username;type:varchar(64);not null;uniqueIndex:uniq_owner_sess_seq,priority:1;index:idx_owner_id,priority:1;index:idx_owner_sess,priority:1"`
	SessionID     string `gorm:"column:session_id;type:varchar(64);not null;uniqueIndex:uniq_owner_sess_seq,priority:2;index:idx_owner_sess,priority:2"`
	SeqID         int64  `gorm:"column:seq_id;type:bigint;not null;uniqueIndex:uniq_owner_sess_seq,priority:3"`
	EventID       int64  `gorm:"column:event_id;type:bigint;not null"`
	EventType     int    `gorm:"column:event_type;type:smallint;not null"`
	Payload       []byte `gorm:"column:payload;type:bytea;not null"`
	CreatedAt     time.Time
}

// MessageOutbox 本地消息表（Outbox Pattern，可靠投递）
// 索引：PK(id) + idx_event_id(event_id) + idx_status_next_retry(status, next_retry_time)
//   - idx_event_id：按事件 ID 查询投递状态（发送确认、幂等检查）
//   - idx_status_next_retry：定时任务轮询待重试的消息
//     典型查询: WHERE status = 0 AND next_retry_time <= NOW() ORDER BY next_retry_time LIMIT ?
type MessageOutbox struct {
	ID            int64     `gorm:"primaryKey;column:id;autoIncrement"`
	EventID       int64     `gorm:"column:event_id;type:bigint;not null;index:idx_event_id"`
	Topic         string    `gorm:"column:topic;type:varchar(64);not null"`
	Payload       []byte    `gorm:"column:payload;type:bytea;not null"`
	Status        int       `gorm:"column:status;type:smallint;default:0;index:idx_status_next_retry,priority:1"` // 0-待发送, 1-已发送, 2-失败
	RetryCount    int       `gorm:"column:retry_count;type:int;default:0"`
	NextRetryTime time.Time `gorm:"column:next_retry_time;index:idx_status_next_retry,priority:2"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AgentRun 是 Pilot 的持久化执行队列和状态机。
//
// 队列中的同一会话可以存在多个 Run，但从 CLAIMED 到 COMMITTING 最多只能有一个
// Active Run。READY_TO_COMMIT 之后的字段是不可变提交事实，租约恢复不得让它重新推理。
type AgentRun struct {
	RunID          string `gorm:"primaryKey;column:run_id;type:varchar(64);not null"`
	TenantID       string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uniq_agent_run_source,priority:1;uniqueIndex:uniq_agent_run_active_conversation,priority:1,where:status = 'CLAIMED' OR status = 'STARTING_RUNTIME' OR status = 'RUNNING' OR status = 'READY_TO_COMMIT' OR status = 'COMMITTING';index:idx_agent_run_conversation,priority:1"`
	ConversationID string `gorm:"column:conversation_id;type:varchar(64);not null;uniqueIndex:uniq_agent_run_active_conversation,priority:2;index:idx_agent_run_conversation,priority:2"`

	SourceEventID     int64  `gorm:"column:source_event_id;type:bigint;not null;uniqueIndex:uniq_agent_run_source,priority:2"`
	SourceSeqID       int64  `gorm:"column:source_seq_id;type:bigint;not null"`
	SourceTimestampMs int64  `gorm:"column:source_timestamp_ms;type:bigint;not null"`
	SourceHash        string `gorm:"column:source_hash;type:char(64);not null"`
	Prompt            string `gorm:"column:prompt;type:text;not null"`
	TraceContext      []byte `gorm:"column:trace_context;type:bytea"`

	ActorID        string `gorm:"column:actor_id;type:varchar(64);not null"`
	ActorUsername  string `gorm:"column:actor_username;type:varchar(64);not null"`
	ProfileID      string `gorm:"column:profile_id;type:varchar(64);not null"`
	ProfileVersion int64  `gorm:"column:profile_version;type:bigint;not null"`
	RuntimeKind    string `gorm:"column:runtime_kind;type:varchar(32);not null"`
	RuntimeVersion string `gorm:"column:runtime_version;type:varchar(64);not null"`
	BridgeVersion  string `gorm:"column:bridge_version;type:varchar(64);not null"`
	ModelProvider  string `gorm:"column:model_provider;type:varchar(64);not null"`
	ModelID        string `gorm:"column:model_id;type:varchar(128);not null"`

	Status      string `gorm:"column:status;type:varchar(32);not null;index:idx_agent_run_queue,priority:1;index:idx_agent_run_lease,priority:1"`
	Attempt     int    `gorm:"column:attempt;type:int;not null;default:0"`
	MaxAttempts int    `gorm:"column:max_attempts;type:int;not null"`
	Version     int64  `gorm:"column:version;type:bigint;not null;default:1"`

	LeaseOwner     string     `gorm:"column:lease_owner;type:varchar(128);not null;default:''"`
	LeaseToken     string     `gorm:"column:lease_token;type:varchar(128);not null;default:''"`
	LeaseExpiresAt *time.Time `gorm:"column:lease_expires_at;index:idx_agent_run_lease,priority:2"`
	AvailableAt    time.Time  `gorm:"column:available_at;not null;index:idx_agent_run_queue,priority:2"`
	QueuedAt       time.Time  `gorm:"column:queued_at;not null;index:idx_agent_run_queue,priority:3"`
	ClaimedAt      *time.Time `gorm:"column:claimed_at"`
	StartedAt      *time.Time `gorm:"column:started_at"`
	PreparedAt     *time.Time `gorm:"column:prepared_at"`
	CompletedAt    *time.Time `gorm:"column:completed_at"`
	// SessionInvalidatedAt is set by an authoritative history mutation. A Run
	// without a durable final message is cancelled; a Run whose final message
	// already exists may finish without promoting its stale Candidate Session.
	SessionInvalidatedAt *time.Time `gorm:"column:session_invalidated_at"`

	BaseSessionGeneration int64  `gorm:"column:base_session_generation;type:bigint;not null;default:0"`
	CandidateSessionID    string `gorm:"column:candidate_session_id;type:varchar(128);not null;default:''"`
	CandidateSessionRef   string `gorm:"column:candidate_session_ref;type:text;not null;default:''"`
	CandidateChecksum     string `gorm:"column:candidate_checksum;type:char(64);not null;default:''"`
	CandidateLeafEntryID  string `gorm:"column:candidate_leaf_entry_id;type:varchar(128);not null;default:''"`
	CandidateSessionBytes int64  `gorm:"column:candidate_session_bytes;type:bigint;not null;default:0"`
	CandidateEntryCount   int64  `gorm:"column:candidate_entry_count;type:bigint;not null;default:0"`
	FrozenFinalText       string `gorm:"column:frozen_final_text;type:text;not null;default:''"`
	FinalClientMsgID      string `gorm:"column:final_client_msg_id;type:varchar(64);not null;default:''"`

	FinalEventID        int64   `gorm:"column:final_event_id;type:bigint;not null;default:0"`
	FinalSeqID          int64   `gorm:"column:final_seq_id;type:bigint;not null;default:0"`
	FinalTimestampMs    int64   `gorm:"column:final_timestamp_ms;type:bigint;not null;default:0"`
	CommittedGeneration int64   `gorm:"column:committed_generation;type:bigint;not null;default:0"`
	InputTokens         int64   `gorm:"column:input_tokens;type:bigint;not null;default:0"`
	OutputTokens        int64   `gorm:"column:output_tokens;type:bigint;not null;default:0"`
	CacheReadTokens     int64   `gorm:"column:cache_read_tokens;type:bigint;not null;default:0"`
	CacheWriteTokens    int64   `gorm:"column:cache_write_tokens;type:bigint;not null;default:0"`
	TotalTokens         int64   `gorm:"column:total_tokens;type:bigint;not null;default:0"`
	UsageState          string  `gorm:"column:usage_state;type:varchar(16);not null;default:''"`
	CostMicros          int64   `gorm:"column:cost_micros;type:bigint;not null;default:0"`
	Cost                float64 `gorm:"column:cost;type:double precision;not null;default:0"`

	LastErrorCode      string `gorm:"column:last_error_code;type:varchar(64);not null;default:''"`
	LastErrorSummary   string `gorm:"column:last_error_summary;type:varchar(512);not null;default:''"`
	LastErrorRetryable bool   `gorm:"column:last_error_retryable;not null;default:false"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// AgentBudgetPolicy 是租户预算的权威配置。缺失或 Disabled 时必须拒绝新的 Attempt。
// 金额统一使用 micro-USD；MaxAttempt* 是 Reserve 的受控硬上界。
type AgentBudgetPolicy struct {
	TenantID               string `gorm:"primaryKey;column:tenant_id;type:varchar(64);not null"`
	Enabled                bool   `gorm:"column:enabled;not null;default:false"`
	DailyTokenLimit        int64  `gorm:"column:daily_token_limit;type:bigint;not null"`
	MonthlyTokenLimit      int64  `gorm:"column:monthly_token_limit;type:bigint;not null"`
	DailyCostLimitMicros   int64  `gorm:"column:daily_cost_limit_micros;type:bigint;not null"`
	MonthlyCostLimitMicros int64  `gorm:"column:monthly_cost_limit_micros;type:bigint;not null"`
	MaxAttemptTokens       int64  `gorm:"column:max_attempt_tokens;type:bigint;not null"`
	MaxAttemptCostMicros   int64  `gorm:"column:max_attempt_cost_micros;type:bigint;not null"`
	Version                int64  `gorm:"column:version;type:bigint;not null;default:1"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// AgentBudgetBucket 保存租户 UTC 日/月周期内已结算和仍被持有的用量。
// UnknownReserved* 是 Reserved* 的子集，仅用于可观测性，不能再次从余额中扣除。
type AgentBudgetBucket struct {
	TenantID                  string    `gorm:"primaryKey;column:tenant_id;type:varchar(64);not null"`
	PeriodKind                string    `gorm:"primaryKey;column:period_kind;type:varchar(8);not null"`
	PeriodStart               time.Time `gorm:"primaryKey;column:period_start;type:timestamptz;not null"`
	PolicyVersion             int64     `gorm:"column:policy_version;type:bigint;not null"`
	ReservedTokens            int64     `gorm:"column:reserved_tokens;type:bigint;not null;default:0"`
	SettledTokens             int64     `gorm:"column:settled_tokens;type:bigint;not null;default:0"`
	UnknownReservedTokens     int64     `gorm:"column:unknown_reserved_tokens;type:bigint;not null;default:0"`
	ReservedCostMicros        int64     `gorm:"column:reserved_cost_micros;type:bigint;not null;default:0"`
	SettledCostMicros         int64     `gorm:"column:settled_cost_micros;type:bigint;not null;default:0"`
	UnknownReservedCostMicros int64     `gorm:"column:unknown_reserved_cost_micros;type:bigint;not null;default:0"`
	Version                   int64     `gorm:"column:version;type:bigint;not null;default:1"`
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// AgentBudgetAttempt 是每个模型调用 Attempt 的幂等账本。
// Day/MonthPeriodStart 固化 Reserve 时的周期，跨周期结算必须回写原 Bucket。
type AgentBudgetAttempt struct {
	TenantID               string    `gorm:"primaryKey;column:tenant_id;type:varchar(64);not null"`
	RunID                  string    `gorm:"primaryKey;column:run_id;type:varchar(64);not null"`
	Attempt                int       `gorm:"primaryKey;column:attempt;type:int;not null"`
	ProfileID              string    `gorm:"column:profile_id;type:varchar(64);not null"`
	ProfileVersion         int64     `gorm:"column:profile_version;type:bigint;not null"`
	PolicyVersion          int64     `gorm:"column:policy_version;type:bigint;not null"`
	LeaseOwner             string    `gorm:"column:lease_owner;type:varchar(128);not null"`
	LeaseToken             string    `gorm:"column:lease_token;type:varchar(128);not null"`
	RunVersion             int64     `gorm:"column:run_version;type:bigint;not null"`
	DayPeriodStart         time.Time `gorm:"column:day_period_start;type:timestamptz;not null"`
	MonthPeriodStart       time.Time `gorm:"column:month_period_start;type:timestamptz;not null"`
	ReservedTokens         int64     `gorm:"column:reserved_tokens;type:bigint;not null"`
	ReservedCostMicros     int64     `gorm:"column:reserved_cost_micros;type:bigint;not null"`
	ActualInputTokens      int64     `gorm:"column:actual_input_tokens;type:bigint;not null;default:0"`
	ActualOutputTokens     int64     `gorm:"column:actual_output_tokens;type:bigint;not null;default:0"`
	ActualCacheReadTokens  int64     `gorm:"column:actual_cache_read_tokens;type:bigint;not null;default:0"`
	ActualCacheWriteTokens int64     `gorm:"column:actual_cache_write_tokens;type:bigint;not null;default:0"`
	ActualTotalTokens      int64     `gorm:"column:actual_total_tokens;type:bigint;not null;default:0"`
	ActualCostMicros       int64     `gorm:"column:actual_cost_micros;type:bigint;not null;default:0"`
	UsageState             string    `gorm:"column:usage_state;type:varchar(16);not null;default:''"`
	Status                 string    `gorm:"column:status;type:varchar(16);not null;index:idx_agent_budget_attempt_status,priority:1"`
	Version                int64     `gorm:"column:version;type:bigint;not null;default:1"`
	SettledAt              *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// AgentSessionBinding 保存会话当前已提交的 Runtime Session 快照。
// SessionRef 是不透明对象引用；业务代码不得解析 Pi JSONL 来推导身份或授权。
type AgentSessionBinding struct {
	TenantID             string `gorm:"primaryKey;column:tenant_id;type:varchar(64);not null;index:idx_agent_session_binding_status,priority:1"`
	ConversationID       string `gorm:"primaryKey;column:conversation_id;type:varchar(64);not null"`
	RuntimeKind          string `gorm:"column:runtime_kind;type:varchar(32);not null"`
	RuntimeVersion       string `gorm:"column:runtime_version;type:varchar(64);not null"`
	BridgeVersion        string `gorm:"column:bridge_version;type:varchar(64);not null"`
	RuntimeSessionID     string `gorm:"column:runtime_session_id;type:varchar(128);not null"`
	SessionRef           string `gorm:"column:session_ref;type:text;not null"`
	Checksum             string `gorm:"column:checksum;type:char(64);not null"`
	ProfileID            string `gorm:"column:profile_id;type:varchar(64);not null"`
	ProfileVersion       int64  `gorm:"column:profile_version;type:bigint;not null"`
	Generation           int64  `gorm:"column:generation;type:bigint;not null"`
	LastCommittedEntryID string `gorm:"column:last_committed_entry_id;type:varchar(128);not null"`
	SessionBytes         int64  `gorm:"column:session_bytes;type:bigint;not null;default:0"`
	EntryCount           int64  `gorm:"column:entry_count;type:bigint;not null;default:0"`
	Status               string `gorm:"column:status;type:varchar(32);not null;index:idx_agent_session_binding_status,priority:2"`
	Version              int64  `gorm:"column:version;type:bigint;not null;default:1"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// AgentApproval 是 Logic 持有的用户可见审批事实。它只保存脱敏参数摘要和哈希，
// 不保存冻结参数正文；冻结参数正文或安全引用由 AgentToolExecution 持有。
type AgentApproval struct {
	ID             int64  `gorm:"primaryKey;column:id;autoIncrement"`
	TenantID       string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uniq_agent_approval_call,priority:1;index:idx_agent_approval_run,priority:1;index:idx_agent_approval_expiry,priority:1"`
	RunID          string `gorm:"column:run_id;type:varchar(64);not null;index:idx_agent_approval_run,priority:2"`
	CallID         string `gorm:"column:call_id;type:varchar(128);not null;uniqueIndex:uniq_agent_approval_call,priority:2"`
	ToolName       string `gorm:"column:tool_name;type:varchar(128);not null"`
	RequesterID    string `gorm:"column:requester_id;type:varchar(64);not null"`
	ArgsHash       string `gorm:"column:args_hash;type:char(64);not null"`
	ArgsSummary    string `gorm:"column:args_summary;type:text;not null"`
	Status         string `gorm:"column:status;type:varchar(24);not null;index:idx_agent_approval_expiry,priority:2"`
	Decision       string `gorm:"column:decision;type:varchar(16);not null;default:'NONE'"`
	DecisionBy     string `gorm:"column:decision_by;type:varchar(64);not null;default:''"`
	DecisionReason string `gorm:"column:decision_reason;type:varchar(512);not null;default:''"`
	DecidedAt      *time.Time
	RevokedBy      string `gorm:"column:revoked_by;type:varchar(64);not null;default:''"`
	RevokeReason   string `gorm:"column:revoke_reason;type:varchar(512);not null;default:''"`
	RevokedAt      *time.Time
	ExpiredAt      *time.Time
	ExpiresAt      time.Time `gorm:"column:expires_at;not null;index:idx_agent_approval_expiry,priority:3"`
	Version        int64     `gorm:"column:version;type:bigint;not null;default:1"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AgentToolExecution 是 Pilot/Tool Broker 持有的冻结参数和执行事实。
// FrozenArgsRef 必须指向不可变、受保护的参数对象，表中不保存明文敏感参数。
type AgentToolExecution struct {
	ID                    int64  `gorm:"primaryKey;column:id;autoIncrement"`
	TenantID              string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uniq_agent_tool_call,priority:1;uniqueIndex:uniq_agent_tool_idempotency,priority:1;index:idx_agent_tool_run,priority:1"`
	RunID                 string `gorm:"column:run_id;type:varchar(64);not null;index:idx_agent_tool_run,priority:2"`
	CallID                string `gorm:"column:call_id;type:varchar(128);not null;uniqueIndex:uniq_agent_tool_call,priority:2"`
	RuntimeToolCallID     string `gorm:"column:runtime_tool_call_id;type:varchar(128);not null"`
	ToolName              string `gorm:"column:tool_name;type:varchar(128);not null"`
	ToolVersion           string `gorm:"column:tool_version;type:varchar(64);not null"`
	SchemaVersion         string `gorm:"column:schema_version;type:varchar(64);not null"`
	ArgsHash              string `gorm:"column:args_hash;type:char(64);not null"`
	FrozenArgsRef         string `gorm:"column:frozen_args_ref;type:text;not null"`
	ArgsSummary           string `gorm:"column:args_summary;type:text;not null"`
	IdempotencyKey        string `gorm:"column:idempotency_key;type:varchar(192);not null;uniqueIndex:uniq_agent_tool_idempotency,priority:2"`
	Status                string `gorm:"column:status;type:varchar(32);not null"`
	Version               int64  `gorm:"column:version;type:bigint;not null;default:1"`
	ApprovalVersion       int64  `gorm:"column:approval_version;type:bigint;not null;default:0"`
	Attempt               int    `gorm:"column:attempt;type:int;not null;default:0"`
	ReadyAt               *time.Time
	StartedAt             *time.Time
	LastFailedAt          *time.Time
	FinishedAt            *time.Time
	ResultRef             string `gorm:"column:result_ref;type:text;not null;default:''"`
	ResultSummary         string `gorm:"column:result_summary;type:text;not null;default:''"`
	ResultHash            string `gorm:"column:result_hash;type:char(64);not null;default:''"`
	DownstreamOperationID string `gorm:"column:downstream_operation_id;type:varchar(192);not null;default:''"`
	ErrorCode             string `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	ErrorSummary          string `gorm:"column:error_summary;type:varchar(512);not null;default:''"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// AgentFrozenToolArgs 保存 Pilot 在发起审批前冻结的规范化 Tool 参数。
// Payload 是规范化 JSON；Ref、CallID 和 ArgsHash 共同构成不可替换引用。
// 该表属于 Pilot 事实，Logic/IAM 执行端不得从请求参数绕过 ArgsHash 绑定。
type AgentFrozenToolArgs struct {
	ID                int64     `gorm:"primaryKey;column:id;autoIncrement"`
	TenantID          string    `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uniq_agent_frozen_ref,priority:1;uniqueIndex:uniq_agent_frozen_call,priority:1"`
	Ref               string    `gorm:"column:ref;type:varchar(512);not null;uniqueIndex:uniq_agent_frozen_ref,priority:2"`
	RunID             string    `gorm:"column:run_id;type:varchar(64);not null;index:idx_agent_frozen_run,priority:1"`
	CallID            string    `gorm:"column:call_id;type:varchar(128);not null;uniqueIndex:uniq_agent_frozen_call,priority:2;index:idx_agent_frozen_run,priority:2"`
	RequesterID       string    `gorm:"column:requester_id;type:varchar(64);not null"`
	ArgsHash          string    `gorm:"column:args_hash;type:char(64);not null"`
	Payload           []byte    `gorm:"column:payload;type:bytea;not null"`
	ApprovalExpiresAt time.Time `gorm:"column:approval_expires_at;not null"`
	CreatedAt         time.Time
}

// AgentIAMMutationReceipt 是 Logic 在 IAM 变更事务内写入的不可变执行收据。
// 它使下游已经成功、但 RPC 响应丢失时，重试仍返回同一个权威事实。
type AgentIAMMutationReceipt struct {
	ID                    int64  `gorm:"primaryKey;column:id;autoIncrement"`
	TenantID              string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uniq_agent_iam_operation,priority:1;uniqueIndex:uniq_agent_iam_call,priority:1;uniqueIndex:uniq_agent_iam_idempotency,priority:1"`
	OperationID           string `gorm:"column:operation_id;type:varchar(192);not null;uniqueIndex:uniq_agent_iam_operation,priority:2"`
	IdempotencyKey        string `gorm:"column:idempotency_key;type:varchar(192);not null;uniqueIndex:uniq_agent_iam_idempotency,priority:2"`
	RunID                 string `gorm:"column:run_id;type:varchar(64);not null"`
	CallID                string `gorm:"column:call_id;type:varchar(128);not null;uniqueIndex:uniq_agent_iam_call,priority:2"`
	ArgsHash              string `gorm:"column:args_hash;type:char(64);not null"`
	ToolName              string `gorm:"column:tool_name;type:varchar(128);not null"`
	RequesterID           string `gorm:"column:requester_id;type:varchar(64);not null"`
	TargetUsername        string `gorm:"column:target_username;type:varchar(64);not null"`
	PreviousStatus        string `gorm:"column:previous_status;type:varchar(16);not null"`
	ResultStatus          string `gorm:"column:result_status;type:varchar(16);not null"`
	PreviousVersion       int64  `gorm:"column:previous_version;type:bigint;not null"`
	ResultVersion         int64  `gorm:"column:result_version;type:bigint;not null"`
	ApprovalVersion       int64  `gorm:"column:approval_version;type:bigint;not null"`
	DownstreamCommittedAt time.Time
	CreatedAt             time.Time
}

// AgentAuditLog 是 Pilot 的追加式安全审计链。同一 Tenant/Run 内 Sequence 连续递增，
// PrevHash/EntryHash 形成 SHA-256 链；AuditID 是调用方提供的幂等键。
type AgentAuditLog struct {
	ID         int64  `gorm:"primaryKey;column:id;autoIncrement"`
	TenantID   string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uniq_agent_audit_id,priority:1;uniqueIndex:uniq_agent_audit_sequence,priority:1;index:idx_agent_audit_call,priority:1"`
	AuditID    string `gorm:"column:audit_id;type:varchar(128);not null;uniqueIndex:uniq_agent_audit_id,priority:2"`
	RunID      string `gorm:"column:run_id;type:varchar(64);not null;uniqueIndex:uniq_agent_audit_sequence,priority:2"`
	Sequence   int64  `gorm:"column:sequence;type:bigint;not null;uniqueIndex:uniq_agent_audit_sequence,priority:3"`
	CallID     string `gorm:"column:call_id;type:varchar(128);not null;default:'';index:idx_agent_audit_call,priority:2"`
	EventType  string `gorm:"column:event_type;type:varchar(64);not null"`
	ActorType  string `gorm:"column:actor_type;type:varchar(32);not null"`
	ActorID    string `gorm:"column:actor_id;type:varchar(64);not null"`
	Summary    string `gorm:"column:summary;type:text;not null"`
	DetailRef  string `gorm:"column:detail_ref;type:text;not null;default:''"`
	OccurredAt time.Time
	PrevHash   string `gorm:"column:prev_hash;type:char(64);not null"`
	EntryHash  string `gorm:"column:entry_hash;type:char(64);not null"`
	CreatedAt  time.Time
}

// ============================================================================
// 表名映射
// ============================================================================

func (User) TableName() string                { return "t_user" }
func (TenantMembership) TableName() string    { return "t_tenant_membership" }
func (SystemRoleBinding) TableName() string   { return "t_system_role_binding" }
func (Session) TableName() string             { return "t_session" }
func (SessionMember) TableName() string       { return "t_session_member" }
func (MessageContent) TableName() string      { return "t_message_content" }
func (Inbox) TableName() string               { return "t_inbox" }
func (MessageOutbox) TableName() string       { return "t_message_outbox" }
func (AgentRun) TableName() string            { return "t_agent_run" }
func (AgentBudgetPolicy) TableName() string   { return "t_agent_budget_policy" }
func (AgentBudgetBucket) TableName() string   { return "t_agent_budget_bucket" }
func (AgentBudgetAttempt) TableName() string  { return "t_agent_budget_attempt" }
func (AgentSessionBinding) TableName() string { return "t_agent_session_binding" }
func (AgentApproval) TableName() string       { return "t_agent_approval" }
func (AgentToolExecution) TableName() string  { return "t_agent_tool_execution" }
func (AgentFrozenToolArgs) TableName() string { return "t_agent_frozen_tool_args" }
func (AgentIAMMutationReceipt) TableName() string {
	return "t_agent_iam_mutation_receipt"
}
func (AgentAuditLog) TableName() string { return "t_agent_audit_log" }

// ============================================================================
// 常量
// ============================================================================

// Outbox 状态
const (
	OutboxStatusPending = 0
	OutboxStatusSent    = 1
	OutboxStatusFailed  = 2
)

const (
	AgentBudgetPeriodDay   = "DAY"
	AgentBudgetPeriodMonth = "MONTH"

	AgentUsageStateExact      = "EXACT"
	AgentUsageStateUnknown    = "UNKNOWN"
	AgentUsageStateNotStarted = "NOT_STARTED"

	AgentBudgetAttemptStatusReserved  = "RESERVED"
	AgentBudgetAttemptStatusSettled   = "SETTLED"
	AgentBudgetAttemptStatusReleased  = "RELEASED"
	AgentBudgetAttemptStatusUnknown   = "UNKNOWN"
	AgentBudgetAttemptStatusOverdrawn = "OVERDRAWN"
)

const (
	UserKindHuman    = 0
	UserKindAgentBot = 1
)

const (
	DefaultTenantID = "default"

	TenantMembershipStatusActive   = "ACTIVE"
	TenantMembershipStatusDisabled = "DISABLED"

	SystemRoleUser     = "user"
	SystemRoleIAMAdmin = "iam-admin"

	ScopeChatUse             = "chat:use"
	ScopeProfileSelfRead     = "profile:self:read"
	ScopeIAMUsersRead        = "iam:users:read"
	ScopeIAMUsersWrite       = "iam:users:write"
	ScopeIAMRolesRead        = "iam:roles:read"
	ScopeIAMRolesWrite       = "iam:roles:write"
	ScopeAgentApprovalRead   = "agent:approval:read"
	ScopeAgentApprovalDecide = "agent:approval:decide"
)

const (
	SessionKindStandard = 0
	SessionKindAI       = 1

	AgentProfileUserAssistant = "user-assistant"
	AgentProfileIAMAdmin      = "iam-admin"
)

const (
	AgentSessionBindingStatusActive  = "ACTIVE"
	AgentSessionBindingStatusDirty   = "DIRTY"
	AgentSessionBindingStatusRevoked = "REVOKED"
)

// Agent Run 状态。状态值同时写入数据库部分唯一索引，修改时必须配套迁移。
const (
	AgentRunStatusQueued          = "QUEUED"
	AgentRunStatusClaimed         = "CLAIMED"
	AgentRunStatusStartingRuntime = "STARTING_RUNTIME"
	AgentRunStatusRunning         = "RUNNING"
	AgentRunStatusReadyToCommit   = "READY_TO_COMMIT"
	AgentRunStatusCommitting      = "COMMITTING"
	AgentRunStatusSucceeded       = "SUCCEEDED"
	AgentRunStatusFailedRetryable = "FAILED_RETRYABLE"
	AgentRunStatusFailedFinal     = "FAILED_FINAL"
	AgentRunStatusCancelled       = "CANCELLED"
)

const (
	AgentApprovalStatusPending  = "PENDING"
	AgentApprovalStatusApproved = "APPROVED"
	AgentApprovalStatusRejected = "REJECTED"
	AgentApprovalStatusRevoked  = "REVOKED"
	AgentApprovalStatusExpired  = "EXPIRED"
)

const (
	AgentApprovalDecisionNone    = "NONE"
	AgentApprovalDecisionApprove = "APPROVE"
	AgentApprovalDecisionReject  = "REJECT"
)

const (
	AgentToolExecutionStatusPrepared        = "PREPARED"
	AgentToolExecutionStatusReady           = "READY"
	AgentToolExecutionStatusExecuting       = "EXECUTING"
	AgentToolExecutionStatusSucceeded       = "SUCCEEDED"
	AgentToolExecutionStatusFailedRetryable = "FAILED_RETRYABLE"
	AgentToolExecutionStatusFailedFinal     = "FAILED_FINAL"
	AgentToolExecutionStatusCancelled       = "CANCELLED"
)

// InboxEventType 枚举与 common.v1.ChatEvent.payload 对齐
const (
	InboxEventTypeMessage       = 1
	InboxEventTypeMessageRecall = 2
	InboxEventTypeMessageEdit   = 3
	InboxEventTypeReadReceipt   = 4
	InboxEventTypeSessionUpdate = 5
)

// AllModels 返回所有需要 AutoMigrate 的模型列表
func AllModels() []any {
	return []any{
		&User{},
		&TenantMembership{},
		&SystemRoleBinding{},
		&Session{},
		&SessionMember{},
		&MessageContent{},
		&Inbox{},
		&MessageOutbox{},
		&AgentRun{},
		&AgentBudgetPolicy{},
		&AgentBudgetBucket{},
		&AgentBudgetAttempt{},
		&AgentSessionBinding{},
		&AgentApproval{},
		&AgentToolExecution{},
		&AgentFrozenToolArgs{},
		&AgentIAMMutationReceipt{},
		&AgentAuditLog{},
	}
}
