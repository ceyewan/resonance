package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/bootstrap"
	"github.com/ceyewan/resonance/model"
)

func newTestMessageRepo(t *testing.T) (MessageRepo, context.Context) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	return repo, context.Background()
}

func TestMessageRepo_SaveMessageContent(t *testing.T) {
	repo, ctx := newTestMessageRepo(t)

	err := repo.SaveMessageContent(ctx, &model.MessageContent{
		EventID:        time.Now().UnixNano(),
		SessionID:      "s_1",
		SenderUsername: "alice",
		SeqID:          1,
		Content:        "hello",
		MsgType:        1,
	})
	require.NoError(t, err)

	msgs, err := repo.GetHistoryMessages(ctx, "s_1", 0, 10)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "hello", msgs[0].Content)
}

func TestMessageRepo_SaveInboxBatch_AndGetInboxDelta(t *testing.T) {
	repo, ctx := newTestMessageRepo(t)

	payload := []byte{0x01, 0x02, 0x03}
	items := []*model.Inbox{
		{
			OwnerUsername: "bob",
			SessionID:     "s_1",
			SeqID:         1,
			EventID:       1001,
			EventType:     model.InboxEventTypeMessage,
			Payload:       payload,
		},
	}

	err := repo.SaveInboxBatch(ctx, items)
	require.NoError(t, err)
	err = repo.SaveInboxBatch(ctx, items) // 幂等写入
	require.NoError(t, err)

	got, err := repo.GetInboxDelta(ctx, "bob", 0, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1001), got[0].EventID)
	require.Equal(t, payload, got[0].Payload)
}

func TestMessageRepo_GetUnreadMessageCount(t *testing.T) {
	repo, ctx := newTestMessageRepo(t)

	database := setupTestDB(t)
	gormDB := database.DB(ctx)
	require.NoError(t, gormDB.Create(&model.Session{
		SessionID: "s_1",
		Type:      1,
		MaxSeqID:  5,
	}).Error)
	require.NoError(t, gormDB.Create(&model.SessionMember{
		SessionID:   "s_1",
		Username:    "bob",
		LastReadSeq: 1,
	}).Error)

	err := repo.SaveInboxBatch(ctx, []*model.Inbox{
		// seq=1 已读位之前，无论类型都不该计入
		{OwnerUsername: "bob", SessionID: "s_1", SeqID: 1, EventID: 1001, EventType: model.InboxEventTypeMessage, Payload: []byte{0x01}},
		// seq=2,3 两条消息：应计入
		{OwnerUsername: "bob", SessionID: "s_1", SeqID: 2, EventID: 1002, EventType: model.InboxEventTypeMessage, Payload: []byte{0x02}},
		{OwnerUsername: "bob", SessionID: "s_1", SeqID: 3, EventID: 1003, EventType: model.InboxEventTypeMessage, Payload: []byte{0x03}},
		// seq=4 撤回事件：同步进 Inbox，但不计入角标
		{OwnerUsername: "bob", SessionID: "s_1", SeqID: 4, EventID: 1004, EventType: model.InboxEventTypeMessageRecall, Payload: []byte{0x04}},
		// seq=5 已读回执：同步进 Inbox，但不计入角标
		{OwnerUsername: "bob", SessionID: "s_1", SeqID: 5, EventID: 1005, EventType: model.InboxEventTypeReadReceipt, Payload: []byte{0x05}},
	})
	require.NoError(t, err)

	count, err := repo.GetUnreadMessageCount(ctx, "bob", "s_1")
	require.NoError(t, err)
	require.Equal(t, int64(2), count, "未读角标只统计 event_type = Message 的事件")
}

