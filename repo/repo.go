package repo

import (
	"context"
	"time"

	"github.com/ceyewan/resonance/model"
)

// RouterRepo 定义了路由表（用户与网关实例映射）的数据访问接口，通常由 Redis 实现
type RouterRepo interface {
	// SetUserGateway 设置用户的网关映射关系
	SetUserGateway(ctx context.Context, router *model.Router) error
	// GetUserGateway 获取用户的网关映射关系
	GetUserGateway(ctx context.Context, username string) (*model.Router, error)
	// DeleteUserGateway 删除用户的网关映射关系
	DeleteUserGateway(ctx context.Context, username string) error
	// BatchSetUserGateway 批量设置用户的网关映射关系
	// TODO: 目前实现为简单的循环调用，后续可优化为 Redis Pipeline 或 MSET
	BatchSetUserGateway(ctx context.Context, routers []*model.Router) error
	// BatchDeleteUserGateway 批量删除用户的网关映射关系
	// TODO: 目前实现为简单的循环调用，后续可优化为 Redis Pipeline
	BatchDeleteUserGateway(ctx context.Context, usernames []string) error
	// BatchGetUsersGateway 批量获取用户的网关映射关系
	// TODO: 目前实现为简单的循环调用，后续可优化为 Redis MGET 或管道方式
	BatchGetUsersGateway(ctx context.Context, usernames []string) ([]*model.Router, error)
	// Close 释放资源（如数据库连接等）
	Close() error
}

// UserRepo 定义了用户数据访问接口
type UserRepo interface {
	// CreateUser 创建新用户
	CreateUser(ctx context.Context, user *model.User) error
	// GetUserByUsername 根据用户名获取用户
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
	// GetUsersByUsernames 批量获取用户（避免 N+1 查询）
	GetUsersByUsernames(ctx context.Context, usernames []string) ([]*model.User, error)
	// SearchUsers 搜索用户
	SearchUsers(ctx context.Context, query string) ([]*model.User, error)
	// UpdateUser 更新用户信息
	UpdateUser(ctx context.Context, user *model.User) error
	// Close 释放资源（如数据库连接等）
	Close() error
}

// IdentityRepo 管理租户成员关系和系统角色。
// 所有查询和变更都要求显式 tenant_id；禁止提供无租户条件的 IAM API。
type IdentityRepo interface {
	// CreateIdentity 原子创建全局用户、租户成员关系和初始系统角色。
	CreateIdentity(ctx context.Context, user *model.User, membership *model.TenantMembership, roles []string) error
	CreateTenantMembership(ctx context.Context, membership *model.TenantMembership) error
	GetTenantMembership(ctx context.Context, tenantID, username string) (*model.TenantMembership, error)
	UpdateTenantMembershipStatus(ctx context.Context, tenantID, username, status string, expectedVersion int64) (*model.TenantMembership, error)
	CreateSystemRoleBinding(ctx context.Context, binding *model.SystemRoleBinding) error
	DeleteSystemRoleBinding(ctx context.Context, tenantID, username, role string) error
	ListSystemRoleBindings(ctx context.Context, tenantID, username string) ([]*model.SystemRoleBinding, error)
	ListTenantMemberships(ctx context.Context, tenantID string, limit int) ([]*model.TenantMembership, error)
	// ResolveTenantAuthorization 在单个数据库快照中返回成员关系和角色。
	ResolveTenantAuthorization(ctx context.Context, tenantID, username string) (*TenantAuthorization, error)
	Close() error
}

type TenantAuthorization struct {
	Membership *model.TenantMembership
	Roles      []string
}

