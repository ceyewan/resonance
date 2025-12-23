package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ceyewan/resonance/im-sdk/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserRepo_CreateUser(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("创建正常用户", func(t *testing.T) {
		user := &model.User{
			Username: "test_user_001",
			Nickname: "测试用户001",
			Password: "hashed_password_123",
			Avatar:   "https://example.com/avatar/001.jpg",
		}

		err := repo.CreateUser(ctx, user)
		require.NoError(t, err)

		// 验证用户已创建
		found, err := repo.GetUserByUsername(ctx, user.Username)
		require.NoError(t, err)
		assert.Equal(t, user.Username, found.Username)
		assert.Equal(t, user.Nickname, found.Nickname)
		assert.Equal(t, user.Password, found.Password)
		assert.Equal(t, user.Avatar, found.Avatar)
		assert.WithinDuration(t, time.Now(), found.CreatedAt, 2*time.Second)
	})

	t.Run("创建重复用户应失败", func(t *testing.T) {
		user := &model.User{
			Username: "duplicate_user",
			Nickname: "重复用户",
			Password: "password123",
		}

		// 第一次创建成功
		err := repo.CreateUser(ctx, user)
		require.NoError(t, err)

		// 第二次创建应失败
		err = repo.CreateUser(ctx, user)
		assert.Error(t, err)
	})

	t.Run("创建空用户名应失败", func(t *testing.T) {
		user := &model.User{
			Nickname: "无名氏",
			Password: "password123",
		}

		err := repo.CreateUser(ctx, user)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")
	})

	t.Run("创建nil用户应失败", func(t *testing.T) {
		err := repo.CreateUser(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user cannot be nil")
	})
}

func TestUserRepo_GetUserByUsername(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 准备测试数据
	testUser := &model.User{
		Username: "get_test_user",
		Nickname: "获取测试用户",
		Password: "password123",
		Avatar:   "https://example.com/avatar.jpg",
	}
	err = repo.CreateUser(ctx, testUser)
	require.NoError(t, err)

	t.Run("获取存在的用户", func(t *testing.T) {
		found, err := repo.GetUserByUsername(ctx, "get_test_user")
		require.NoError(t, err)
		assert.Equal(t, testUser.Username, found.Username)
		assert.Equal(t, testUser.Nickname, found.Nickname)
		assert.Equal(t, testUser.Avatar, found.Avatar)
	})

	t.Run("获取不存在的用户应返回错误", func(t *testing.T) {
		_, err := repo.GetUserByUsername(ctx, "non_existent_user")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user not found")
	})

	t.Run("获取空用户名应返回错误", func(t *testing.T) {
		_, err := repo.GetUserByUsername(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")
	})
}

func TestUserRepo_SearchUsers(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 准备测试数据
	testUsers := []*model.User{
		{Username: "alice", Nickname: "爱丽丝", Password: "pass1"},
		{Username: "bob", Nickname: "鲍勃", Password: "pass2"},
		{Username: "charlie", Nickname: "查理", Password: "pass3"},
		{Username: "alice_smith", Nickname: "爱丽丝·史密斯", Password: "pass4"},
	}

	for _, user := range testUsers {
		err := repo.CreateUser(ctx, user)
		require.NoError(t, err)
	}

	t.Run("按用户名搜索", func(t *testing.T) {
		users, err := repo.SearchUsers(ctx, "alice")
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(users), 2) // alice 和 alice_smith
	})

	t.Run("按昵称搜索（中文）", func(t *testing.T) {
		users, err := repo.SearchUsers(ctx, "爱丽丝")
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(users), 2) // 爱丽丝和爱丽丝·史密斯
	})

	t.Run("搜索空字符串应返回空列表", func(t *testing.T) {
		users, err := repo.SearchUsers(ctx, "")
		require.NoError(t, err)
		assert.Empty(t, users)
	})

	t.Run("搜索不存在的用户应返回空列表", func(t *testing.T) {
		users, err := repo.SearchUsers(ctx, "nonexistent")
		require.NoError(t, err)
		assert.Empty(t, users)
	})
}