func TestMessageRepo_RecallWithOutboxAdvancesSessionMaxSeq(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)

	ctx := context.Background()
	gormDB := database.DB(ctx)
	require.NoError(t, gormDB.Create(&model.Session{
		SessionID: "s_recall_seq",
		Type:      2,
		MaxSeqID:  1,
	}).Error)
	require.NoError(t, gormDB.Create(&model.MessageContent{
		EventID:        2001,
		SessionID:      "s_recall_seq",
		SenderUsername: "alice",
		SeqID:          1,
		Content:        "hello",
		MsgType:        int(commonv1.MessageType_MESSAGE_TYPE_TEXT),
	}).Error)

	payload, err := proto.Marshal(&mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:      2002,
			SeqId:        2,
			SessionId:    "s_recall_seq",
			FromUsername: "alice",
			TimestampMs:  time.Now().UnixMilli(),
			Payload: &commonv1.ChatEvent_Recall{
				Recall: &commonv1.MessageRecall{TargetEventId: 2001},
			},
		},
		TargetUsernames: []string{"alice"},
	})
	require.NoError(t, err)

	err = repo.RecallMessageWithOutbox(ctx, 2001, time.Now(), &model.MessageOutbox{
		EventID:       2002,
		Topic:         "resonance.chat.event.v1",
		Payload:       payload,
		Status:        model.OutboxStatusPending,
		NextRetryTime: time.Now(),
	})
	require.NoError(t, err)

	var session model.Session
	require.NoError(t, gormDB.Where("session_id = ?", "s_recall_seq").First(&session).Error)
	require.Equal(t, int64(2), session.MaxSeqID)
}

func TestMessageRepo_HistoryMutationCancelsUncommittedAgentRunsAndDirtiesBinding(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	messageRepo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 9, 5, 0, 0, 0, time.UTC)
	createAgentMessageFixture(t, database.DB(ctx), "conversation-history-cancel", 9401, base)

	active := newTestAgentRun("run-history-active", "tenant-a", "conversation-history-cancel", 9401, 1, base)
	prepared := prepareTestAgentRun(t, ctx, runRepo, active, 1, "candidate-history-active", base)
	committing, err := runRepo.BeginAgentRunCommit(ctx, leaseFor(prepared, base.Add(5*time.Second)))
	require.NoError(t, err)
	queued := newTestAgentRun("run-history-queued", "tenant-a", "conversation-history-cancel", 9402, 2, base.Add(6*time.Second))
	_, err = runRepo.EnqueueAgentRun(ctx, queued)
	require.NoError(t, err)

	err = messageRepo.RecallMessageWithOutbox(ctx, 9401, base.Add(7*time.Second), historyMutationOutbox(t,
		"conversation-history-cancel", 9403, 3, &commonv1.ChatEvent_Recall{Recall: &commonv1.MessageRecall{TargetEventId: 9401}}, base.Add(7*time.Second)))
	require.NoError(t, err)

	for _, runID := range []string{committing.RunID, queued.RunID} {
		cancelled, getErr := runRepo.GetAgentRun(ctx, "tenant-a", runID)
		require.NoError(t, getErr)
		require.Equal(t, model.AgentRunStatusCancelled, cancelled.Status)
		require.NotNil(t, cancelled.SessionInvalidatedAt)
		require.Equal(t, "history_invalidated", cancelled.LastErrorCode)
		require.Empty(t, cancelled.LeaseToken)
	}
	binding, err := runRepo.GetAgentSessionBinding(ctx, "tenant-a", "conversation-history-cancel")
	require.NoError(t, err)
	require.Equal(t, model.AgentSessionBindingStatusDirty, binding.Status)

	_, err = messageRepo.SaveMessageWithOutbox(ctx, &model.MessageContent{
		EventID: 9404, SessionID: committing.ConversationID, SenderUsername: "resonance-agent", SeqID: 4,
		Content: committing.FrozenFinalText, MsgType: int(commonv1.MessageType_MESSAGE_TYPE_TEXT),
		ClientMsgID: committing.FinalClientMsgID, IdempotencyHash: testDigest("cancelled-final"), CreatedAt: base.Add(8 * time.Second),
	}, testMessageOutbox(9404))
	require.ErrorIs(t, err, ErrAgentFinalMessageNotCommittable)
}

