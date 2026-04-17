package repo

import (
	"context"
	"testing"
	"time"

	"github.com/ceyewan/resonance/model"
	"github.com/stretchr/testify/require"
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

func TestMessageRepo_GetUnreadCount(t *testing.T) {
	repo, ctx := newTestMessageRepo(t)

	database := setupTestDB(t)
	gormDB := database.DB(ctx)
	require.NoError(t, gormDB.Create(&model.Session{
		SessionID: "s_1",
		Type:      1,
		MaxSeqID:  3,
	}).Error)
	require.NoError(t, gormDB.Create(&model.SessionMember{
		SessionID:   "s_1",
		Username:    "bob",
		LastReadSeq: 1,
	}).Error)

	err := repo.SaveInboxBatch(ctx, []*model.Inbox{
		{
			OwnerUsername: "bob",
			SessionID:     "s_1",
			SeqID:         1,
			EventID:       1001,
			EventType:     model.InboxEventTypeMessage,
			Payload:       []byte{0x01},
		},
		{
			OwnerUsername: "bob",
			SessionID:     "s_1",
			SeqID:         2,
			EventID:       1002,
			EventType:     model.InboxEventTypeMessage,
			Payload:       []byte{0x02},
		},
		{
			OwnerUsername: "bob",
			SessionID:     "s_1",
			SeqID:         3,
			EventID:       1003,
			EventType:     model.InboxEventTypeMessage,
			Payload:       []byte{0x03},
		},
	})
	require.NoError(t, err)

	count, err := repo.GetUnreadCount(ctx, "bob", "s_1")
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
}