// SessionRepo 定义了会话数据访问接口
type SessionRepo interface {
	// CreateSession 创建会话
	CreateSession(ctx context.Context, session *model.Session) error
	// CreateSessionWithMembers atomically creates a session and all members.
	// An exact existing direct/AI session is returned as created=false.
	CreateSessionWithMembers(ctx context.Context, session *model.Session, members []*model.SessionMember) (persisted *model.Session, created bool, err error)
	// GetSession 获取会话详情
	GetSession(ctx context.Context, sessionID string) (*model.Session, error)
	FindDirectSessionByMembers(ctx context.Context, tenantID, username1, username2 string) (*model.Session, error)
	// GetUserSession 获取特定用户的特定会话详情（包含最后阅读位置）
	GetUserSession(ctx context.Context, username, sessionID string) (*model.SessionMember, error)
	// GetUserSessionList 获取用户的所有会话列表
	GetUserSessionList(ctx context.Context, username string) ([]*model.Session, error)
	GetUserSessionListByTenant(ctx context.Context, tenantID, username string) ([]*model.Session, error)
	// GetUserSessionsBatch 批量获取用户的会话信息（避免 N+1 查询）
	GetUserSessionsBatch(ctx context.Context, username string, sessionIDs []string) ([]*model.SessionMember, error)
	// AddMember 添加成员
	AddMember(ctx context.Context, member *model.SessionMember) error
	// GetMembers 获取会话成员
	GetMembers(ctx context.Context, sessionID string) ([]*model.SessionMember, error)
	// UpdateMaxSeqID 更新会话最新序列号 (CAS操作)
	UpdateMaxSeqID(ctx context.Context, sessionID string, newSeqID int64) error
	// GetContactList 获取联系人列表（有过单聊关系的用户）
	GetContactList(ctx context.Context, username string) ([]*model.User, error)
	GetContactListByTenant(ctx context.Context, tenantID, username string) ([]*model.User, error)
	// UpdateLastReadSeq 更新用户在会话中的已读位置
	UpdateLastReadSeq(ctx context.Context, sessionID, username string, lastReadSeq int64) error
	// AdvanceLastReadSeqWithOutbox 在读游标前进时原子更新 last_read_seq 并写 Outbox；未前进时返回 advanced=false
	AdvanceLastReadSeqWithOutbox(ctx context.Context, sessionID, username string, lastReadSeq int64, outbox *model.MessageOutbox) (advanced bool, err error)
	// Close 释放资源（如数据库连接等）
	Close() error
}