func TestMessageRepo_HistoryMutationAfterFinalFactKeepsMessageAndSkipsStaleSessionCommit(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	messageRepo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 9, 6, 0, 0, 0, time.UTC)
	createAgentMessageFixture(t, database.DB(ctx), "conversation-history-fact", 9501, base)

	run := newTestAgentRun("run-history-fact", "tenant-a", "conversation-history-fact", 9501, 1, base)
	prepared := prepareTestAgentRun(t, ctx, runRepo, run, 1, "candidate-history-fact", base)
	committing, err := runRepo.BeginAgentRunCommit(ctx, leaseFor(prepared, base.Add(5*time.Second)))
	require.NoError(t, err)
	finalMessage := &model.MessageContent{
		EventID: 9502, SessionID: committing.ConversationID, SenderUsername: "resonance-agent", SeqID: 2,
		Content: committing.FrozenFinalText, MsgType: int(commonv1.MessageType_MESSAGE_TYPE_TEXT),
		ClientMsgID: committing.FinalClientMsgID, IdempotencyHash: testDigest("history-final-fact"), CreatedAt: base.Add(6 * time.Second),
	}
	saved, err := messageRepo.SaveMessageWithOutbox(ctx, finalMessage, testMessageOutbox(9502))
	require.NoError(t, err)
	require.True(t, saved.Created)

	err = messageRepo.EditMessageWithOutbox(ctx, 9501, "edited authoritative prompt", base.Add(7*time.Second), historyMutationOutbox(t,
		"conversation-history-fact", 9503, 3, &commonv1.ChatEvent_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 9501, NewContent: "edited authoritative prompt"}}, base.Add(7*time.Second)))
	require.NoError(t, err)
	invalidated, err := runRepo.GetAgentRun(ctx, "tenant-a", committing.RunID)
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusCommitting, invalidated.Status)
	require.NotNil(t, invalidated.SessionInvalidatedAt)
	require.Equal(t, committing.Version, invalidated.Version, "history marker must not break the in-flight final ACK fence")

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
	require.Zero(t, completed.CommittedGeneration, "invalidated Candidate must remain an orphan")

	binding, err := runRepo.GetAgentSessionBinding(ctx, "tenant-a", "conversation-history-fact")
	require.NoError(t, err)
	require.Equal(t, model.AgentSessionBindingStatusDirty, binding.Status)
	require.Equal(t, int64(1), binding.Generation)
	var finalCount int64
	require.NoError(t, database.DB(ctx).Model(&model.MessageContent{}).
		Where("session_id = ? AND client_msg_id = ?", committing.ConversationID, committing.FinalClientMsgID).
		Count(&finalCount).Error)
	require.Equal(t, int64(1), finalCount)
}

func TestMessageRepo_SaveMessageWithOutbox_IdempotentRetry(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	messageRepo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)

	ctx := context.Background()
	gormDB := database.DB(ctx)
	require.NoError(t, gormDB.Create(&model.Session{SessionID: "s_idem", Type: 1}).Error)

	firstMessage := testIdempotentMessage(3001, 1, "s_idem", "alice", "client-1", strings.Repeat("a", 64))
	firstOutbox := testMessageOutbox(3001)
	first, err := messageRepo.SaveMessageWithOutbox(ctx, firstMessage, firstOutbox)
	require.NoError(t, err)
	require.True(t, first.Created)
	require.Equal(t, int64(3001), first.Message.EventID)
	require.NotNil(t, first.Outbox)

	retryMessage := testIdempotentMessage(3002, 2, "s_idem", "alice", "client-1", strings.Repeat("a", 64))
	retry, err := messageRepo.SaveMessageWithOutbox(ctx, retryMessage, testMessageOutbox(3002))
	require.NoError(t, err)
	require.False(t, retry.Created)
	require.Equal(t, int64(3001), retry.Message.EventID)
	require.Equal(t, int64(1), retry.Message.SeqID)
	require.Nil(t, retry.Outbox)

	var messageCount, outboxCount int64
	require.NoError(t, gormDB.Model(&model.MessageContent{}).Count(&messageCount).Error)
	require.NoError(t, gormDB.Model(&model.MessageOutbox{}).Count(&outboxCount).Error)
	require.Equal(t, int64(1), messageCount)
	require.Equal(t, int64(1), outboxCount)

	var session model.Session
	require.NoError(t, gormDB.Where("session_id = ?", "s_idem").Take(&session).Error)
	require.Equal(t, int64(1), session.MaxSeqID, "duplicate must not advance the durable sequence")
}

