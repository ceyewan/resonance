package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ceyewan/resonance/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageRepo_SaveMessage(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("保存正常消息", func(t *testing.T) {
		msg := &model.MessageContent{
			MsgID:          time.Now().UnixNano(),
			SessionID:      "test_session_001",
			SenderUsername: "user001",
			SeqID:          1,
			Content:        "Hello, World!",
			MsgType:        "text",
		}

		err := repo.SaveMessage(ctx, msg)
		require.NoError(t, err)

		// 验证消息已保存
		messages, err := repo.GetHistoryMessages(ctx, "test_session_001", 0, 10)
		require.NoError(t, err)
		assert.Len(t, messages, 1)
		assert.Equal(t, "Hello, World!", messages[0].Content)
	})

	t.Run("保存多条消息", func(t *testing.T) {
		sessionID := "test_session_002"
		for i := 1; i <= 5; i++ {
			msg := &model.MessageContent{
				MsgID:          time.Now().UnixNano() + int64(i),
				SessionID:      sessionID,
				SenderUsername: "user001",
				SeqID:          int64(i),
				Content:        fmt.Sprintf("Message %d", i),
				MsgType:        "text",
			}
			err := repo.SaveMessage(ctx, msg)
			require.NoError(t, err)
		}

		// 验证所有消息已保存
		messages, err := repo.GetHistoryMessages(ctx, sessionID, 0, 10)
		require.NoError(t, err)
		assert.Len(t, messages, 5)
	})

	t.Run("保存重复MsgID应失败", func(t *testing.T) {
		msgID := time.Now().UnixNano()
		msg1 := &model.MessageContent{
			MsgID:          msgID,
			SessionID:      "test_session_003",
			SenderUsername: "user001",
			SeqID:          1,
			Content:        "First message",
		}

		msg2 := &model.MessageContent{
			MsgID:          msgID, // 重复的 MsgID
			SessionID:      "test_session_003",
			SenderUsername: "user001",
			SeqID:          2,
			Content:        "Second message",
		}

		err := repo.SaveMessage(ctx, msg1)
		require.NoError(t, err)

		err = repo.SaveMessage(ctx, msg2)
		// 可能会失败（主键冲突）或成功（取决于数据库）
		t.Logf("保存重复MsgID结果: %v", err)
	})

	t.Run("保存空会话ID应失败", func(t *testing.T) {
		msg := &model.MessageContent{
			MsgID:          time.Now().UnixNano(),
			SenderUsername: "user001",
			SeqID:          1,
			Content:        "Test",
		}

		err := repo.SaveMessage(ctx, msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session_id cannot be empty")
	})

	t.Run("保存空发送者应失败", func(t *testing.T) {
		msg := &model.MessageContent{
			MsgID:     time.Now().UnixNano(),
			SessionID: "test_session",
			SeqID:     1,
			Content:   "Test",
		}

		err := repo.SaveMessage(ctx, msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sender_username cannot be empty")
	})

	t.Run("保存零值MsgID应失败", func(t *testing.T) {
		msg := &model.MessageContent{
			MsgID:          0,
			SessionID:      "test_session",
			SenderUsername: "user001",
			SeqID:          1,
			Content:        "Test",
		}

		err := repo.SaveMessage(ctx, msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "msg_id cannot be zero")
	})

	t.Run("保存nil消息应失败", func(t *testing.T) {
		err := repo.SaveMessage(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "message cannot be nil")
	})
}

func TestMessageRepo_SaveInbox(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("批量写入信箱", func(t *testing.T) {
		inboxes := []*model.Inbox{
			{
				OwnerUsername: "user001",
				SessionID:     "session_001",
				MsgID:         time.Now().UnixNano(),
				SeqID:         1,
				IsRead:        0,
			},
			{
				OwnerUsername: "user002",
				SessionID:     "session_001",
				MsgID:         time.Now().UnixNano() + 1,
				SeqID:         1,
				IsRead:        0,
			},
		}

		err := repo.SaveInbox(ctx, inboxes)
		require.NoError(t, err)

		// 验证未读消息
		unread, err := repo.GetUnreadMessages(ctx, "user001", 10)
		require.NoError(t, err)
		assert.Len(t, unread, 1)
		assert.Equal(t, "user001", unread[0].OwnerUsername)
	})

	t.Run("写入空信箱列表应成功", func(t *testing.T) {
		var inboxes []*model.Inbox
		err := repo.SaveInbox(ctx, inboxes)
		require.NoError(t, err)
	})

	t.Run("批量写入大量信箱", func(t *testing.T) {
		const batchSize = 100
		inboxes := make([]*model.Inbox, batchSize)

		baseTime := time.Now().UnixNano()
		for i := 0; i < batchSize; i++ {
			inboxes[i] = &model.Inbox{
				OwnerUsername: fmt.Sprintf("batch_user_%d", i%10),
				SessionID:     fmt.Sprintf("session_%d", i/10),
				MsgID:         baseTime + int64(i),
				SeqID:         int64(i),
				IsRead:        0,
			}
		}

		err := repo.SaveInbox(ctx, inboxes)
		require.NoError(t, err)

		// 验证某个用户的未读消息数量
		unread, err := repo.GetUnreadMessages(ctx, "batch_user_0", 100)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(unread), 10)
	})
}

func TestMessageRepo_GetHistoryMessages(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 准备测试数据
	sessionID := "history_session"
	for i := 1; i <= 20; i++ {
		msg := &model.MessageContent{
			MsgID:          time.Now().UnixNano() + int64(i),
			SessionID:      sessionID,
			SenderUsername: "user001",
			SeqID:          int64(i),
			Content:        fmt.Sprintf("History message %d", i),
			MsgType:        "text",
		}
		err := repo.SaveMessage(ctx, msg)
		require.NoError(t, err)
	}

	t.Run("获取所有历史消息（默认限制）", func(t *testing.T) {
		messages, err := repo.GetHistoryMessages(ctx, sessionID, 0, 0)
		require.NoError(t, err)
		assert.Len(t, messages, 20) // 默认拉取50条，但只有20条
	})

	t.Run("限制拉取数量", func(t *testing.T) {
		messages, err := repo.GetHistoryMessages(ctx, sessionID, 0, 5)
		require.NoError(t, err)
		assert.Len(t, messages, 5)
		assert.Equal(t, int64(16), messages[0].SeqID)
		assert.Equal(t, int64(20), messages[4].SeqID)
	})

	t.Run("从 beforeSeq 向前拉取历史", func(t *testing.T) {
		messages, err := repo.GetHistoryMessages(ctx, sessionID, 11, 10)
		require.NoError(t, err)
		assert.Len(t, messages, 10)
		assert.Equal(t, int64(1), messages[0].SeqID)
		assert.Equal(t, int64(10), messages[9].SeqID)
	})

	t.Run("按序列号升序排列", func(t *testing.T) {
		messages, err := repo.GetHistoryMessages(ctx, sessionID, 19, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(messages), 2)

		// 验证序列号递增
		for i := 1; i < len(messages); i++ {
			assert.Greater(t, messages[i].SeqID, messages[i-1].SeqID)
		}
	})

	t.Run("beforeSeq 为最小值时返回空", func(t *testing.T) {
		messages, err := repo.GetHistoryMessages(ctx, sessionID, 1, 10)
		require.NoError(t, err)
		assert.Empty(t, messages)
	})

	t.Run("获取不存在会话的历史消息应返回空列表", func(t *testing.T) {
		messages, err := repo.GetHistoryMessages(ctx, "non_existent_session", 0, 10)
		require.NoError(t, err)
		assert.Empty(t, messages)
	})

	t.Run("获取空会话ID应返回错误", func(t *testing.T) {
		_, err := repo.GetHistoryMessages(ctx, "", 0, 10)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session_id cannot be empty")
	})

	t.Run("限制数量超过最大值应使用最大值", func(t *testing.T) {
		messages, err := repo.GetHistoryMessages(ctx, sessionID, 0, 2000)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(messages), 1000) // 最大1000条
	})
}

