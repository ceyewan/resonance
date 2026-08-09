package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
)

// This local dialect contract keeps the history/final-message race testable
// when Docker/Testcontainers is unavailable. PostgreSQL remains the production
// authority and has the corresponding locking/concurrency tests in
// message_test.go.
func TestMessageHistoryInvalidationSQLiteContract(t *testing.T) {
	database := newHistorySQLiteDB(t)
	messageRepo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 9, 7, 0, 0, 0, time.UTC)
	createAgentMessageFixture(t, database.DB(ctx), "conversation-sqlite-history", 9601, base)

	run := newTestAgentRun("run-sqlite-history", "tenant-a", "conversation-sqlite-history", 9601, 1, base)
	prepared := prepareTestAgentRun(t, ctx, runRepo, run, 1, "candidate-sqlite-history", base)
	committing, err := runRepo.BeginAgentRunCommit(ctx, leaseFor(prepared, base.Add(5*time.Second)))
	require.NoError(t, err)

	err = messageRepo.EditMessageWithOutbox(ctx, 9601, "new authoritative text", base.Add(6*time.Second), historyMutationOutbox(t,
		"conversation-sqlite-history", 9602, 2,
		&commonv1.ChatEvent_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 9601, NewContent: "new authoritative text"}},
		base.Add(6*time.Second)))
	require.NoError(t, err)

	cancelled, err := runRepo.GetAgentRun(ctx, "tenant-a", committing.RunID)
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusCancelled, cancelled.Status)
	require.NotNil(t, cancelled.SessionInvalidatedAt)
	binding, err := runRepo.GetAgentSessionBinding(ctx, "tenant-a", committing.ConversationID)
	require.NoError(t, err)
	require.Equal(t, model.AgentSessionBindingStatusDirty, binding.Status)

	_, err = messageRepo.SaveMessageWithOutbox(ctx, &model.MessageContent{
		EventID: 9603, SessionID: committing.ConversationID, SenderUsername: "resonance-agent", SeqID: 3,
		Content: committing.FrozenFinalText, MsgType: int(commonv1.MessageType_MESSAGE_TYPE_TEXT),
		ClientMsgID: committing.FinalClientMsgID, IdempotencyHash: testDigest("sqlite-stale-final"), CreatedAt: base.Add(7 * time.Second),
	}, testMessageOutbox(9603))
	require.ErrorIs(t, err, ErrAgentFinalMessageNotCommittable)
}

func TestMessageHistoryInvalidationAfterFinalFactSQLiteContract(t *testing.T) {
	database := newHistorySQLiteDB(t)
	messageRepo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC)
	createAgentMessageFixture(t, database.DB(ctx), "conversation-sqlite-final-fact", 9701, base)

	run := newTestAgentRun("run-sqlite-final-fact", "tenant-a", "conversation-sqlite-final-fact", 9701, 1, base)
	prepared := prepareTestAgentRun(t, ctx, runRepo, run, 1, "candidate-sqlite-final-fact", base)
	committing, err := runRepo.BeginAgentRunCommit(ctx, leaseFor(prepared, base.Add(5*time.Second)))
	require.NoError(t, err)
	finalMessage := &model.MessageContent{
		EventID: 9702, SessionID: committing.ConversationID, SenderUsername: "resonance-agent", SeqID: 2,
		Content: committing.FrozenFinalText, MsgType: int(commonv1.MessageType_MESSAGE_TYPE_TEXT),
		ClientMsgID: committing.FinalClientMsgID, IdempotencyHash: testDigest("sqlite-final-fact"), CreatedAt: base.Add(6 * time.Second),
	}
	result, err := messageRepo.SaveMessageWithOutbox(ctx, finalMessage, testMessageOutbox(9702))
	require.NoError(t, err)
	require.True(t, result.Created)

	err = messageRepo.EditMessageWithOutbox(ctx, 9701, "new authoritative text", base.Add(7*time.Second), historyMutationOutbox(t,
		"conversation-sqlite-final-fact", 9703, 3,
		&commonv1.ChatEvent_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 9701, NewContent: "new authoritative text"}},
		base.Add(7*time.Second)))
	require.NoError(t, err)

	invalidated, err := runRepo.GetAgentRun(ctx, "tenant-a", committing.RunID)
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusCommitting, invalidated.Status)
	require.NotNil(t, invalidated.SessionInvalidatedAt)
	require.Equal(t, committing.Version, invalidated.Version)

	recorded, err := runRepo.RecordAgentRunFinalMessage(ctx, AgentRunFinalMessage{
		Lease: leaseFor(invalidated, base.Add(8*time.Second)), EventID: finalMessage.EventID,
		SeqID: finalMessage.SeqID, TimestampMs: finalMessage.CreatedAt.UnixMilli(),
	})
	require.NoError(t, err)
	completed, err := runRepo.CompleteAgentRun(ctx, AgentRunCompletion{
		Lease: leaseFor(recorded, base.Add(9*time.Second)), CommittedGeneration: 2,
	})
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusSucceeded, completed.Status)
	require.Zero(t, completed.CommittedGeneration)

	binding, err := runRepo.GetAgentSessionBinding(ctx, "tenant-a", committing.ConversationID)
	require.NoError(t, err)
	require.Equal(t, model.AgentSessionBindingStatusDirty, binding.Status)
	require.Equal(t, int64(1), binding.Generation)
	var finalCount int64
	require.NoError(t, database.DB(ctx).Model(&model.MessageContent{}).
		Where("session_id = ? AND client_msg_id = ?", committing.ConversationID, committing.FinalClientMsgID).
		Count(&finalCount).Error)
	require.Equal(t, int64(1), finalCount)
}

type historySQLiteDB struct{ database *gorm.DB }

func newHistorySQLiteDB(t *testing.T) *historySQLiteDB {
	t.Helper()
	dsn := fmt.Sprintf("file:history-%d?mode=memory&cache=shared", time.Now().UnixNano())
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, database.AutoMigrate(
		&model.User{}, &model.Session{}, &model.SessionMember{}, &model.MessageContent{},
		&model.MessageOutbox{}, &model.AgentRun{}, &model.AgentSessionBinding{},
	))
	return &historySQLiteDB{database: database}
}

func (d *historySQLiteDB) DB(ctx context.Context) *gorm.DB {
	return d.database.WithContext(ctx)
}

func (d *historySQLiteDB) Transaction(ctx context.Context, fn func(context.Context, *gorm.DB) error) error {
	return d.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(ctx, tx) })
}

func (*historySQLiteDB) Close() error { return nil }