func TestMessageRepo_SaveMessageWithOutbox_RejectsPayloadConflict(t *testing.T) {
	messageRepo, ctx := newTestMessageRepo(t)

	_, err := messageRepo.SaveMessageWithOutbox(
		ctx,
		testIdempotentMessage(3101, 1, "s_conflict", "alice", "client-1", strings.Repeat("a", 64)),
		testMessageOutbox(3101),
	)
	require.NoError(t, err)

	_, err = messageRepo.SaveMessageWithOutbox(
		ctx,
		testIdempotentMessage(3102, 2, "s_conflict", "alice", "client-1", strings.Repeat("b", 64)),
		testMessageOutbox(3102),
	)
	require.ErrorIs(t, err, ErrMessageIdempotencyConflict)
}

func TestMessageRepo_SaveMessageWithOutbox_EmptyClientMessageIDIsNotDeduplicated(t *testing.T) {
	messageRepo, ctx := newTestMessageRepo(t)

	for i := int64(0); i < 2; i++ {
		result, err := messageRepo.SaveMessageWithOutbox(
			ctx,
			testIdempotentMessage(3201+i, i+1, "s_empty", "system", "", ""),
			testMessageOutbox(3201+i),
		)
		require.NoError(t, err)
		require.True(t, result.Created)
	}
}

func TestMessageRepo_SaveMessageWithOutbox_IdempotencyKeyIncludesSessionAndSender(t *testing.T) {
	messageRepo, ctx := newTestMessageRepo(t)
	hash := strings.Repeat("c", 64)

	for i, key := range []struct {
		session string
		sender  string
	}{
		{session: "s_1", sender: "alice"},
		{session: "s_1", sender: "bob"},
		{session: "s_2", sender: "alice"},
	} {
		eventID := int64(3301 + i)
		result, err := messageRepo.SaveMessageWithOutbox(
			ctx,
			testIdempotentMessage(eventID, int64(i+1), key.session, key.sender, "shared-client-id", hash),
			testMessageOutbox(eventID),
		)
		require.NoError(t, err)
		require.True(t, result.Created)
	}
}

func TestMessageRepo_SaveMessageWithOutbox_PrimaryKeyConflictIsNotSwallowed(t *testing.T) {
	messageRepo, ctx := newTestMessageRepo(t)

	_, err := messageRepo.SaveMessageWithOutbox(
		ctx,
		testIdempotentMessage(3401, 1, "s_1", "alice", "client-a", strings.Repeat("d", 64)),
		testMessageOutbox(3401),
	)
	require.NoError(t, err)

	_, err = messageRepo.SaveMessageWithOutbox(
		ctx,
		testIdempotentMessage(3401, 2, "s_2", "bob", "client-b", strings.Repeat("e", 64)),
		testMessageOutbox(3402),
	)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrMessageIdempotencyConflict))
}

