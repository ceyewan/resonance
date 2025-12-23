package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/resonance/im-sdk/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRouterRepo_BasicOperations 测试基本的 CRUD 操作
func TestRouterRepo_BasicOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	redisConn := getTestRedis(t)
	logger := getTestLogger(t)

	routerRepo, err := NewRouterRepo(redisConn, WithLogger(logger))
	require.NoError(t, err)
	defer routerRepo.Close()
	defer cleanupRedisData(t, redisConn) // 清理数据

	ctx := context.Background()

	// 测试数据
	testRouter := &model.Router{
		Username:  "testuser123",
		GatewayID: "gateway-001",
		RemoteIP:  "192.168.1.100",
		Timestamp: time.Now().Unix(),
	}

	// 1. 测试设置用户网关映射
	t.Run("SetUserGateway", func(t *testing.T) {
		err := routerRepo.SetUserGateway(ctx, testRouter)
		assert.NoError(t, err)
	})

	// 2. 测试获取用户网关映射
	t.Run("GetUserGateway", func(t *testing.T) {
		retrievedRouter, err := routerRepo.GetUserGateway(ctx, testRouter.Username)
		assert.NoError(t, err)
		assert.NotNil(t, retrievedRouter)
		assert.Equal(t, testRouter.Username, retrievedRouter.Username)
		assert.Equal(t, testRouter.GatewayID, retrievedRouter.GatewayID)
		assert.Equal(t, testRouter.RemoteIP, retrievedRouter.RemoteIP)
		assert.Equal(t, testRouter.Timestamp, retrievedRouter.Timestamp)
	})

	// 3. 测试更新用户网关映射
	t.Run("UpdateUserGateway", func(t *testing.T) {
		// 更新网关 ID 和时间戳
		updatedRouter := &model.Router{
			Username:  testRouter.Username,
			GatewayID: "gateway-002", // 更换网关
			RemoteIP:  "192.168.1.101", // 更换 IP
			Timestamp: time.Now().Unix(),
		}

		err := routerRepo.SetUserGateway(ctx, updatedRouter)
		assert.NoError(t, err)

		// 验证更新成功
		retrievedRouter, err := routerRepo.GetUserGateway(ctx, testRouter.Username)
		assert.NoError(t, err)
		assert.Equal(t, "gateway-002", retrievedRouter.GatewayID)
		assert.Equal(t, "192.168.1.101", retrievedRouter.RemoteIP)
	})

	// 4. 测试批量获取用户网关映射
	t.Run("BatchGetUsersGateway", func(t *testing.T) {
		// 创建多个测试用户
		testUsers := []string{"user1", "user2", "user3"}
		for i, username := range testUsers {
			router := &model.Router{
				Username:  username,
				GatewayID: fmt.Sprintf("gateway-%03d", i+1),
				RemoteIP:  fmt.Sprintf("192.168.1.%d", i+100),
				Timestamp: time.Now().Unix(),
			}
			err := routerRepo.SetUserGateway(ctx, router)
			assert.NoError(t, err)
		}

		// 批量获取
		usernames := append(testUsers, testRouter.Username) // 包含之前创建的用户
		routers, err := routerRepo.BatchGetUsersGateway(ctx, usernames)
		assert.NoError(t, err)
		assert.Len(t, routers, 4) // 应该返回 4 个用户

		// 验证返回的路由信息
		routerMap := make(map[string]*model.Router)
		for _, router := range routers {
			routerMap[router.Username] = router
		}

		for _, username := range usernames {
			assert.Contains(t, routerMap, username)
			assert.NotEmpty(t, routerMap[username].GatewayID)
		}
	})

	// 5. 测试删除用户网关映射
	t.Run("DeleteUserGateway", func(t *testing.T) {
		err := routerRepo.DeleteUserGateway(ctx, testRouter.Username)
		assert.NoError(t, err)

		// 验证删除成功
		_, err = routerRepo.GetUserGateway(ctx, testRouter.Username)
		assert.Error(t, err) // 应该返回错误，因为用户已被删除
	})

	// 6. 测试获取不存在的用户
	t.Run("GetNonExistentUser", func(t *testing.T) {
		_, err := routerRepo.GetUserGateway(ctx, "nonexistentuser")
		assert.Error(t, err)
	})
}

