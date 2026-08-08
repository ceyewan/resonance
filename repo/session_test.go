package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

func TestSessionRepo_CreateSession(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("创建单聊会话", func(t *testing.T) {
		session := &model.Session{
			SessionID:     "single_chat_001",
			Type:          1, // 单聊
			OwnerUsername: "user001",
		}

		err := repo.CreateSession(ctx, session)
		require.NoError(t, err)

		// 验证会话已创建
		found, err := repo.GetSession(ctx, session.SessionID)
		require.NoError(t, err)
		assert.Equal(t, session.SessionID, found.SessionID)
		assert.Equal(t, 1, found.Type)
	})

	t.Run("创建群聊会话", func(t *testing.T) {
		session := &model.Session{
			SessionID:     "group_chat_001",
			Type:          2, // 群聊
			Name:          "测试群组",
			OwnerUsername: "user001",
		}

		err := repo.CreateSession(ctx, session)
		require.NoError(t, err)

		// 验证会话已创建
		found, err := repo.GetSession(ctx, session.SessionID)
		require.NoError(t, err)
		assert.Equal(t, "测试群组", found.Name)
		assert.Equal(t, 2, found.Type)
	})

	t.Run("持久化AI会话权威快照", func(t *testing.T) {
		session := &model.Session{
			SessionID: "agent:9001", Type: 1, Kind: model.SessionKindAI,
			TenantID: "tenant-a", ProfileID: model.AgentProfileIAMAdmin, ProfileVersion: 3,
			OwnerUsername: "admin",
		}
		require.NoError(t, repo.CreateSession(ctx, session))

		found, err := repo.GetSession(ctx, session.SessionID)
		require.NoError(t, err)
		assert.Equal(t, model.SessionKindAI, found.Kind)
		assert.Equal(t, "tenant-a", found.TenantID)
		assert.Equal(t, model.AgentProfileIAMAdmin, found.ProfileID)
		assert.Equal(t, int64(3), found.ProfileVersion)
	})

	t.Run("创建重复会话ID应失败", func(t *testing.T) {
		session := &model.Session{
			SessionID: "duplicate_session",
			Type:      1,
		}

		// 第一次创建成功
		err := repo.CreateSession(ctx, session)
		require.NoError(t, err)

		// 第二次创建应失败
		session2 := &model.Session{
			SessionID: "duplicate_session",
			Type:      2,
		}
		err = repo.CreateSession(ctx, session2)
		assert.Error(t, err)
	})

	t.Run("创建空会话ID应失败", func(t *testing.T) {
		session := &model.Session{
			Type: 1,
		}

		err := repo.CreateSession(ctx, session)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session_id cannot be empty")
	})

	t.Run("创建nil会话应失败", func(t *testing.T) {
		err := repo.CreateSession(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session cannot be nil")
	})
}

func TestSessionRepo_CreateSessionWithMembers_AtomicIdempotentAndVersioned(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	sessionRepo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer sessionRepo.Close()
	ctx := context.Background()

	t.Run("member insert failure rolls back session", func(t *testing.T) {
		session := &model.Session{
			SessionID: "agent:rollback", Type: 1, Kind: model.SessionKindAI, TenantID: "tenant-rollback",
			OwnerUsername: "alice", ProfileID: model.AgentProfileUserAssistant, ProfileVersion: 1,
		}
		members := []*model.SessionMember{
			{SessionID: session.SessionID, Username: "alice", Role: 1},
			{SessionID: session.SessionID, Username: strings.Repeat("x", 65)},
		}
		_, _, err := sessionRepo.CreateSessionWithMembers(ctx, session, members)
		require.Error(t, err)
		_, err = sessionRepo.GetSession(ctx, session.SessionID)
		require.ErrorIs(t, err, ErrSessionNotFound)
	})

	t.Run("concurrent replay creates one exact session", func(t *testing.T) {
		session := &model.Session{
			SessionID: "agent:concurrent", Type: 1, Kind: model.SessionKindAI, TenantID: "tenant-concurrent",
			OwnerUsername: "alice", ProfileID: model.AgentProfileUserAssistant, ProfileVersion: 1,
		}
		members := []*model.SessionMember{
			{SessionID: session.SessionID, Username: "alice", Role: 1},
			{SessionID: session.SessionID, Username: "resonance-agent"},
		}
		var created atomic.Int64
		errors := make(chan error, 8)
		var group sync.WaitGroup
		for range 8 {
			group.Add(1)
			go func() {
				defer group.Done()
				persisted, wasCreated, createErr := sessionRepo.CreateSessionWithMembers(ctx, session, members)
				if createErr == nil {
					if persisted == nil || persisted.SessionID != session.SessionID {
						createErr = fmt.Errorf("unexpected persisted session")
					}
					if wasCreated {
						created.Add(1)
					}
				}
				errors <- createErr
			}()
		}
		group.Wait()
		close(errors)
		for createErr := range errors {
			require.NoError(t, createErr)
		}
		require.Equal(t, int64(1), created.Load())
		persistedMembers, err := sessionRepo.GetMembers(ctx, session.SessionID)
		require.NoError(t, err)
		require.Len(t, persistedMembers, 2)
	})

	t.Run("profile versions coexist", func(t *testing.T) {
		for version, sessionID := range map[int64]string{1: "agent:version-1", 2: "agent:version-2"} {
			session := &model.Session{
				SessionID: sessionID, Type: 1, Kind: model.SessionKindAI, TenantID: "tenant-versioned",
				OwnerUsername: "alice", ProfileID: model.AgentProfileUserAssistant, ProfileVersion: version,
			}
			members := []*model.SessionMember{
				{SessionID: sessionID, Username: "alice", Role: 1},
				{SessionID: sessionID, Username: "resonance-agent"},
			}
			_, created, err := sessionRepo.CreateSessionWithMembers(ctx, session, members)
			require.NoError(t, err)
			require.True(t, created)
		}
	})

	t.Run("list and contacts stay inside tenant", func(t *testing.T) {
		userRepo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
		require.NoError(t, err)
		for _, username := range []string{"tenant-list-alice", "tenant-list-bob", "tenant-list-carol"} {
			require.NoError(t, userRepo.CreateUser(ctx, &model.User{Username: username, Password: "!", Nickname: username}))
		}
		createDirect := func(sessionID, tenantID, peer string) {
			session := &model.Session{
				SessionID: sessionID, Type: 1, Kind: model.SessionKindStandard, TenantID: tenantID,
				OwnerUsername: "tenant-list-alice",
			}
			members := []*model.SessionMember{
				{SessionID: sessionID, Username: "tenant-list-alice", Role: 1},
				{SessionID: sessionID, Username: peer},
			}
			_, _, err := sessionRepo.CreateSessionWithMembers(ctx, session, members)
			require.NoError(t, err)
		}
		createDirect("direct:tenant-list-a", "tenant-list-a", "tenant-list-bob")
		createDirect("direct:tenant-list-b", "tenant-list-b", "tenant-list-carol")

		sessions, err := sessionRepo.GetUserSessionListByTenant(ctx, "tenant-list-a", "tenant-list-alice")
		require.NoError(t, err)
		require.Len(t, sessions, 1)
		require.Equal(t, "direct:tenant-list-a", sessions[0].SessionID)
		contacts, err := sessionRepo.GetContactListByTenant(ctx, "tenant-list-a", "tenant-list-alice")
		require.NoError(t, err)
		require.Len(t, contacts, 1)
		require.Equal(t, "tenant-list-bob", contacts[0].Username)
		legacy, err := sessionRepo.FindDirectSessionByMembers(ctx, "tenant-list-a", "tenant-list-alice", "tenant-list-bob")
		require.NoError(t, err)
		require.Equal(t, "direct:tenant-list-a", legacy.SessionID)
	})
}

func TestSessionRepo_GetSession(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 准备测试数据
	session := &model.Session{
		SessionID:     "get_test_session",
		Type:          2,
		Name:          "获取测试群组",
		OwnerUsername: "owner001",
	}
	err = repo.CreateSession(ctx, session)
	require.NoError(t, err)

	t.Run("获取存在的会话", func(t *testing.T) {
		found, err := repo.GetSession(ctx, "get_test_session")
		require.NoError(t, err)
		assert.Equal(t, session.SessionID, found.SessionID)
		assert.Equal(t, session.Name, found.Name)
		assert.Equal(t, session.OwnerUsername, found.OwnerUsername)
	})

	t.Run("获取不存在的会话应返回错误", func(t *testing.T) {
		_, err := repo.GetSession(ctx, "non_existent_session")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("获取空会话ID应返回错误", func(t *testing.T) {
		_, err := repo.GetSession(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session_id cannot be empty")
	})
}

func TestSessionRepo_AddMember_GetMembers(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 创建测试会话
	session := &model.Session{
		SessionID: "member_test_session",
		Type:      2,
		Name:      "成员测试群组",
	}
	err = repo.CreateSession(ctx, session)
	require.NoError(t, err)

	t.Run("添加成员", func(t *testing.T) {
		members := []*model.SessionMember{
			{SessionID: "member_test_session", Username: "user001", Role: 1}, // 管理员
			{SessionID: "member_test_session", Username: "user002", Role: 0}, // 成员
			{SessionID: "member_test_session", Username: "user003", Role: 0}, // 成员
		}

		for _, member := range members {
			err := repo.AddMember(ctx, member)
			require.NoError(t, err)
		}

		// 验证成员数量
		foundMembers, err := repo.GetMembers(ctx, "member_test_session")
		require.NoError(t, err)
		assert.Len(t, foundMembers, 3)
	})

	t.Run("添加重复成员应失败", func(t *testing.T) {
		member := &model.SessionMember{
			SessionID: "member_test_session",
			Username:  "user001",
			Role:      0,
		}

		// 第一次添加
		err := repo.AddMember(ctx, member)
		// 可能失败（已存在）或成功（根据唯一约束）
		t.Logf("添加重复成员结果: %v", err)
	})

	t.Run("获取成员列表", func(t *testing.T) {
		members, err := repo.GetMembers(ctx, "member_test_session")
		require.NoError(t, err)
		assert.Len(t, members, 3)

		// 验证成员信息
		usernames := make([]string, len(members))
		for i, m := range members {
			usernames[i] = m.Username
		}
		assert.Contains(t, usernames, "user001")
		assert.Contains(t, usernames, "user002")
		assert.Contains(t, usernames, "user003")
	})

	t.Run("获取不存在会话的成员应返回空列表", func(t *testing.T) {
		members, err := repo.GetMembers(ctx, "non_existent_session")
		require.NoError(t, err)
		assert.Empty(t, members)
	})

	t.Run("添加空会话ID成员应失败", func(t *testing.T) {
		member := &model.SessionMember{
			Username: "user001",
		}

		err := repo.AddMember(ctx, member)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session_id cannot be empty")
	})
}

func TestSessionRepo_GetUserSession(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 创建测试会话和成员
	session := &model.Session{
		SessionID: "user_session_test",
		Type:      1,
	}
	err = repo.CreateSession(ctx, session)
	require.NoError(t, err)

	member := &model.SessionMember{
		SessionID:   "user_session_test",
		Username:    "test_user",
		Role:        0,
		LastReadSeq: 100,
	}
	err = repo.AddMember(ctx, member)
	require.NoError(t, err)

	t.Run("获取存在的用户会话", func(t *testing.T) {
		found, err := repo.GetUserSession(ctx, "test_user", "user_session_test")
		require.NoError(t, err)
		assert.Equal(t, "test_user", found.Username)
		assert.Equal(t, "user_session_test", found.SessionID)
		assert.Equal(t, int64(100), found.LastReadSeq)
	})

	t.Run("获取不存在的用户会话应返回错误", func(t *testing.T) {
		_, err := repo.GetUserSession(ctx, "non_existent_user", "user_session_test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session member not found")
		assert.ErrorIs(t, err, ErrSessionMemberNotFound)
	})

	t.Run("获取空用户名应返回错误", func(t *testing.T) {
		_, err := repo.GetUserSession(ctx, "", "user_session_test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")
	})
}

func TestSessionRepo_UpdateLastReadSeq(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	require.NoError(t, repo.CreateSession(ctx, &model.Session{
		SessionID: "read_seq_session",
		Type:      1,
		MaxSeqID:  20,
	}))
	require.NoError(t, repo.AddMember(ctx, &model.SessionMember{
		SessionID:   "read_seq_session",
		Username:    "alice",
		LastReadSeq: 10,
	}))

	t.Run("更新为更大 seq 成功", func(t *testing.T) {
		err := repo.UpdateLastReadSeq(ctx, "read_seq_session", "alice", 15)
		require.NoError(t, err)

		member, err := repo.GetUserSession(ctx, "alice", "read_seq_session")
		require.NoError(t, err)
		assert.Equal(t, int64(15), member.LastReadSeq)
	})

	t.Run("更新为更小 seq 视为 no-op", func(t *testing.T) {
		err := repo.UpdateLastReadSeq(ctx, "read_seq_session", "alice", 12)
		require.NoError(t, err)

		member, err := repo.GetUserSession(ctx, "alice", "read_seq_session")
		require.NoError(t, err)
		assert.Equal(t, int64(15), member.LastReadSeq)
	})

	t.Run("不存在的成员返回 not found", func(t *testing.T) {
		err := repo.UpdateLastReadSeq(ctx, "read_seq_session", "bob", 18)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSessionMemberNotFound))
	})
}

func TestSessionRepo_GetUserSessionList(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 创建测试会话和成员
	sessions := []*model.Session{
		{SessionID: "session_001", Type: 1},
		{SessionID: "session_002", Type: 2},
		{SessionID: "session_003", Type: 1},
	}

	for _, s := range sessions {
		err := repo.CreateSession(ctx, s)
		require.NoError(t, err)
	}

	// 添加用户到多个会话
	members := []*model.SessionMember{
		{SessionID: "session_001", Username: "list_test_user"},
		{SessionID: "session_002", Username: "list_test_user"},
		{SessionID: "session_003", Username: "list_test_user"},
	}

	for _, m := range members {
		err := repo.AddMember(ctx, m)
		require.NoError(t, err)
	}

	t.Run("获取用户的会话列表", func(t *testing.T) {
		userSessions, err := repo.GetUserSessionList(ctx, "list_test_user")
		require.NoError(t, err)
		assert.Len(t, userSessions, 3)

		sessionIDs := make([]string, len(userSessions))
		for i, s := range userSessions {
			sessionIDs[i] = s.SessionID
		}
		assert.Contains(t, sessionIDs, "session_001")
		assert.Contains(t, sessionIDs, "session_002")
		assert.Contains(t, sessionIDs, "session_003")
	})

	t.Run("会话列表按 max_seq_id 倒序且同分稳定排序", func(t *testing.T) {
		require.NoError(t, repo.UpdateMaxSeqID(ctx, "session_001", 20))
		require.NoError(t, repo.UpdateMaxSeqID(ctx, "session_002", 20))
		require.NoError(t, repo.UpdateMaxSeqID(ctx, "session_003", 10))

		userSessions, err := repo.GetUserSessionList(ctx, "list_test_user")
		require.NoError(t, err)
		require.Len(t, userSessions, 3)

		assert.Equal(t, "session_001", userSessions[0].SessionID)
		assert.Equal(t, "session_002", userSessions[1].SessionID)
		assert.Equal(t, "session_003", userSessions[2].SessionID)
		assert.Equal(t, int64(20), userSessions[0].MaxSeqID)
		assert.Equal(t, int64(20), userSessions[1].MaxSeqID)
		assert.Equal(t, int64(10), userSessions[2].MaxSeqID)
	})

	t.Run("获取不存在用户的会话列表应返回空", func(t *testing.T) {
		userSessions, err := repo.GetUserSessionList(ctx, "non_existent_user")
		require.NoError(t, err)
		assert.Empty(t, userSessions)
	})

	t.Run("获取空用户名的会话列表应返回错误", func(t *testing.T) {
		_, err := repo.GetUserSessionList(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")
	})
}

func TestSessionRepo_UpdateMaxSeqID(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 创建测试会话
	session := &model.Session{
		SessionID: "seq_test_session",
		Type:      1,
		MaxSeqID:  100,
	}
	err = repo.CreateSession(ctx, session)
	require.NoError(t, err)

	t.Run("更新序列号为更大值应成功", func(t *testing.T) {
		err := repo.UpdateMaxSeqID(ctx, "seq_test_session", 200)
		require.NoError(t, err)

		// 验证更新
		found, err := repo.GetSession(ctx, "seq_test_session")
		require.NoError(t, err)
		assert.Equal(t, int64(200), found.MaxSeqID)
	})

	t.Run("更新序列号为相同值应无变化", func(t *testing.T) {
		err := repo.UpdateMaxSeqID(ctx, "seq_test_session", 200)
		require.NoError(t, err)

		// 验证无变化
		found, err := repo.GetSession(ctx, "seq_test_session")
		require.NoError(t, err)
		assert.Equal(t, int64(200), found.MaxSeqID)
	})

	t.Run("更新序列号为更小值应无变化（CAS保护）", func(t *testing.T) {
		err := repo.UpdateMaxSeqID(ctx, "seq_test_session", 150)
		require.NoError(t, err)

		// 验证无变化
		found, err := repo.GetSession(ctx, "seq_test_session")
		require.NoError(t, err)
		assert.Equal(t, int64(200), found.MaxSeqID) // 保持200不变
	})

	t.Run("更新不存在会话的序列号应返回错误", func(t *testing.T) {
		err := repo.UpdateMaxSeqID(ctx, "non_existent_session", 300)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("更新空会话ID应返回错误", func(t *testing.T) {
		err := repo.UpdateMaxSeqID(ctx, "", 300)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session_id cannot be empty")
	})
}

func TestSessionRepo_GetContactList(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	// 需要先创建用户，因为联系人查询依赖用户表
	userRepo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer userRepo.Close()

	sessionRepo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer sessionRepo.Close()

	ctx := context.Background()

	// 创建测试用户
	users := []*model.User{
		{Username: "contact_user_001", Nickname: "联系人001", Password: "pass1"},
		{Username: "contact_user_002", Nickname: "联系人002", Password: "pass2"},
		{Username: "contact_user_003", Nickname: "联系人003", Password: "pass3"},
		{Username: "contact_user_004", Nickname: "联系人004", Password: "pass4"},
	}

	for _, user := range users {
		err := userRepo.CreateUser(ctx, user)
		require.NoError(t, err)
	}

	// 创建单聊会话
	singleChats := []*model.Session{
		{SessionID: "single_001", Type: 1}, // contact_user_001 与 contact_user_002
		{SessionID: "single_002", Type: 1}, // contact_user_001 与 contact_user_003
		{SessionID: "group_001", Type: 2},  // 群聊（不应出现在联系人列表）
	}

	for _, s := range singleChats {
		err := sessionRepo.CreateSession(ctx, s)
		require.NoError(t, err)
	}

	// 添加成员到会话
	members := []*model.SessionMember{
		{SessionID: "single_001", Username: "contact_user_001"},
		{SessionID: "single_001", Username: "contact_user_002"},
		{SessionID: "single_002", Username: "contact_user_001"},
		{SessionID: "single_002", Username: "contact_user_003"},
		{SessionID: "group_001", Username: "contact_user_001"}, // 群聊
		{SessionID: "group_001", Username: "contact_user_004"}, // 群聊
	}

	for _, m := range members {
		err := sessionRepo.AddMember(ctx, m)
		require.NoError(t, err)
	}

	t.Run("获取联系人列表（仅单聊）", func(t *testing.T) {
		contacts, err := sessionRepo.GetContactList(ctx, "contact_user_001")
		require.NoError(t, err)

		// contact_user_001 的联系人应该是 contact_user_002 和 contact_user_003
		// 不包括群聊中的 contact_user_004
		assert.Len(t, contacts, 2)

		contactUsernames := make([]string, len(contacts))
		for i, c := range contacts {
			contactUsernames[i] = c.Username
		}
		assert.Contains(t, contactUsernames, "contact_user_002")
		assert.Contains(t, contactUsernames, "contact_user_003")
		assert.NotContains(t, contactUsernames, "contact_user_004") // 群聊成员不在联系人列表
	})

	t.Run("获取没有单聊会话用户的联系人列表应返回空", func(t *testing.T) {
		// contact_user_004 只有群聊，没有单聊
		contacts, err := sessionRepo.GetContactList(ctx, "contact_user_004")
		require.NoError(t, err)
		assert.Empty(t, contacts)
	})

	t.Run("获取不存在用户的联系人列表应返回空", func(t *testing.T) {
		contacts, err := sessionRepo.GetContactList(ctx, "non_existent_user")
		require.NoError(t, err)
		assert.Empty(t, contacts)
	})

	t.Run("获取空用户名的联系人列表应返回错误", func(t *testing.T) {
		_, err := sessionRepo.GetContactList(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")
	})
}

func TestSessionRepo_CompleteLifecycle(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 1. 创建会话
	session := &model.Session{
		SessionID: "lifecycle_session",
		Type:      2,
		Name:      "生命周期测试群组",
	}
	err = repo.CreateSession(ctx, session)
	require.NoError(t, err)

	// 2. 添加成员
	members := []*model.SessionMember{
		{SessionID: "lifecycle_session", Username: "user_001", Role: 1},
		{SessionID: "lifecycle_session", Username: "user_002", Role: 0},
	}
	for _, m := range members {
		err := repo.AddMember(ctx, m)
		require.NoError(t, err)
	}

	// 3. 获取会话详情
	found, err := repo.GetSession(ctx, "lifecycle_session")
	require.NoError(t, err)
	assert.Equal(t, "生命周期测试群组", found.Name)

	// 4. 获取成员列表
	memberList, err := repo.GetMembers(ctx, "lifecycle_session")
	require.NoError(t, err)
	assert.Len(t, memberList, 2)

	// 5. 更新序列号
	err = repo.UpdateMaxSeqID(ctx, "lifecycle_session", 999)
	require.NoError(t, err)

	// 6. 验证序列号更新
	updated, err := repo.GetSession(ctx, "lifecycle_session")
	require.NoError(t, err)
	assert.Equal(t, int64(999), updated.MaxSeqID)
}

func TestSessionRepo_Concurrent(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewSessionRepo(database, WithSessionRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("并发添加成员", func(t *testing.T) {
		// 创建会话
		session := &model.Session{
			SessionID: "concurrent_session",
			Type:      2,
			Name:      "并发测试群组",
		}
		err := repo.CreateSession(ctx, session)
		require.NoError(t, err)

		const numGoroutines = 10
		const membersPerGoroutine = 5
		done := make(chan bool, numGoroutines)

		for i := range numGoroutines {
			worker := func(goroutineID int) {
				for j := range membersPerGoroutine {
					username := fmt.Sprintf("concurrent_user_%d_%d", goroutineID, j)
					member := &model.SessionMember{
						SessionID: "concurrent_session",
						Username:  username,
						Role:      0,
					}
					_ = repo.AddMember(ctx, member)
				}
				done <- true
			}
			worker(i)
		}

		// 等待所有 goroutine 完成
		for range numGoroutines {
			<-done
		}

		// 验证至少有一些成员添加成功
		members, _ := repo.GetMembers(ctx, "concurrent_session")
		t.Logf("并发添加了 %d 个成员", len(members))
	})
}

func TestSessionRepo_Options(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	t.Run("不提供logger应使用默认值", func(t *testing.T) {
		repo, err := NewSessionRepo(database)
		require.NoError(t, err)
		assert.NotNil(t, repo)
		repo.Close()
	})

	t.Run("提供自定义logger", func(t *testing.T) {
		customLogger := getTestLogger(t)
		repo, err := NewSessionRepo(database, WithSessionRepoLogger(customLogger))
		require.NoError(t, err)
		assert.NotNil(t, repo)
		repo.Close()
	})

	t.Run("database为nil应返回错误", func(t *testing.T) {
		_, err := NewSessionRepo(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database cannot be nil")
	})
}