// MessageRepo 定义了消息数据访问接口
type MessageRepo interface {
	// SaveMessageContent 保存消息内容
	SaveMessageContent(ctx context.Context, msg *model.MessageContent) error
	// SaveInboxBatch 批量写入信箱事件（写扩散）
	SaveInboxBatch(ctx context.Context, inboxes []*model.Inbox) error
	// GetHistoryMessages 拉取历史消息（beforeSeq=0 表示最近一页，否则拉取 seq_id < beforeSeq）
	GetHistoryMessages(ctx context.Context, sessionID string, beforeSeq int64, limit int) ([]*model.MessageContent, error)
	// GetLastMessage 获取会话的最后一条消息
	GetLastMessage(ctx context.Context, sessionID string) (*model.MessageContent, error)
	// GetLastMessagesBatch 批量获取会话的最后一条消息（避免 N+1 查询）
	GetLastMessagesBatch(ctx context.Context, sessionIDs []string) ([]*model.MessageContent, error)
	// GetInboxDelta 按游标拉取用户增量消息
	GetInboxDelta(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error)
	// GetUnreadMessageCount 获取用户在会话内的未读"消息"数
	// 只统计 event_type = InboxEventTypeMessage 的事件，Recall/Edit/ReadReceipt/SessionUpdate 均不计入角标
	GetUnreadMessageCount(ctx context.Context, username, sessionID string) (int64, error)
	// GetMessageByEventID 按 event_id 精确查询消息
	GetMessageByEventID(ctx context.Context, eventID int64) (*model.MessageContent, error)
	// GetMessageByIdempotencyKey 按发送端幂等键查询第一次持久化的消息。
	GetMessageByIdempotencyKey(ctx context.Context, sessionID, senderUsername, clientMsgID string) (*model.MessageContent, error)
	// MarkMessageRecalled 按 event_id 标记撤回
	MarkMessageRecalled(ctx context.Context, eventID int64, at time.Time) error
	// UpdateMessageContent 按 event_id 更新消息内容
	UpdateMessageContent(ctx context.Context, eventID int64, newContent string, at time.Time) error

	// SaveMessageWithOutbox 事务内幂等保存消息、推进会话序号并记录 Outbox。
	// 非空 client_msg_id 重试返回第一次持久化的 Message；同键不同 hash 返回 ErrMessageIdempotencyConflict。
	SaveMessageWithOutbox(ctx context.Context, msg *model.MessageContent, outbox *model.MessageOutbox) (*MessageSaveResult, error)
	// RecallMessageWithOutbox 事务内标记撤回并写 Outbox
	RecallMessageWithOutbox(ctx context.Context, eventID int64, recalledAt time.Time, outbox *model.MessageOutbox) error
	// EditMessageWithOutbox 事务内编辑消息并写 Outbox
	EditMessageWithOutbox(ctx context.Context, eventID int64, newContent string, editedAt time.Time, outbox *model.MessageOutbox) error
	// UpdateOutboxStatus 更新本地消息表状态
	UpdateOutboxStatus(ctx context.Context, id int64, status int) error
	// UpdateOutboxRetry 更新本地消息表重试信息
	UpdateOutboxRetry(ctx context.Context, id int64, nextRetry time.Time, count int) error
	// GetPendingOutboxMessages 获取待发送的本地消息
	GetPendingOutboxMessages(ctx context.Context, limit int) ([]*model.MessageOutbox, error)

	// Close 释放资源（如数据库连接等）
	Close() error
}

// MessageSaveResult 是消息主事务的幂等结果。
type MessageSaveResult struct {
	Message *model.MessageContent
	Outbox  *model.MessageOutbox
	Created bool
}

// AgentRunRepo 管理 Pilot durable queue、租约 fencing 与 prepare-then-commit 状态机。
// 所有按 Run 定位的方法都要求 tenant_id，调用方不能执行无租户条件的查询。
type AgentRunRepo interface {
	EnqueueAgentRun(ctx context.Context, run *model.AgentRun) (*AgentRunEnqueueResult, error)
	GetAgentRun(ctx context.Context, tenantID, runID string) (*model.AgentRun, error)
	ClaimNextAgentRun(ctx context.Context, claim AgentRunClaim) (*model.AgentRun, error)
	HeartbeatAgentRun(ctx context.Context, lease AgentRunLease) (*model.AgentRun, error)
	AdvanceAgentRun(ctx context.Context, transition AgentRunTransition) (*model.AgentRun, error)
	FailAgentRun(ctx context.Context, failure AgentRunFailure) (*model.AgentRun, error)
	CancelAgentRun(ctx context.Context, cancellation AgentRunCancellation) (*model.AgentRun, error)
	CancelPendingAgentRuns(ctx context.Context, cancellation AgentPendingRunCancellation) (int64, error)
	PrepareAgentRun(ctx context.Context, prepared AgentRunPreparedResult) (*model.AgentRun, error)
	BeginAgentRunCommit(ctx context.Context, lease AgentRunLease) (*model.AgentRun, error)
	ClaimPreparedAgentRun(ctx context.Context, claim AgentRunClaim) (*model.AgentRun, error)
	RecordAgentRunFinalMessage(ctx context.Context, result AgentRunFinalMessage) (*model.AgentRun, error)
	CompleteAgentRun(ctx context.Context, completion AgentRunCompletion) (*model.AgentRun, error)
	RecoverExpiredAgentRuns(ctx context.Context, tenantID string, now time.Time) (AgentRunRecoveryResult, error)
	PutAgentBudgetPolicy(ctx context.Context, policy *model.AgentBudgetPolicy, expectedVersion int64) (*model.AgentBudgetPolicy, error)
	GetAgentBudgetPolicy(ctx context.Context, tenantID string) (*model.AgentBudgetPolicy, error)
	ReserveAgentBudget(ctx context.Context, reservation AgentBudgetReservation) (*model.AgentBudgetAttempt, error)
	SettleAgentBudget(ctx context.Context, settlement AgentBudgetSettlement) (*model.AgentBudgetAttempt, error)
	RecoverExpiredAgentBudgetAttempts(ctx context.Context, tenantID string, now time.Time) (int64, error)
	GetAgentBudgetAttempt(ctx context.Context, tenantID, runID string, attempt int) (*model.AgentBudgetAttempt, error)
	GetAgentBudgetBucket(ctx context.Context, tenantID, periodKind string, periodStart time.Time) (*model.AgentBudgetBucket, error)
	GetAgentSessionBinding(ctx context.Context, tenantID, conversationID string) (*model.AgentSessionBinding, error)
	MarkAgentSessionBindingDirty(ctx context.Context, tenantID, conversationID string, expectedGeneration int64, now time.Time) (*model.AgentSessionBinding, error)
	// WithAgentSessionGCLock holds a PostgreSQL transaction advisory lock,
	// snapshots live Session references across every tenant sharing the Store,
	// and invokes prune before releasing the lock.
	WithAgentSessionGCLock(ctx context.Context, lockID string, prune func(context.Context, []string) error) (bool, error)
	Close() error
}

