package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/resonance/internal/model"
	"github.com/joho/godotenv"
)

var (
	globalDB        db.DB
	globalDBOnce    sync.Once
	globalLogger    clog.Logger
	envLoaded       bool
	envLoadedOnce   sync.Once
	globalMysqlConn connector.MySQLConnector // 保存连接引用以便稍后关闭
	globalRedisConn connector.RedisConnector // Redis 连接管理
	globalRedisOnce sync.Once                // Redis 连接单例
)

// loadTestEnv 加载测试环境变量
func loadTestEnv() {
	envLoadedOnce.Do(func() {
		// 尝试加载项目根目录的 .env 文件
		projectRoot := filepath.Join("..", "..")
		envFile := filepath.Join(projectRoot, ".env")

		// 如果 .env 存在则加载
		if _, err := os.Stat(envFile); err == nil {
			_ = godotenv.Load(envFile)
		}
		envLoaded = true
	})
}

// getEnvOrDefault 获取环境变量，如果不存在则返回默认值
func getEnvOrDefault(key, defaultValue string) string {
	loadTestEnv() // 确保环境变量已加载
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvIntOrDefault 获取环境变量并转换为 int，如果不存在或转换失败则返回默认值
func getEnvIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var intValue int
		if _, err := fmt.Sscanf(value, "%d", &intValue); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// setupTestRedis 初始化全局测试 Redis 连接
// 使用 sync.Once 确保只创建一次
func setupTestRedis(t *testing.T) connector.RedisConnector {
	globalRedisOnce.Do(func() {
		redisConfig := &connector.RedisConfig{
			Name:            "test-redis",
			ConnectTimeout:  5 * time.Second,
			Addr:            getEnvOrDefault("REDIS_ADDR", "127.0.0.1:6379"),
			Password:        getEnvOrDefault("REDIS_PASSWORD", ""),
			DB:              getEnvIntOrDefault("REDIS_DB", 1),
			PoolSize:        20, // 用户确认：提升连接池以支持并发测试
			MinIdleConns:    10,
			DialTimeout:     5 * time.Second,
			ReadTimeout:     3 * time.Second,
			WriteTimeout:    3 * time.Second,
			HealthCheckFreq: 30 * time.Second,
		}

		var err error
		globalRedisConn, err = connector.NewRedis(redisConfig, connector.WithLogger(globalLogger))
		if err != nil {
			t.Logf("创建 Redis 连接器失败: %v", err)
			return
		}

		ctx := context.Background()
		if err := globalRedisConn.Connect(ctx); err != nil {
			t.Logf("连接 Redis 失败: %v", err)
			globalRedisConn = nil
			return
		}

		t.Log("✓ 全局 Redis 连接初始化成功 (DB=1, PoolSize=20)")
	})

	if globalRedisConn == nil {
		t.Skip("Redis 连接不可用，跳过测试")
		return nil
	}

	return globalRedisConn
}

// getTestRedis 获取 Redis 连接的便捷函数
func getTestRedis(t *testing.T) connector.RedisConnector {
	conn := setupTestRedis(t)
	if conn == nil {
		t.Skip("Redis 连接不可用，跳过测试")
	}
	return conn
}

// autoMigrateTables 自动迁移表结构
func autoMigrateTables(ctx context.Context) error {
	if globalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	gormDB := globalDB.DB(ctx)

	// 导入 model 包，使用 GORM 的 AutoMigrate 自动创建表
	// 注意：需要在测试文件中导入 model 包
	err := gormDB.AutoMigrate(
		&model.User{},
		&model.Session{},
		&model.SessionMember{},
		&model.MessageContent{},
		&model.Inbox{},
	)

	if err != nil {
		return fmt.Errorf("auto migrate failed: %w", err)
	}

	return nil
}

// setupTestDB 初始化全局测试数据库连接
// 使用 sync.Once 确保只创建一次
func setupTestDB(t *testing.T) db.DB {
	globalDBOnce.Do(func() {
		var err error

		// 初始化日志记录器
		globalLogger, err = clog.New(&clog.Config{
			Level:  "debug",
			Format: "console",
			Output: "stdout",
		}, clog.WithNamespace("test"))
		if err != nil {
			t.Fatalf("初始化日志记录器失败: %v", err)
		}

		// 创建 MySQL 连接器
		// 优先使用 root 用户进行测试，因为测试环境需要完整的数据库权限
		username := getEnvOrDefault("MYSQL_USER", "root")
		password := getEnvOrDefault("MYSQL_PASSWORD", "")

		// 如果指定了 MYSQL_ROOT_PASSWORD，则使用 root 用户
		if rootPassword := getEnvOrDefault("MYSQL_ROOT_PASSWORD", ""); rootPassword != "" {
			username = "root"
			password = rootPassword
		}

		mysqlConn, err := connector.NewMySQL(&connector.MySQLConfig{
			Name:            "test-mysql",
			ConnectTimeout:  5 * time.Second,
			Host:            getEnvOrDefault("MYSQL_HOST", "127.0.0.1"),
			Port:            getEnvIntOrDefault("MYSQL_PORT", 3306),
			Username:        username,
			Password:        password,
			Database:        getEnvOrDefault("MYSQL_DATABASE", "resonance"),
			Charset:         "utf8mb4",
			MaxIdleConns:    10, // 5 -> 10
			MaxOpenConns:    20, // 10 -> 20（用户确认：提升连接池以支持并发测试）
			ConnMaxLifetime: 1 * time.Hour,
		}, connector.WithLogger(globalLogger))
		if err != nil {
			t.Skipf("创建 MySQL 连接器失败: %v", err)
			return
		}

		// 建立连接
		ctx := context.Background()
		if err := mysqlConn.Connect(ctx); err != nil {
			t.Skipf("连接 MySQL 失败: %v", err)
			return
		}

		// 创建 DB 组件
		globalDB, err = db.New(&db.Config{
			Driver:         "mysql",
			EnableSharding: false, // 测试环境不启用分片
		}, db.WithMySQLConnector(mysqlConn), db.WithLogger(globalLogger))
		if err != nil {
			t.Fatalf("创建 DB 组件失败: %v", err)
		}

		// 自动迁移表结构
		migrateCtx := context.Background()
		if err := autoMigrateTables(migrateCtx); err != nil {
			t.Logf("警告：自动迁移表结构失败: %v", err)
			// 不跳过测试，因为表可能已经存在
		} else {
			t.Log("✓ 自动迁移表结构成功")
		}

		// 保存连接引用，稍后在测试包级别关闭
		globalMysqlConn = mysqlConn
	})

	// 如果 globalDB 仍然为 nil（连接失败），跳过测试
	if globalDB == nil {
		t.Skip("数据库连接不可用，跳过测试")
		return nil
	}

	return globalDB
}

// getTestLogger 获取测试用的日志记录器
func getTestLogger(t *testing.T) clog.Logger {
	if globalLogger == nil {
		var err error
		globalLogger, err = clog.New(&clog.Config{
			Level:  "debug",
			Format: "console",
			Output: "stdout",
		}, clog.WithNamespace("test"))
		if err != nil {
			t.Fatalf("初始化日志记录器失败: %v", err)
		}
	}
	return globalLogger
}

// cleanupTestData 清理测试数据，为下一次测试做准备
// 注意：这个函数不删除表结构，只删除数据，并重置自增ID
func cleanupTestData(t *testing.T, database db.DB) {
	ctx := context.Background()
	gormDB := database.DB(ctx)

	// 按依赖关系倒序删除：inbox -> message_content -> session_member -> session -> user
	tables := []string{
		"t_inbox",
		"t_message_content",
		"t_session_member",
		"t_session",
		"t_user",
	}

	for _, table := range tables {
		// 删除数据
		if err := gormDB.Exec(fmt.Sprintf("DELETE FROM %s", table)).Error; err != nil {
			t.Logf("警告：清理表 %s 失败: %v", table, err)
		}
		// 重置自增ID
		resetAutoIncrement(t, database, table)
	}
}

// cleanupRedisData 清理 Redis 测试数据
func cleanupRedisData(t *testing.T, redisConn connector.RedisConnector) {
	if redisConn == nil {
		return
	}

	ctx := context.Background()
	client := redisConn.GetClient()

	// 清理 resonance 命名空间下的所有 key
	keys, err := client.Keys(ctx, "resonance:*").Result()
	if err != nil {
		t.Logf("警告：获取 Redis key 列表失败: %v", err)
		return
	}

	if len(keys) > 0 {
		if err := client.Del(ctx, keys...).Err(); err != nil {
			t.Logf("警告：清理 Redis 数据失败: %v", err)
		} else {
			t.Logf("✓ 清理了 %d 个 Redis key", len(keys))
		}
	}
}

// resetAutoIncrement 重置表的自增ID
func resetAutoIncrement(t *testing.T, database db.DB, tableName string) {
	ctx := context.Background()
	gormDB := database.DB(ctx)

	if err := gormDB.Exec(fmt.Sprintf("ALTER TABLE %s AUTO_INCREMENT = 1", tableName)).Error; err != nil {
		t.Logf("警告：重置表 %s 自增ID失败: %v", tableName, err)
	}
}

// setupTestContext 创建一个测试用的数据库上下文
// 返回 DB 实例和清理函数
func setupTestContext(t *testing.T) (db.DB, func()) {
	database := setupTestDB(t)

	// 如果 database 为 nil（连接失败），返回空清理函数
	if database == nil {
		return nil, func() {}
	}

	// 在测试开始前清理数据
	cleanupTestData(t, database)

	// 返回清理函数供测试结束后调用
	cleanupFunc := func() {
		cleanupTestData(t, database)
	}

	return database, cleanupFunc
}

// TestMain 是包级别的测试入口，用于管理全局资源
func TestMain(m *testing.M) {
	// 运行测试
	code := m.Run()

	// 清理全局资源（按相反顺序）
	if globalDB != nil {
		globalDB.Close()
		globalDB = nil
	}
	if globalMysqlConn != nil {
		globalMysqlConn.Close()
		globalMysqlConn = nil
	}
	if globalRedisConn != nil {
		globalRedisConn.Close()
		globalRedisConn = nil
	}

	// 退出
	os.Exit(code)
}