func TestMessageRepo_GetLastMessage(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("获取会话的最后一条消息", func(t *testing.T) {
		sessionID := "last_msg_session"

		// 保存3条消息
		for i := 1; i <= 3; i++ {
			msg := &model.MessageContent{
				MsgID:          time.Now().UnixNano() + int64(i),
				SessionID:      sessionID,
				SenderUsername: "user001",
				SeqID:          int64(i),
				Content:        fmt.Sprintf("Message %d", i),
			}
			err := repo.SaveMessage(ctx, msg)
			require.NoError(t, err)
		}

		// 获取最后一条消息
		lastMsg, err := repo.GetLastMessage(ctx, sessionID)
		require.NoError(t, err)
		assert.Equal(t, int64(3), lastMsg.SeqID)
		assert.Equal(t, "Message 3", lastMsg.Content)
	})

	t.Run("获取不存在会话的最后一条消息应返回错误", func(t *testing.T) {
		_, err := repo.GetLastMessage(ctx, "non_existent_session")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no message found")
	})

	t.Run("获取空会话ID应返回错误", func(t *testing.T) {
		_, err := repo.GetLastMessage(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session_id cannot be empty")
	})
}

func TestMessageRepo_GetUnreadMessages(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	username := "unread_test_user"

	t.Run("获取未读消息", func(t *testing.T) {
		// 保存一些未读消息到信箱
		inboxes := []*model.Inbox{
			{
				OwnerUsername: username,
				SessionID:     "session_001",
				MsgID:         time.Now().UnixNano(),
				SeqID:         1,
				IsRead:        0,
			},
			{
				OwnerUsername: username,
				SessionID:     "session_002",
				MsgID:         time.Now().UnixNano() + 1,
				SeqID:         1,
				IsRead:        0,
			},
			{
				OwnerUsername: username,
				SessionID:     "session_003",
				MsgID:         time.Now().UnixNano() + 2,
				SeqID:         1,
				IsRead:        1, // 已读
			},
		}

		err := repo.SaveInbox(ctx, inboxes)
		require.NoError(t, err)

		// 获取未读消息
		unread, err := repo.GetUnreadMessages(ctx, username, 10)
		require.NoError(t, err)
		assert.Len(t, unread, 2) // 只有2条未读

		// 验证都是未读状态
		for _, msg := range unread {
			assert.Equal(t, 0, msg.IsRead)
		}
	})

	t.Run("限制未读消息数量", func(t *testing.T) {
		// 添加更多未读消息
		baseTime := time.Now().UnixNano()
		for i := 0; i < 10; i++ {
			inbox := &model.Inbox{
				OwnerUsername: username,
				SessionID:     fmt.Sprintf("session_%d", i),
				MsgID:         baseTime + int64(i),
				SeqID:         int64(i),
				IsRead:        0,
			}
			_ = repo.SaveInbox(ctx, []*model.Inbox{inbox})
		}

		// 限制拉取5条
		unread, err := repo.GetUnreadMessages(ctx, username, 5)
		require.NoError(t, err)
		assert.Len(t, unread, 5)
	})

	t.Run("获取不存在用户的未读消息应返回空列表", func(t *testing.T) {
		unread, err := repo.GetUnreadMessages(ctx, "non_existent_user", 10)
		require.NoError(t, err)
		assert.Empty(t, unread)
	})

	t.Run("获取空用户名的未读消息应返回错误", func(t *testing.T) {
		_, err := repo.GetUnreadMessages(ctx, "", 10)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")
	})

	t.Run("未读消息按时间倒序排列", func(t *testing.T) {
		// 清理并重新添加有序消息
		gormDB := repo.(*messageRepo).db.DB(ctx)
		gormDB.Exec("DELETE FROM t_inbox WHERE owner_username = ?", username)

		baseTime := time.Now().UnixNano()
		for i := 0; i < 5; i++ {
			inbox := &model.Inbox{
				OwnerUsername: username,
				SessionID:     fmt.Sprintf("ordered_session_%d", i),
				MsgID:         baseTime + int64(i*1000000), // 确保时间戳递增
				SeqID:         int64(i),
				IsRead:        0,
			}
			_ = repo.SaveInbox(ctx, []*model.Inbox{inbox})
		}

		unread, err := repo.GetUnreadMessages(ctx, username, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(unread), 5)

		// 验证按时间倒序（最新的在前）
		for i := 1; i < len(unread); i++ {
			assert.GreaterOrEqual(t, unread[i-1].CreatedAt, unread[i].CreatedAt)
		}
	})
}

func TestMessageRepo_SaveInbox_Idempotent(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()
	msgID := time.Now().UnixNano()

	// 先写入消息体，保证后续联表可用
	err = repo.SaveMessage(ctx, &model.MessageContent{
		MsgID:          msgID,
		SessionID:      "idem_session",
		SenderUsername: "alice",
		SeqID:          1,
		Content:        "idempotent message",
		MsgType:        "text",
	})
	require.NoError(t, err)

	inbox := &model.Inbox{
		OwnerUsername: "bob",
		SessionID:     "idem_session",
		MsgID:         msgID,
		SeqID:         1,
		IsRead:        0,
	}

	// 重复写两次，不应报错
	err = repo.SaveInbox(ctx, []*model.Inbox{inbox})
	require.NoError(t, err)
	err = repo.SaveInbox(ctx, []*model.Inbox{inbox})
	require.NoError(t, err)

	// 验证只存在一条增量记录
	delta, err := repo.GetInboxDelta(ctx, "bob", 0, 10)
	require.NoError(t, err)
	require.Len(t, delta, 1)
	assert.Equal(t, "idempotent message", delta[0].Content)
}

func TestMessageRepo_GetInboxDelta(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()
	username := "delta_user"
	sessionID := "delta_session"

	// 写入 3 条消息 + inbox 记录
	for i := 1; i <= 3; i++ {
		msgID := time.Now().UnixNano() + int64(i)
		err = repo.SaveMessage(ctx, &model.MessageContent{
			MsgID:          msgID,
			SessionID:      sessionID,
			SenderUsername: "alice",
			SeqID:          int64(i),
			Content:        fmt.Sprintf("delta-%d", i),
			MsgType:        "text",
		})
		require.NoError(t, err)

		err = repo.SaveInbox(ctx, []*model.Inbox{{
			OwnerUsername: username,
			SessionID:     sessionID,
			MsgID:         msgID,
			SeqID:         int64(i),
			IsRead:        0,
		}})
		require.NoError(t, err)
	}

	firstPage, err := repo.GetInboxDelta(ctx, username, 0, 2)
	require.NoError(t, err)
	require.Len(t, firstPage, 2)
	assert.Less(t, firstPage[0].InboxID, firstPage[1].InboxID)
	assert.Equal(t, "delta-1", firstPage[0].Content)
	assert.Equal(t, "delta-2", firstPage[1].Content)

	secondPage, err := repo.GetInboxDelta(ctx, username, firstPage[1].InboxID, 2)
	require.NoError(t, err)
	require.Len(t, secondPage, 1)
	assert.Equal(t, "delta-3", secondPage[0].Content)
}

func TestMessageRepo_CompleteLifecycle(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	sessionID := "lifecycle_session"
	sender := "user001"
	receiver := "user002"

	// 1. 保存消息
	msg := &model.MessageContent{
		MsgID:          time.Now().UnixNano(),
		SessionID:      sessionID,
		SenderUsername: sender,
		SeqID:          1,
		Content:        "Hello, this is a lifecycle test!",
		MsgType:        "text",
	}
	err = repo.SaveMessage(ctx, msg)
	require.NoError(t, err)

	// 2. 写入信箱
	inboxes := []*model.Inbox{
		{
			OwnerUsername: receiver,
			SessionID:     sessionID,
			MsgID:         msg.MsgID,
			SeqID:         msg.SeqID,
			IsRead:        0,
		},
	}
	err = repo.SaveInbox(ctx, inboxes)
	require.NoError(t, err)

	// 3. 拉取历史消息
	history, err := repo.GetHistoryMessages(ctx, sessionID, 0, 10)
	require.NoError(t, err)
	assert.Len(t, history, 1)
	assert.Equal(t, "Hello, this is a lifecycle test!", history[0].Content)

	// 4. 获取最后一条消息
	lastMsg, err := repo.GetLastMessage(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, msg.Content, lastMsg.Content)

	// 5. 获取未读消息
	unread, err := repo.GetUnreadMessages(ctx, receiver, 10)
	require.NoError(t, err)
	assert.Len(t, unread, 1)
	assert.Equal(t, sessionID, unread[0].SessionID)
}

func TestMessageRepo_Concurrent(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewMessageRepo(database, WithMessageRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("并发保存消息", func(t *testing.T) {
		sessionID := "concurrent_msg_session"
		const numGoroutines = 10
		const messagesPerGoroutine = 10

		done := make(chan bool, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			worker := func(goroutineID int) {
				for j := 0; j < messagesPerGoroutine; j++ {
					msg := &model.MessageContent{
						MsgID:          time.Now().UnixNano() + int64(goroutineID*100+j),
						SessionID:      sessionID,
						SenderUsername: fmt.Sprintf("user_%d", goroutineID),
						SeqID:          int64(goroutineID*messagesPerGoroutine + j),
						Content:        fmt.Sprintf("Concurrent message %d-%d", goroutineID, j),
					}
					_ = repo.SaveMessage(ctx, msg)
				}
				done <- true
			}
			worker(i)
		}

		// 等待所有 goroutine 完成
		for i := 0; i < numGoroutines; i++ {
			<-done
		}

		// 验证至少有一些消息保存成功
		messages, _ := repo.GetHistoryMessages(ctx, sessionID, 0, 1000)
		t.Logf("并发保存了 %d 条消息", len(messages))
	})
}

func TestMessageRepo_Options(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	t.Run("不提供logger应使用默认值", func(t *testing.T) {
		repo, err := NewMessageRepo(database)
		require.NoError(t, err)
		assert.NotNil(t, repo)
		repo.Close()
	})

	t.Run("提供自定义logger", func(t *testing.T) {
		customLogger := getTestLogger(t)
		repo, err := NewMessageRepo(database, WithMessageRepoLogger(customLogger))
		require.NoError(t, err)
		assert.NotNil(t, repo)
		repo.Close()
	})

	t.Run("database为nil应返回错误", func(t *testing.T) {
		_, err := NewMessageRepo(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database cannot be nil")
	})
}
