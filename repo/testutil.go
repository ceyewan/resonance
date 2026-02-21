package repo

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/resonance/model"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	globalDB      db.DB
	globalDBOnce  sync.Once
	globalLogger  clog.Logger
	globalLogOnce sync.Once

	globalPostgresConn connector.PostgreSQLConnector
	globalRedisConn    connector.RedisConnector
	globalDBInitErr    error

	postgresContainer testcontainers.Container
	redisContainer    testcontainers.Container

	postgresOnce sync.Once
	redisOnce    sync.Once

	postgresStartErr error
	redisStartErr    error
)

func getTestLogger(t *testing.T) clog.Logger {
	globalLogOnce.Do(func() {
		globalLogger = clog.Discard()
	})

	if globalLogger == nil {
		t.Fatalf("测试日志初始化失败")
	}
	return globalLogger
}

func startPostgresContainer() (string, int, error) {
	postgresOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				postgresStartErr = fmt.Errorf("启动 PostgreSQL Testcontainer panic: %v", r)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		req := testcontainers.ContainerRequest{
			Image:        "postgres:17-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_DB":       "resonance_test",
				"POSTGRES_USER":     "resonance",
				"POSTGRES_PASSWORD": "resonance123",
			},
			WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(90 * time.Second),
		}

		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			postgresStartErr = fmt.Errorf("启动 PostgreSQL Testcontainer 失败: %w", err)
			return
		}
		postgresContainer = container
	})
	if postgresStartErr != nil {
		return "", 0, postgresStartErr
	}

	ctx := context.Background()
	host, err := postgresContainer.Host(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("获取 PostgreSQL 容器 host 失败: %w", err)
	}
	mappedPort, err := postgresContainer.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return "", 0, fmt.Errorf("获取 PostgreSQL 映射端口失败: %w", err)
	}
	port, err := strconv.Atoi(mappedPort.Port())
	if err != nil {
		return "", 0, fmt.Errorf("解析 PostgreSQL 端口失败: %w", err)
	}

	return host, port, nil
}

func startRedisContainer() (string, int, error) {
	redisOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				redisStartErr = fmt.Errorf("启动 Redis Testcontainer panic: %v", r)
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		req := testcontainers.ContainerRequest{
			Image:        "redis:7.2-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second),
		}

		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			redisStartErr = fmt.Errorf("启动 Redis Testcontainer 失败: %w", err)
			return
		}
		redisContainer = container
	})
	if redisStartErr != nil {
		return "", 0, redisStartErr
	}

	ctx := context.Background()
	host, err := redisContainer.Host(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("获取 Redis 容器 host 失败: %w", err)
	}
	mappedPort, err := redisContainer.MappedPort(ctx, "6379/tcp")
	if err != nil {
		return "", 0, fmt.Errorf("获取 Redis 映射端口失败: %w", err)
	}
	port, err := strconv.Atoi(mappedPort.Port())
	if err != nil {
		return "", 0, fmt.Errorf("解析 Redis 端口失败: %w", err)
	}

	return host, port, nil
}

func connectWithRetry(fn func() error, maxAttempts int, interval time.Duration) error {
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(interval)
	}
	return lastErr
}

func setupTestRedis(t *testing.T) connector.RedisConnector {
	if globalRedisConn != nil {
		return globalRedisConn
	}

	host, port, err := startRedisContainer()
	if err != nil {
		t.Skipf("跳过测试：%v", err)
		return nil
	}
	logger := getTestLogger(t)

	redisConfig := &connector.RedisConfig{
		Name:         "test-redis",
		Addr:         fmt.Sprintf("%s:%d", host, port),
		Password:     "",
		DB:           1,
		PoolSize:     20,
		MinIdleConns: 10,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}

	globalRedisConn, err = connector.NewRedis(redisConfig, connector.WithLogger(logger))
	if err != nil {
		t.Fatalf("创建 Redis 连接器失败: %v", err)
	}

	if err := connectWithRetry(func() error {
		return globalRedisConn.Connect(context.Background())
	}, 20, 500*time.Millisecond); err != nil {
		t.Fatalf("连接 Redis 失败: %v", err)
	}

	return globalRedisConn
}

func getTestRedis(t *testing.T) connector.RedisConnector {
	conn := setupTestRedis(t)
	if conn == nil {
		t.Fatalf("Redis 连接初始化失败")
	}
	return conn
}

