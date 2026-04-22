package integration

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/logic/server"
	"github.com/ceyewan/resonance/logic/service"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func TestLogicIntegration_SendEvent_WritesMessageAndOutbox(t *testing.T) {
	ctx := context.Background()
	logger := clog.Discard()

	infra := setupInfra(t, logger)
	dbInstance := infra.db
	gormDB := dbInstance.DB(ctx)
	require.NoError(t, gormDB.AutoMigrate(model.AllModels()...))

	userRepo, err := repo.NewUserRepo(dbInstance)
	require.NoError(t, err)
	sessionRepo, err := repo.NewSessionRepo(dbInstance, repo.WithSessionRepoLogger(logger))
	require.NoError(t, err)
	messageRepo, err := repo.NewMessageRepo(dbInstance, repo.WithMessageRepoLogger(logger))
	require.NoError(t, err)
	routerRepo, err := repo.NewRouterRepo(infra.redisConn, repo.WithLogger(logger))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = routerRepo.Close()
		_ = messageRepo.Close()
		_ = sessionRepo.Close()
		_ = userRepo.Close()
	})

	authenticator, err := auth.New(&auth.Config{
		SecretKey:      "integration-secret-key-32-chars-ok",
		Issuer:         "resonance-integration",
		AccessTokenTTL: 24 * time.Hour,
	}, auth.WithLogger(logger))
	require.NoError(t, err)

	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 1})
	require.NoError(t, err)
	sessionIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 1})
	require.NoError(t, err)
	sequencer, err := idgen.NewSequencer(&idgen.SequencerConfig{
		Driver:    "redis",
		KeyPrefix: "it:logic:seq",
		Step:      1,
	}, idgen.WithRedisConnector(infra.redisConn), idgen.WithLogger(logger))
	require.NoError(t, err)

	authSvc := service.NewAuthService(userRepo, sessionRepo, authenticator, logger)
	sessionSvc := service.NewSessionService(sessionRepo, messageRepo, userRepo, sessionIDGen, msgIDGen, sequencer, infra.mqClient, logger)
	chatSvc := service.NewChatService(sessionRepo, messageRepo, msgIDGen, sequencer, infra.mqClient, logger)
	presenceSvc := service.NewPresenceService(routerRepo, logger)

	addr := mustFreeAddr(t)
	grpcServer := server.NewGRPCServer(addr, logger, authSvc, sessionSvc, chatSvc, presenceSvc)
	go func() {
		_ = grpcServer.Start()
	}()
	t.Cleanup(grpcServer.Stop)

	conn := dialWithRetry(t, addr, 5*time.Second)
	t.Cleanup(func() { _ = conn.Close() })
	authClient := logicv1.NewAuthServiceClient(conn)
	sessionClient := logicv1.NewSessionServiceClient(conn)
	chatClient := logicv1.NewChatServiceClient(conn)

	require.NoError(t, callWithRetry(3*time.Second, func() error {
		_, e := authClient.Register(ctx, &logicv1.RegisterRequest{
			Username: "alice",
			Password: "pass123",
			Nickname: "Alice",
		})
		return e
	}))
	require.NoError(t, callWithRetry(3*time.Second, func() error {
		_, e := authClient.Register(ctx, &logicv1.RegisterRequest{
			Username: "bob",
			Password: "pass123",
			Nickname: "Bob",
		})
		return e
	}))

	aliceCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-username", "alice"))
	createResp, err := sessionClient.CreateSession(aliceCtx, &logicv1.CreateSessionRequest{
		Type:    commonv1.SessionType_SESSION_TYPE_DIRECT,
		Members: []string{"bob"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, createResp.GetSessionId())

	sendResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: createResp.GetSessionId(),
		Payload: &logicv1.SendEventRequest_Message{
			Message: &commonv1.Message{
				Type:        commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content:     "hello from integration",
				ClientMsgId: "it-msg-1",
			},
		},
	})
	require.NoError(t, err)
	require.NotZero(t, sendResp.GetEventId())
	require.NotZero(t, sendResp.GetSeqId())

	history, err := messageRepo.GetHistoryMessages(ctx, createResp.GetSessionId(), 0, 50)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	last := history[len(history)-1]
	require.Equal(t, sendResp.GetEventId(), last.EventID)
	require.Equal(t, sendResp.GetSeqId(), last.SeqID)
	require.Equal(t, "alice", last.SenderUsername)
	require.Equal(t, "hello from integration", last.Content)

	pending, err := messageRepo.GetPendingOutboxMessages(ctx, 50)
	require.NoError(t, err)
	require.NotEmpty(t, pending)
	found := false
	for _, item := range pending {
		if item.EventID == sendResp.GetEventId() {
			found = true
			break
		}
	}
	require.True(t, found, "outbox 中应包含本次发送事件")
}

type infra struct {
	postgresConn connector.PostgreSQLConnector
	redisConn    connector.RedisConnector
	natsConn     connector.NATSConnector
	mqClient     mq.MQ
	db           db.DB
	containers   []testcontainers.Container
}