// TestRouterRepo_ErrorHandling 测试错误处理
func TestRouterRepo_ErrorHandling(t *testing.T) {
	// 创建测试用的 logger（用于错误处理测试）
	logger, err := clog.New(&clog.Config{
		Level:  "debug",
		Format: "console",
		Output: "stdout",
	})
	require.NoError(t, err)

	// 创建一个无效的 Redis 连接器配置
	redisConfig := &connector.RedisConfig{
		BaseConfig: connector.BaseConfig{Name: "invalid-redis"}, // 必须设置 Name
		Addr:       "invalid-host:6379",
		Password:   "",
		DB:         0,
	}
	redisConfig.SetDefaults() // 设置默认值

	redisConn, err := connector.NewRedis(redisConfig, connector.WithLogger(logger))
	require.NoError(t, err)

	// 不连接 Redis，测试错误处理
	routerRepo, err := NewRouterRepo(redisConn)
	require.NoError(t, err)
	defer routerRepo.Close()

	ctx := context.Background()
	testRouter := &model.Router{
		Username:  "testuser",
		GatewayID: "gateway-001",
		RemoteIP:  "192.168.1.100",
		Timestamp: time.Now().Unix(),
	}

	// 测试设置失败
	t.Run("SetUserGateway_Failure", func(t *testing.T) {
		err := routerRepo.SetUserGateway(ctx, testRouter)
		assert.Error(t, err)
	})

	// 测试获取失败
	t.Run("GetUserGateway_Failure", func(t *testing.T) {
		_, err := routerRepo.GetUserGateway(ctx, testRouter.Username)
		assert.Error(t, err)
	})
}

// TestRouterRepo_Validation 测试输入验证
func TestRouterRepo_Validation(t *testing.T) {
	redisConn := getTestRedis(t)
	logger := getTestLogger(t)

	routerRepo, err := NewRouterRepo(redisConn, WithLogger(logger))
	require.NoError(t, err)
	defer routerRepo.Close()
	defer cleanupRedisData(t, redisConn)

	ctx := context.Background()

	// 测试空用户名
	t.Run("EmptyUsername", func(t *testing.T) {
		// 设置空用户名的路由
		err := routerRepo.SetUserGateway(ctx, &model.Router{
			Username:  "",
			GatewayID: "gateway-001",
			RemoteIP:  "192.168.1.100",
			Timestamp: time.Now().Unix(),
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")

		// 获取空用户名的路由
		_, err = routerRepo.GetUserGateway(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")

		// 删除空用户名的路由
		err = routerRepo.DeleteUserGateway(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "username cannot be empty")
	})

	// 测试 nil 路由
	t.Run("NilRouter", func(t *testing.T) {
		err := routerRepo.SetUserGateway(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "router cannot be nil")
	})

	// 测试批量获取空列表
	t.Run("BatchGetEmptyList", func(t *testing.T) {
		routers, err := routerRepo.BatchGetUsersGateway(ctx, []string{})
		assert.NoError(t, err)
		assert.Empty(t, routers)
	})
}

// TestRouterRepo_Concurrency 测试并发操作
func TestRouterRepo_Concurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrency test in short mode")
	}

	redisConn := getTestRedis(t)
	logger := getTestLogger(t)

	routerRepo, err := NewRouterRepo(redisConn, WithLogger(logger))
	require.NoError(t, err)
	defer routerRepo.Close()
	defer cleanupRedisData(t, redisConn)

	ctx := context.Background()

	// 并发测试参数
	const numGoroutines = 10
	const numOperations = 100

	// 并发写入测试
	t.Run("ConcurrentWrites", func(t *testing.T) {
		done := make(chan bool, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func(id int) {
				defer func() { done <- true }()

				for j := 0; j < numOperations; j++ {
					username := fmt.Sprintf("user_%d_%d", id, j)
					router := &model.Router{
						Username:  username,
						GatewayID: fmt.Sprintf("gateway-%d", id),
						RemoteIP:  fmt.Sprintf("192.168.%d.%d", id/256, id%256),
						Timestamp: time.Now().Unix(),
					}

					err := routerRepo.SetUserGateway(ctx, router)
					assert.NoError(t, err)
				}
			}(i)
		}

		// 等待所有 goroutine 完成
		for i := 0; i < numGoroutines; i++ {
			<-done
		}
	})

	// 并发读取测试
	t.Run("ConcurrentReads", func(t *testing.T) {
		// 先创建一个测试用户
		testRouter := &model.Router{
			Username:  "concurrent_test_user",
			GatewayID: "gateway-concurrent",
			RemoteIP:  "192.168.100.100",
			Timestamp: time.Now().Unix(),
		}
		err := routerRepo.SetUserGateway(ctx, testRouter)
		require.NoError(t, err)

		done := make(chan bool, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer func() { done <- true }()

				for j := 0; j < numOperations; j++ {
					router, err := routerRepo.GetUserGateway(ctx, "concurrent_test_user")
					assert.NoError(t, err)
					assert.NotNil(t, router)
					assert.Equal(t, testRouter.Username, router.Username)
				}
			}()
		}

		// 等待所有 goroutine 完成
		for i := 0; i < numGoroutines; i++ {
			<-done
		}
	})
}