func TestMessageRepo_SaveMessageWithOutbox_ConcurrentRetryCreatesOneFact(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	messageRepo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)

	const attempts = 8
	ctx := context.Background()
	results := make(chan *MessageSaveResult, attempts)
	errorsCh := make(chan error, attempts)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			eventID := int64(3501 + index)
			result, saveErr := messageRepo.SaveMessageWithOutbox(
				ctx,
				testIdempotentMessage(eventID, int64(index+1), "s_concurrent", "bot", "client-concurrent-final", strings.Repeat("f", 64)),
				testMessageOutbox(eventID),
			)
			if saveErr != nil {
				errorsCh <- saveErr
				return
			}
			results <- result
		}(i)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsCh)

	for saveErr := range errorsCh {
		require.NoError(t, saveErr)
	}
	var canonicalEventID int64
	created := 0
	resultCount := 0
	for result := range results {
		resultCount++
		if canonicalEventID == 0 {
			canonicalEventID = result.Message.EventID
		}
		require.Equal(t, canonicalEventID, result.Message.EventID)
		if result.Created {
			created++
		}
	}
	require.Equal(t, attempts, resultCount)
	require.Equal(t, 1, created)

	gormDB := database.DB(ctx)
	var messageCount, outboxCount int64
	require.NoError(t, gormDB.Model(&model.MessageContent{}).Count(&messageCount).Error)
	require.NoError(t, gormDB.Model(&model.MessageOutbox{}).Count(&outboxCount).Error)
	require.Equal(t, int64(1), messageCount)
	require.Equal(t, int64(1), outboxCount)
}

func TestMessageRepo_IdempotencyIndexContract(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)

	type indexRow struct {
		Unique     bool   `gorm:"column:is_unique"`
		Definition string `gorm:"column:definition"`
	}
	var index indexRow
	err := database.DB(context.Background()).Raw(`
		SELECT i.indisunique AS is_unique, pg_get_indexdef(i.indexrelid) AS definition
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = ?`, "uniq_message_client_id").Take(&index).Error
	require.NoError(t, err)
	require.True(t, index.Unique)
	normalized := strings.Join(strings.Fields(index.Definition), " ")
	require.Contains(t, normalized, "(session_id, sender_username, client_msg_id)")
	require.Contains(t, normalized, "WHERE ((client_msg_id)::text <> ''::text)")
}

func TestMessageIdempotencyMigration_FailsClosedOnHistoricalDuplicatesAndWrongIndex(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	gormDB := database.DB(ctx)

	restoreIndex := func() {
		_ = gormDB.Exec("DELETE FROM t_message_content WHERE session_id = ?", "s_migration").Error
		_ = gormDB.Exec("DROP INDEX IF EXISTS uniq_message_client_id").Error
		_ = gormDB.Exec(`
			CREATE UNIQUE INDEX IF NOT EXISTS uniq_message_client_id
			ON t_message_content(session_id, sender_username, client_msg_id)
			WHERE client_msg_id <> ''`).Error
	}
	t.Cleanup(restoreIndex)

	require.NoError(t, gormDB.Exec("DROP INDEX uniq_message_client_id").Error)
	for eventID := int64(3601); eventID <= 3602; eventID++ {
		require.NoError(t, gormDB.Create(testIdempotentMessage(
			eventID,
			eventID-3600,
			"s_migration",
			"alice",
			"duplicate-key",
			strings.Repeat("a", 64),
		)).Error)
	}

	err := bootstrap.MigrateSchema(gormDB)
	require.Error(t, err)
	require.Contains(t, err.Error(), "found 1 duplicate")

	require.NoError(t, gormDB.Exec("DELETE FROM t_message_content WHERE session_id = ?", "s_migration").Error)
	require.NoError(t, gormDB.Exec(`
		CREATE INDEX uniq_message_client_id
		ON t_message_content(client_msg_id)`).Error)

	err = bootstrap.MigrateSchema(gormDB)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsafe definition")

	require.NoError(t, gormDB.Exec("DROP INDEX uniq_message_client_id").Error)
	require.NoError(t, bootstrap.MigrateSchema(gormDB))
}