func setupInfra(t *testing.T, logger clog.Logger) *infra {
	t.Helper()

	pgContainer, pgHost, pgPort := mustStartContainer(t, testcontainers.ContainerRequest{
		Image:        "postgres:17-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "resonance_test",
			"POSTGRES_USER":     "resonance",
			"POSTGRES_PASSWORD": "resonance123",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(90 * time.Second),
	}, "5432/tcp")
	redisContainer, redisHost, redisPort := mustStartContainer(t, testcontainers.ContainerRequest{
		Image:        "redis:7.2-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second),
	}, "6379/tcp")
	natsContainer, natsHost, natsPort := mustStartContainer(t, testcontainers.ContainerRequest{
		Image:        "nats:2.10-alpine",
		Cmd:          []string{"-js"},
		ExposedPorts: []string{"4222/tcp"},
		WaitingFor:   wait.ForListeningPort("4222/tcp").WithStartupTimeout(60 * time.Second),
	}, "4222/tcp")

	pgConn, err := connector.NewPostgreSQL(&connector.PostgreSQLConfig{
		Name:            "logic-it-postgres",
		Host:            pgHost,
		Port:            pgPort,
		Username:        "resonance",
		Password:        "resonance123",
		Database:        "resonance_test",
		SSLMode:         "disable",
		ConnectTimeout:  5 * time.Second,
		MaxIdleConns:    10,
		MaxOpenConns:    20,
		ConnMaxLifetime: time.Hour,
		Timezone:        "UTC",
	}, connector.WithLogger(logger))
	require.NoError(t, err)
	require.NoError(t, pgConn.Connect(context.Background()))

	redisConn, err := connector.NewRedis(&connector.RedisConfig{
		Name:         "logic-it-redis",
		Addr:         fmt.Sprintf("%s:%d", redisHost, redisPort),
		DB:           0,
		PoolSize:     20,
		MinIdleConns: 5,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}, connector.WithLogger(logger))
	require.NoError(t, err)
	require.NoError(t, redisConn.Connect(context.Background()))

	natsConn, err := connector.NewNATS(&connector.NATSConfig{
		Name:          "logic-it-nats",
		URL:           fmt.Sprintf("nats://%s:%d", natsHost, natsPort),
		MaxReconnects: 10,
		ReconnectWait: time.Second,
	}, connector.WithLogger(logger))
	require.NoError(t, err)
	require.NoError(t, natsConn.Connect(context.Background()))

	mqClient, err := mq.New(&mq.Config{
		Driver:    mq.DriverNATSJetStream,
		JetStream: &mq.JetStreamConfig{AutoCreateStream: true},
	}, mq.WithNATSConnector(natsConn), mq.WithLogger(logger))
	require.NoError(t, err)

	dbInstance, err := db.New(&db.Config{
		Driver: "postgresql",
	}, db.WithPostgreSQLConnector(pgConn), db.WithLogger(logger))
	require.NoError(t, err)

	infra := &infra{
		postgresConn: pgConn,
		redisConn:    redisConn,
		natsConn:     natsConn,
		mqClient:     mqClient,
		db:           dbInstance,
		containers:   []testcontainers.Container{pgContainer, redisContainer, natsContainer},
	}

	t.Cleanup(func() {
		_ = infra.db.Close()
		_ = infra.mqClient.Close()
		_ = infra.natsConn.Close()
		_ = infra.redisConn.Close()
		_ = infra.postgresConn.Close()
		for _, c := range infra.containers {
			_ = c.Terminate(context.Background())
		}
	})

	return infra
}

func mustStartContainer(t *testing.T, req testcontainers.ContainerRequest, exposedPort string) (testcontainers.Container, string, int) {
	t.Helper()
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if isDockerUnavailable(err) {
			t.Skipf("跳过集成测试：Docker 不可用: %v", err)
		}
		require.NoError(t, err)
	}
	host, err := container.Host(ctx)
	require.NoError(t, err)
	mappedPort, err := container.MappedPort(ctx, nat.Port(exposedPort))
	require.NoError(t, err)
	port, err := strconv.Atoi(mappedPort.Port())
	require.NoError(t, err)
	return container, host, port
}

func isDockerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "docker.sock") ||
		strings.Contains(msg, "docker is not running") ||
		strings.Contains(msg, "cannot connect to the docker daemon") ||
		strings.Contains(msg, "rootless docker not found")
}

func mustFreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func dialWithRetry(t *testing.T, addr string, timeout time.Duration) *grpc.ClientConn {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		conn.Connect()
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		state := conn.GetState()
		if state == connectivity.Ready || state == connectivity.Idle || conn.WaitForStateChange(ctx, state) {
			cancel()
			return conn
		}
		cancel()
		lastErr = fmt.Errorf("grpc not ready, state=%s", conn.GetState())
		_ = conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	return nil
}

func callWithRetry(timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return lastErr
}
