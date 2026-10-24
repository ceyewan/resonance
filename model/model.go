package model

import "time"

// ============================================================================
// 非持久化模型（Redis）
// ============================================================================

// Router 存储用户与在线 Gateway 的映射，通常存放在 Redis 中。
type Router struct {
	Username  string `json:"username"`
	GatewayID string `json:"gateway_id"`
	RemoteIP  string `json:"remote_ip"`
	Timestamp int64  `json:"timestamp"`
}

// ============================================================================
// 持久化模型（PostgreSQL）
// 以下结构体的 GORM tag 是表结构的唯一真相来源，由 `go run main.go -module init`
// 调用 AutoMigrate 创建/更新。
// ============================================================================

// User 用户账号表。索引：PK(username)。
type User struct {
	Username  string `gorm:"primaryKey;column:username;type:varchar(64);not null"`
	Nickname  string `gorm:"column:nickname;type:varchar(64)"`
	Password  string `gorm:"column:password;type:varchar(128);not null"`
	Avatar    string `gorm:"column:avatar;type:varchar(255)"`
	Kind      int    `gorm:"column:kind;type:smallint;not null;default:0"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TenantMembership 是用户名在租户中的权威成员关系，也是 IM 的鉴权边界。
// User.Username 目前全局唯一，但所有 IAM 读写都必须同时携带 tenant_id。
// 索引：PK(tenant_id, username) + idx_tenant_membership_username(反查某用户的租户)
//   - idx_tenant_membership_status(按状态批量筛选)。
type TenantMembership struct {
	TenantID  string `gorm:"primaryKey;column:tenant_id;type:varchar(64);not null"`
	Username  string `gorm:"primaryKey;column:username;type:varchar(64);not null;index:idx_tenant_membership_username"`
	Status    string `gorm:"column:status;type:varchar(16);not null;index:idx_tenant_membership_status"`
	Version   int64  `gorm:"column:version;type:bigint;not null;default:1"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SystemRoleBinding 是租户级系统角色绑定，不得与 SessionMember.Role（会话角色）混用。
// 索引：PK(tenant_id, username, role) + idx_system_role_binding_username。
type SystemRoleBinding struct {
	TenantID  string `gorm:"primaryKey;column:tenant_id;type:varchar(64);not null"`
	Username  string `gorm:"primaryKey;column:username;type:varchar(64);not null;index:idx_system_role_binding_username"`
	Role      string `gorm:"primaryKey;column:role;type:varchar(32);not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Session 会话表（单聊/群聊）。
// 索引：PK(session_id) + uniq_pilot_owner(tenant_id, owner_username) WHERE kind = 1。
// uniq_pilot_owner 是"每个用户在每个租户只有一个 Pilot 私聊"的并发兜底，
// 同时也是并发建聊时唯一的幂等键。
type Session struct {
	SessionID string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
	Type      int    `gorm:"column:type;type:smallint;not null"`
	Kind      int    `gorm:"column:kind;type:smallint;not null;default:0"`
	TenantID  string `gorm:"column:tenant_id;type:varchar(64);not null;default:'default';uniqueIndex:uniq_pilot_owner,priority:1,where:kind = 1"`
	// Internal runtime configuration, never part of the conversation identity.
	ProfileID      string `gorm:"column:profile_id;type:varchar(64);not null;default:''"`
	ProfileVersion int64  `gorm:"column:profile_version;type:bigint;not null;default:0"`
	Name           string `gorm:"column:name;type:varchar(128)"`
	AvatarURL      string `gorm:"column:avatar_url;type:varchar(255)"`
	OwnerUsername  string `gorm:"column:owner_username;type:varchar(64);uniqueIndex:uniq_pilot_owner,priority:2,where:kind = 1"`
	MaxSeqID       int64  `gorm:"column:max_seq_id;type:bigint;default:0"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SessionMember 会话成员表。
// 索引：PK(session_id, username) + idx_member_username(username)。
//   - PK：按会话查成员、快速判断某用户是否在某会话中
//   - idx_member_username：反查某用户加入的所有会话（联系人列表、会话列表）
type SessionMember struct {
	SessionID   string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
	Username    string `gorm:"primaryKey;column:username;type:varchar(64);not null;index:idx_member_username"`
	Role        int    `gorm:"column:role;type:smallint;default:0"`
	LastReadSeq int64  `gorm:"column:last_read_seq;type:bigint;default:0"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MessageContent 消息主事实。
// 索引：
//   - PK(event_id)
//   - idx_sess_seq(session_id, seq_id)：按会话拉历史，seq_id 游标分页
//     典型查询: WHERE session_id = ? AND seq_id > ? ORDER BY seq_id LIMIT ?
//   - uniq_message_client_id(session_id, sender_username, client_msg_id)
//     WHERE client_msg_id <> ”：发送端幂等键，空 client_msg_id 不参与唯一约束
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

// Inbox 用户信箱（写扩散目标，存完整 ChatEvent）。
// 索引：PK(id) + uniq_owner_sess_seq(owner_username, session_id, seq_id)
//   - idx_owner_id(owner_username, id) + idx_owner_sess(owner_username, session_id)。
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

// MessageOutbox 本地消息表（Outbox 模式，可靠投递）。
// 索引：PK(id) + idx_event_id(event_id) + idx_status_next_retry(status, next_retry_time)。
//   - idx_event_id：按事件 ID 查投递状态（发送确认、幂等检查）
//   - idx_status_next_retry：定时任务轮询待重试消息
type MessageOutbox struct {
	ID            int64     `gorm:"primaryKey;column:id;autoIncrement"`
	EventID       int64     `gorm:"column:event_id;type:bigint;not null;index:idx_event_id"`
	Topic         string    `gorm:"column:topic;type:varchar(64);not null"`
	Payload       []byte    `gorm:"column:payload;type:bytea;not null"`
	Status        int       `gorm:"column:status;type:smallint;default:0;index:idx_status_next_retry,priority:1"`
	RetryCount    int       `gorm:"column:retry_count;type:int;default:0"`
	NextRetryTime time.Time `gorm:"column:next_retry_time;index:idx_status_next_retry,priority:2"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AgentRun 是 Pilot 的持久化执行队列与状态机。
//
// 同一会话允许排队多个 Run，但从 CLAIMED 到 COMMITTING 最多只有一个 Active Run；
// READY_TO_COMMIT 之后的字段是不可变提交事实，租约恢复不得让它重新推理。
// 索引：PK(run_id) + uniq_agent_run_source(tenant_id, source_event_id)
//   - uniq_agent_run_active_conversation(tenant_id, conversation_id) WHERE 活跃状态
//   - idx_agent_run_queue(status, available_at, queued_at)
//   - idx_agent_run_lease(status, lease_expires_at)
//
// 状态值同时写进部分唯一索引，修改状态集合必须配套迁移。
type AgentRun struct {
	RunID                 string     `gorm:"primaryKey;column:run_id;type:varchar(64);not null"`
	TenantID              string     `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uniq_agent_run_source,priority:1;uniqueIndex:uniq_agent_run_active_conversation,priority:1,where:status = 'CLAIMED' OR status = 'STARTING_RUNTIME' OR status = 'RUNNING' OR status = 'READY_TO_COMMIT' OR status = 'COMMITTING';index:idx_agent_run_conversation,priority:1"`
	ConversationID        string     `gorm:"column:conversation_id;type:varchar(64);not null;uniqueIndex:uniq_agent_run_active_conversation,priority:2;index:idx_agent_run_conversation,priority:2"`
	SourceEventID         int64      `gorm:"column:source_event_id;type:bigint;not null;uniqueIndex:uniq_agent_run_source,priority:2"`
	SourceSeqID           int64      `gorm:"column:source_seq_id;type:bigint;not null"`
	SourceTimestampMs     int64      `gorm:"column:source_timestamp_ms;type:bigint;not null"`
	SourceHash            string     `gorm:"column:source_hash;type:char(64);not null"`
	Prompt                string     `gorm:"column:prompt;type:text;not null"`
	TraceContext          []byte     `gorm:"column:trace_context;type:bytea"`
	ActorID               string     `gorm:"column:actor_id;type:varchar(64);not null"`
	ActorUsername         string     `gorm:"column:actor_username;type:varchar(64);not null"`
	ProfileID             string     `gorm:"column:profile_id;type:varchar(64);not null"`
	ProfileVersion        int64      `gorm:"column:profile_version;type:bigint;not null"`
	RuntimeVersion        string     `gorm:"column:runtime_version;type:varchar(64);not null"`
	BridgeVersion         string     `gorm:"column:bridge_version;type:varchar(64);not null"`
	ModelID               string     `gorm:"column:model_id;type:varchar(128);not null"`
	Status                string     `gorm:"column:status;type:varchar(32);not null;index:idx_agent_run_queue,priority:1;index:idx_agent_run_lease,priority:1"`
	Attempt               int        `gorm:"column:attempt;type:int;not null;default:0"`
	MaxAttempts           int        `gorm:"column:max_attempts;type:int;not null"`
	Version               int64      `gorm:"column:version;type:bigint;not null;default:1"`
	LeaseOwner            string     `gorm:"column:lease_owner;type:varchar(128);not null;default:''"`
	LeaseToken            string     `gorm:"column:lease_token;type:varchar(128);not null;default:''"`
	LeaseExpiresAt        *time.Time `gorm:"column:lease_expires_at;index:idx_agent_run_lease,priority:2"`
	AvailableAt           time.Time  `gorm:"column:available_at;not null;index:idx_agent_run_queue,priority:2"`
	QueuedAt              time.Time  `gorm:"column:queued_at;not null;index:idx_agent_run_queue,priority:3"`
	ClaimedAt             *time.Time
	StartedAt             *time.Time
	PreparedAt            *time.Time
	CompletedAt           *time.Time
	BaseSessionGeneration int64 `gorm:"column:base_session_generation;not null;default:0"`
	// BaseBindingVersion 是 Run 启动时读到的 binding 版本号。提交前若版本已变，
	// 说明权威历史在本轮执行期间被撤回/编辑改过，候选快照不得晋升为新基线。
	BaseBindingVersion    int64   `gorm:"column:base_binding_version;not null;default:0"`
	CandidateSessionID    string  `gorm:"column:candidate_session_id;type:varchar(128);not null;default:''"`
	CandidateSessionRef   string  `gorm:"column:candidate_session_ref;type:text;not null;default:''"`
	CandidateChecksum     string  `gorm:"column:candidate_checksum;type:char(64);not null;default:''"`
	CandidateLeafEntryID  string  `gorm:"column:candidate_leaf_entry_id;type:varchar(128);not null;default:''"`
	CandidateSessionBytes int64   `gorm:"column:candidate_session_bytes;not null;default:0"`
	CandidateEntryCount   int64   `gorm:"column:candidate_entry_count;not null;default:0"`
	FrozenFinalText       string  `gorm:"column:frozen_final_text;type:text;not null;default:''"`
	FinalClientMsgID      string  `gorm:"column:final_client_msg_id;type:varchar(64);not null;default:''"`
	FinalEventID          int64   `gorm:"column:final_event_id;not null;default:0"`
	FinalSeqID            int64   `gorm:"column:final_seq_id;not null;default:0"`
	FinalTimestampMs      int64   `gorm:"column:final_timestamp_ms;not null;default:0"`
	CommittedGeneration   int64   `gorm:"column:committed_generation;not null;default:0"`
	InputTokens           int64   `gorm:"column:input_tokens;not null;default:0"`
	OutputTokens          int64   `gorm:"column:output_tokens;not null;default:0"`
	CacheReadTokens       int64   `gorm:"column:cache_read_tokens;not null;default:0"`
	CacheWriteTokens      int64   `gorm:"column:cache_write_tokens;not null;default:0"`
	TotalTokens           int64   `gorm:"column:total_tokens;not null;default:0"`
	UsageState            string  `gorm:"column:usage_state;type:varchar(16);not null;default:''"`
	CostMicros            int64   `gorm:"column:cost_micros;not null;default:0"`
	Cost                  float64 `gorm:"column:cost;type:double precision;not null;default:0"`
	LastErrorCode         string  `gorm:"column:last_error_code;type:varchar(64);not null;default:''"`
	LastErrorSummary      string  `gorm:"column:last_error_summary;type:varchar(512);not null;default:''"`
	LastErrorRetryable    bool    `gorm:"column:last_error_retryable;not null;default:false"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// AgentSessionBinding 保存每个私聊当前已提交的 Pi Session 快照。
// SessionRef 是不透明对象引用，业务代码不得解析 Pi JSONL 来推导身份或授权。
// 每一次提交都推进 generation；撤回/编辑把 status 置 DIRTY，下一轮从权威历史重建。
// 索引：PK(tenant_id, conversation_id) + idx_agent_session_binding_status。
type AgentSessionBinding struct {
	TenantID             string `gorm:"primaryKey;column:tenant_id;type:varchar(64);not null;index:idx_agent_session_binding_status,priority:1"`
	ConversationID       string `gorm:"primaryKey;column:conversation_id;type:varchar(64);not null"`
	RuntimeVersion       string `gorm:"column:runtime_version;type:varchar(64);not null"`
	BridgeVersion        string `gorm:"column:bridge_version;type:varchar(64);not null"`
	RuntimeSessionID     string `gorm:"column:runtime_session_id;type:varchar(128);not null"`
	SessionRef           string `gorm:"column:session_ref;type:text;not null"`
	Checksum             string `gorm:"column:checksum;type:char(64);not null"`
	ProfileID            string `gorm:"column:profile_id;type:varchar(64);not null"`
	ProfileVersion       int64  `gorm:"column:profile_version;type:bigint;not null"`
	Generation           int64  `gorm:"column:generation;type:bigint;not null"`
	LastCommittedEntryID string `gorm:"column:last_committed_entry_id;type:varchar(128);not null"`
	SessionBytes         int64  `gorm:"column:session_bytes;not null;default:0"`
	EntryCount           int64  `gorm:"column:entry_count;not null;default:0"`
	Status               string `gorm:"column:status;type:varchar(32);not null;index:idx_agent_session_binding_status,priority:2"`
	Version              int64  `gorm:"column:version;not null;default:1"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (User) TableName() string                { return "t_user" }
func (TenantMembership) TableName() string    { return "t_tenant_membership" }
func (SystemRoleBinding) TableName() string   { return "t_system_role_binding" }
func (Session) TableName() string             { return "t_session" }
func (SessionMember) TableName() string       { return "t_session_member" }
func (MessageContent) TableName() string      { return "t_message_content" }
func (Inbox) TableName() string               { return "t_inbox" }
func (MessageOutbox) TableName() string       { return "t_message_outbox" }
func (AgentRun) TableName() string            { return "t_agent_run" }
func (AgentSessionBinding) TableName() string { return "t_agent_session_binding" }

const (
	OutboxStatusPending              = 0
	OutboxStatusSent                 = 1
	OutboxStatusFailed               = 2
	UserKindHuman                    = 0
	UserKindAgentBot                 = 1
	DefaultTenantID                  = "default"
	TenantMembershipStatusActive     = "ACTIVE"
	TenantMembershipStatusDisabled   = "DISABLED"
	SystemRoleUser                   = "user"
	ScopeChatUse                     = "chat:use"
	ScopeProfileSelfRead             = "profile:self:read"
	SessionKindStandard              = 0
	SessionKindAI                    = 1
	AgentProfileUserAssistant        = "user-assistant"
	AgentSessionBindingStatusActive  = "ACTIVE"
	AgentSessionBindingStatusDirty   = "DIRTY"
	AgentSessionBindingStatusRevoked = "REVOKED"
	AgentUsageStateExact             = "EXACT"
	AgentUsageStateUnknown           = "UNKNOWN"
	AgentUsageStateNotStarted        = "NOT_STARTED"
	AgentRunStatusQueued             = "QUEUED"
	AgentRunStatusClaimed            = "CLAIMED"
	AgentRunStatusStartingRuntime    = "STARTING_RUNTIME"
	AgentRunStatusRunning            = "RUNNING"
	AgentRunStatusReadyToCommit      = "READY_TO_COMMIT"
	AgentRunStatusCommitting         = "COMMITTING"
	AgentRunStatusSucceeded          = "SUCCEEDED"
	AgentRunStatusFailedRetryable    = "FAILED_RETRYABLE"
	AgentRunStatusFailedFinal        = "FAILED_FINAL"
	AgentRunStatusCancelled          = "CANCELLED"
	InboxEventTypeMessage            = 1
	InboxEventTypeMessageRecall      = 2
	InboxEventTypeMessageEdit        = 3
	InboxEventTypeReadReceipt        = 4
	InboxEventTypeSessionUpdate      = 5
)

func AllModels() []any {
	return []any{&User{}, &TenantMembership{}, &SystemRoleBinding{}, &Session{}, &SessionMember{}, &MessageContent{}, &Inbox{}, &MessageOutbox{}, &AgentRun{}, &AgentSessionBinding{}}
}