func autoMigrateTables(ctx context.Context) error {
	if globalDB == nil {
		return fmt.Errorf("database not initialized")
	}

	gormDB := globalDB.DB(ctx)
	if err := gormDB.AutoMigrate(model.AllModels()...); err != nil {
		return fmt.Errorf("auto migrate failed: %w", err)
	}
	return nil
}

func setupTestDB(t *testing.T) db.DB {
	globalDBOnce.Do(func() {
		host, port, err := startPostgresContainer()
		if err != nil {
			globalDBInitErr = err
			return
		}
		logger := getTestLogger(t)

		postgresCfg := &connector.PostgreSQLConfig{
			Name:            "test-postgres",
			Host:            host,
			Port:            port,
			Username:        "resonance",
			Password:        "resonance123",
			Database:        "resonance_test",
			SSLMode:         "disable",
			MaxIdleConns:    10,
			MaxOpenConns:    20,
			ConnMaxLifetime: time.Hour,
			ConnectTimeout:  5 * time.Second,
			Timezone:        "UTC",
		}

		globalPostgresConn, err = connector.NewPostgreSQL(postgresCfg, connector.WithLogger(logger))
		if err != nil {
			globalDBInitErr = fmt.Errorf("创建 PostgreSQL 连接器失败: %w", err)
			return
		}

		if err := connectWithRetry(func() error {
			return globalPostgresConn.Connect(context.Background())
		}, 20, 500*time.Millisecond); err != nil {
			globalDBInitErr = fmt.Errorf("连接 PostgreSQL 失败: %w", err)
			return
		}

		globalDB, err = db.New(&db.Config{
			Driver:         "postgresql",
			EnableSharding: false,
		}, db.WithPostgreSQLConnector(globalPostgresConn), db.WithLogger(logger))
		if err != nil {
			globalDBInitErr = fmt.Errorf("创建 DB 组件失败: %w", err)
			return
		}

		if err := autoMigrateTables(context.Background()); err != nil {
			globalDBInitErr = fmt.Errorf("自动迁移表结构失败: %w", err)
			_ = globalDB.Close()
			globalDB = nil
			return
		}
	})

	if globalDBInitErr != nil {
		if strings.Contains(globalDBInitErr.Error(), "docker.sock") || strings.Contains(globalDBInitErr.Error(), "rootless Docker not found") {
			t.Skipf("跳过测试：%v", globalDBInitErr)
			return nil
		}
		t.Fatalf("数据库连接初始化失败: %v", globalDBInitErr)
	}
	if globalDB == nil {
		t.Fatalf("数据库连接初始化失败")
	}
	return globalDB
}

func cleanupTestData(t *testing.T, database db.DB) {
	ctx := context.Background()
	gormDB := database.DB(ctx)

	tables := []string{
		"t_inbox",
		"t_message_outbox",
		"t_message_content",
		"t_session_member",
		"t_session",
		"t_user",
	}

	for _, table := range tables {
		stmt := fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE", table)
		if err := gormDB.Exec(stmt).Error; err != nil {
			if strings.Contains(err.Error(), "does not exist") {
				continue
			}
			t.Logf("警告：清理表 %s 失败: %v", table, err)
		}
	}
}

func cleanupRedisData(t *testing.T, redisConn connector.RedisConnector) {
	if redisConn == nil {
		return
	}

	ctx := context.Background()
	client := redisConn.GetClient()
	keys, err := client.Keys(ctx, "resonance:*").Result()
	if err != nil {
		t.Logf("警告：获取 Redis key 列表失败: %v", err)
		return
	}

	if len(keys) > 0 {
		if err := client.Del(ctx, keys...).Err(); err != nil {
			t.Logf("警告：清理 Redis 数据失败: %v", err)
		}
	}
}

func setupTestContext(t *testing.T) (db.DB, func()) {
	database := setupTestDB(t)
	cleanupTestData(t, database)
	cleanupFunc := func() {
		cleanupTestData(t, database)
	}
	return database, cleanupFunc
}

func TestMain(m *testing.M) {
	code := m.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if globalDB != nil {
		_ = globalDB.Close()
		globalDB = nil
	}
	if globalPostgresConn != nil {
		_ = globalPostgresConn.Close()
		globalPostgresConn = nil
	}
	if globalRedisConn != nil {
		_ = globalRedisConn.Close()
		globalRedisConn = nil
	}
	if postgresContainer != nil {
		_ = postgresContainer.Terminate(ctx)
		postgresContainer = nil
	}
	if redisContainer != nil {
		_ = redisContainer.Terminate(ctx)
		redisContainer = nil
	}

	os.Exit(code)
}