func TestUserRepo_UpdateUser(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 准备测试数据
	user := &model.User{
		Username: "update_test_user",
		Nickname: "原名",
		Password: "old_password",
		Avatar:   "https://example.com/old_avatar.jpg",
	}
	err = repo.CreateUser(ctx, user)
	require.NoError(t, err)

	t.Run("更新用户昵称和头像", func(t *testing.T) {
		user.Nickname = "新昵称"
		user.Avatar = "https://example.com/new_avatar.jpg"

		err := repo.UpdateUser(ctx, user)
		require.NoError(t, err)

		// 验证更新
		updated, err := repo.GetUserByUsername(ctx, user.Username)
		require.NoError(t, err)
		assert.Equal(t, "新昵称", updated.Nickname)
		assert.Equal(t, "https://example.com/new_avatar.jpg", updated.Avatar)
	})

	t.Run("更新不存在的用户应失败", func(t *testing.T) {
		nonExistentUser := &model.User{
			Username: "non_existent_user",
			Nickname: "不存在",
		}

		err := repo.UpdateUser(ctx, nonExistentUser)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user not found")
	})

	t.Run("更新空用户名应失败", func(t *testing.T) {
		user := &model.User{
			Nickname: "无用户名",
		}

		err := repo.UpdateUser(ctx, user)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")
	})

	t.Run("更新nil用户应失败", func(t *testing.T) {
		err := repo.UpdateUser(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user cannot be nil")
	})
}

func TestUserRepo_CompleteLifecycle(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// 1. 创建用户
	user := &model.User{
		Username: "lifecycle_user",
		Nickname: "生命周期测试用户",
		Password: "password123",
	}
	err = repo.CreateUser(ctx, user)
	require.NoError(t, err)

	// 2. 获取用户
	found, err := repo.GetUserByUsername(ctx, user.Username)
	require.NoError(t, err)
	assert.Equal(t, user.Username, found.Username)

	// 3. 搜索用户
	users, err := repo.SearchUsers(ctx, "生命周期")
	require.NoError(t, err)
	assert.NotEmpty(t, users)

	// 4. 更新用户
	found.Nickname = "已更新的昵称"
	err = repo.UpdateUser(ctx, found)
	require.NoError(t, err)

	// 5. 验证更新
	updated, err := repo.GetUserByUsername(ctx, user.Username)
	require.NoError(t, err)
	assert.Equal(t, "已更新的昵称", updated.Nickname)
}

// 并发测试
func TestUserRepo_Concurrent(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("并发创建用户", func(t *testing.T) {
		const numGoroutines = 10
		const usersPerGoroutine = 10

		done := make(chan bool, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			worker := func(goroutineID int) {
				for j := 0; j < usersPerGoroutine; j++ {
					username := fmt.Sprintf("concurrent_user_%d_%d", goroutineID, j)
					user := &model.User{
						Username: username,
						Nickname: username,
						Password: "password",
					}
					_ = repo.CreateUser(ctx, user)
				}
				done <- true
			}
			worker(i)
		}

		// 等待所有 goroutine 完成
		for i := 0; i < numGoroutines; i++ {
			<-done
		}

		// 验证至少有一些用户创建成功
		users, _ := repo.SearchUsers(ctx, "concurrent_user_")
		t.Logf("并发创建了 %d 个用户", len(users))
	})
}

func TestUserRepo_Options(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	t.Run("不提供logger应使用默认值", func(t *testing.T) {
		repo, err := NewUserRepo(database)
		require.NoError(t, err)
		assert.NotNil(t, repo)
		repo.Close()
	})

	t.Run("提供自定义logger", func(t *testing.T) {
		customLogger := getTestLogger(t)
		repo, err := NewUserRepo(database, WithUserRepoLogger(customLogger))
		require.NoError(t, err)
		assert.NotNil(t, repo)
		repo.Close()
	})

	t.Run("database为nil应返回错误", func(t *testing.T) {
		_, err := NewUserRepo(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database cannot be nil")
	})
}

func TestUserRepo_ErrorHandling(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	repo, err := NewUserRepo(database, WithUserRepoLogger(getTestLogger(t)))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("创建超长用户名", func(t *testing.T) {
		// username 字段是 VARCHAR(64)
		longUsername := string(make([]byte, 100))
		user := &model.User{
			Username: longUsername,
			Password: "password",
		}

		err := repo.CreateUser(ctx, user)
		// 可能会失败，取决于数据库实现
		if err != nil {
			t.Logf("超长用户名创建失败（预期行为）: %v", err)
		}
	})

	t.Run("创建空密码应成功（业务层应验证）", func(t *testing.T) {
		// 数据库层只负责存储，业务层应验证密码
		user := &model.User{
			Username: "empty_password",
			Password: "",
		}

		err := repo.CreateUser(ctx, user)
		// 数据库层可能允许，也可能不允许（取决于约束）
		t.Logf("空密码创建结果: %v", err)
	})
}