type AgentRunEnqueueResult struct {
	Run     *model.AgentRun
	Created bool
}

// AgentRunClaim 的 LeaseToken 必须由协调器使用密码学安全随机数生成。
type AgentRunClaim struct {
	TenantID       string
	ProfileID      string
	ProfileVersion int64
	WorkerID       string
	LeaseToken     string
	Now            time.Time
	LeaseDuration  time.Duration
}

// AgentRunLease 是所有 Active Run 写操作的完整 fencing 条件。
type AgentRunLease struct {
	TenantID        string
	RunID           string
	WorkerID        string
	LeaseToken      string
	ExpectedVersion int64
	Now             time.Time
	LeaseDuration   time.Duration
}

type AgentRunTransition struct {
	Lease AgentRunLease
	From  string
	To    string
}

type AgentRunFailure struct {
	Lease        AgentRunLease
	ErrorCode    string
	ErrorSummary string
	Retryable    bool
	RetryAt      time.Time
}

type AgentRunCancellation struct {
	Lease        AgentRunLease
	ErrorCode    string
	ErrorSummary string
}

type AgentPendingRunCancellation struct {
	TenantID       string
	ActorID        string
	ProfileID      string
	ProfileVersion int64
	ErrorCode      string
	ErrorSummary   string
	Now            time.Time
}

type AgentRunPreparedResult struct {
	Lease                 AgentRunLease
	BaseSessionGeneration int64
	CandidateSessionID    string
	CandidateSessionRef   string
	CandidateChecksum     string
	CandidateLeafEntryID  string
	CandidateSessionBytes int64
	CandidateEntryCount   int64
	FrozenFinalText       string
	FinalClientMsgID      string
	InputTokens           int64
	OutputTokens          int64
	CacheReadTokens       int64
	CacheWriteTokens      int64
	TotalTokens           int64
	UsageState            string
	CostMicros            int64
	Cost                  float64
}

type AgentRunFinalMessage struct {
	Lease       AgentRunLease
	EventID     int64
	SeqID       int64
	TimestampMs int64
}

type AgentRunCompletion struct {
	Lease               AgentRunLease
	CommittedGeneration int64
}

type AgentRunRecoveryResult struct {
	RetryableExecutions   int64
	FinalFailures         int64
	PreparedRuns          int64
	UnknownBudgetAttempts int64
}