func testIdempotentMessage(eventID, seqID int64, sessionID, sender, clientMessageID, hash string) *model.MessageContent {
	return &model.MessageContent{
		EventID:         eventID,
		SessionID:       sessionID,
		SenderUsername:  sender,
		SeqID:           seqID,
		Content:         fmt.Sprintf("message-%d", eventID),
		MsgType:         int(commonv1.MessageType_MESSAGE_TYPE_TEXT),
		ClientMsgID:     clientMessageID,
		IdempotencyHash: hash,
		CreatedAt:       time.Unix(eventID, 0).UTC(),
	}
}

func testMessageOutbox(eventID int64) *model.MessageOutbox {
	return &model.MessageOutbox{
		EventID:       eventID,
		Topic:         "resonance.chat.event.v1",
		Payload:       []byte{byte(eventID)},
		Status:        model.OutboxStatusPending,
		NextRetryTime: time.Unix(eventID, 0).UTC(),
	}
}

func createAgentMessageFixture(t *testing.T, gormDB *gorm.DB, conversationID string, sourceEventID int64, base time.Time) {
	t.Helper()
	require.NoError(t, gormDB.Create(&model.User{
		Username: "resonance-agent", Password: "disabled", Kind: model.UserKindAgentBot, CreatedAt: base,
	}).Error)
	require.NoError(t, gormDB.Create(&model.Session{
		SessionID: conversationID, Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT), Kind: model.SessionKindAI,
		TenantID: "tenant-a", ProfileID: model.AgentProfileUserAssistant, ProfileVersion: 1,
		OwnerUsername: "user-1", MaxSeqID: 1, CreatedAt: base,
	}).Error)
	require.NoError(t, gormDB.Create(&[]*model.SessionMember{
		{SessionID: conversationID, Username: "user-1", Role: 1, CreatedAt: base},
		{SessionID: conversationID, Username: "resonance-agent", Role: 0, CreatedAt: base},
	}).Error)
	require.NoError(t, gormDB.Create(&model.MessageContent{
		EventID: sourceEventID, SessionID: conversationID, SenderUsername: "user-1", SeqID: 1,
		Content: "original authoritative prompt", MsgType: int(commonv1.MessageType_MESSAGE_TYPE_TEXT), CreatedAt: base,
	}).Error)
	require.NoError(t, gormDB.Create(&model.AgentSessionBinding{
		TenantID: "tenant-a", ConversationID: conversationID,
		RuntimeKind: "pi", RuntimeVersion: "0.50.1", BridgeVersion: "1.0.0",
		RuntimeSessionID: "base-" + conversationID, SessionRef: "sessions/tenant-a/" + conversationID + "/base.jsonl",
		Checksum: testDigest("base-" + conversationID), ProfileID: model.AgentProfileUserAssistant, ProfileVersion: 1,
		Generation: 1, LastCommittedEntryID: "base-leaf", Status: model.AgentSessionBindingStatusActive, Version: 1,
		CreatedAt: base,
	}).Error)
}

func historyMutationOutbox(
	t *testing.T,
	conversationID string,
	eventID, seqID int64,
	payload any,
	at time.Time,
) *model.MessageOutbox {
	t.Helper()
	event := &commonv1.ChatEvent{
		EventId: eventID, SeqId: seqID, SessionId: conversationID,
		FromUsername: "user-1", TimestampMs: at.UnixMilli(),
	}
	switch value := payload.(type) {
	case *commonv1.ChatEvent_Recall:
		event.Payload = value
	case *commonv1.ChatEvent_Edit:
		event.Payload = value
	default:
		t.Fatalf("unsupported history mutation payload %T", payload)
	}
	encoded, err := proto.Marshal(&mqv1.MQEvent{Event: event, TargetUsernames: []string{"user-1", "resonance-agent"}})
	require.NoError(t, err)
	return &model.MessageOutbox{
		EventID: eventID, Topic: "resonance.chat.event.v1", Payload: encoded,
		Status: model.OutboxStatusPending, NextRetryTime: at,
	}
}
