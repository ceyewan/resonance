package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/gateway/pushserver"
	gwserver "github.com/ceyewan/resonance/gateway/server"
	gwws "github.com/ceyewan/resonance/gateway/transport/ws"
	logicserver "github.com/ceyewan/resonance/logic/server"
	logicservice "github.com/ceyewan/resonance/logic/service"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
	taskcfg "github.com/ceyewan/resonance/task/config"
	"github.com/ceyewan/resonance/task/consumer"
	"github.com/ceyewan/resonance/task/dispatcher"
	"github.com/ceyewan/resonance/task/pusher"
)

func TestMessageDelivery_GoldenPath(t *testing.T) {
	ctx := context.Background()
	logger := clog.Discard()
	infra := setupInfra(t, logger)
	require.NoError(t, infra.db.DB(ctx).AutoMigrate(model.AllModels()...))

	userRepo, err := repo.NewUserRepo(infra.db)
	require.NoError(t, err)
	sessionRepo, err := repo.NewSessionRepo(infra.db, repo.WithSessionRepoLogger(logger))
	require.NoError(t, err)
	messageRepo, err := repo.NewMessageRepo(infra.db, repo.WithMessageRepoLogger(logger))
	require.NoError(t, err)
	routerRepo, err := repo.NewRouterRepo(infra.redisConn, repo.WithLogger(logger))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = routerRepo.Close()
		_ = messageRepo.Close()
		_ = sessionRepo.Close()
		_ = userRepo.Close()
	})

	// Gateway Push 服务（用于接收 Task 推送并转发 WS）
	connMgr := gwws.NewManager(logger, nil, nil, nil)
	bobConn, bobWSClient, wsCleanup := newWSConnForTest(t, "bob")
	defer wsCleanup()
	require.NoError(t, connMgr.AddConnection("bob", bobConn))

	gatewayID := "gw-integration-1"
	gatewayAddr := mustFreeAddr(t)
	gatewayGRPC := gwserver.NewGRPCServer(gatewayAddr, logger, pushserver.NewService(connMgr, logger))
	go func() { _ = gatewayGRPC.Start() }()
	t.Cleanup(gatewayGRPC.Stop)

	gatewayClient := newGatewayClientWithRetry(t, gatewayAddr, gatewayID, logger)
	t.Cleanup(func() { _ = gatewayClient.Close() })
	pusherMgr := &singleGatewayPusherManager{
		gatewayID: gatewayID,
		client:    gatewayClient,
	}

	// Task（Consumer + Dispatcher）
	taskDispatcher := dispatcher.NewDispatcher(messageRepo, routerRepo, pusherMgr, logger)
	taskConsumer := consumer.NewConsumer(
		infra.mqClient,
		taskDispatcher.Handle,
		taskcfg.ConsumerConfig{
			Topic:         "resonance.chat.event.v1",
			QueueGroup:    "golden-message-delivery",
			WorkerCount:   1,
			MaxRetry:      1,
			RetryInterval: 1,
		},
		logger,
	)
	taskConsumer.SetName("storage")
	require.NoError(t, taskConsumer.Start())
	t.Cleanup(func() { _ = taskConsumer.Stop() })

	// Logic（Auth/Session/Chat）
	authenticator, err := auth.New(&auth.Config{
		SecretKey:      "golden-path-secret-key-with-32-plus",
		Issuer:         "resonance-integration",
		AccessTokenTTL: 24 * time.Hour,
	}, auth.WithLogger(logger))
	require.NoError(t, err)
	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 11})
	require.NoError(t, err)
	sessionIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 11})
	require.NoError(t, err)
	sequencer, err := idgen.NewSequencer(&idgen.SequencerConfig{
		Driver:    "redis",
		KeyPrefix: "it:golden:seq",
		Step:      1,
	}, idgen.WithRedisConnector(infra.redisConn), idgen.WithLogger(logger))
	require.NoError(t, err)

	authSvc := logicservice.NewAuthService(userRepo, sessionRepo, authenticator, logger)
	sessionSvc := logicservice.NewSessionService(sessionRepo, messageRepo, userRepo, sessionIDGen, msgIDGen, sequencer, infra.mqClient, logger)
	chatSvc := logicservice.NewChatService(sessionRepo, messageRepo, msgIDGen, sequencer, infra.mqClient, logger)
	presenceSvc := logicservice.NewPresenceService(routerRepo, logger)

	logicAddr := mustFreeAddr(t)
	logicGRPC := logicserver.NewGRPCServer(logicAddr, logger, authSvc, sessionSvc, chatSvc, presenceSvc)
	go func() { _ = logicGRPC.Start() }()
	t.Cleanup(logicGRPC.Stop)

	logicConn := dialWithRetry(t, logicAddr, 5*time.Second)
	t.Cleanup(func() { _ = logicConn.Close() })
	authClient := logicv1.NewAuthServiceClient(logicConn)
	sessionClient := logicv1.NewSessionServiceClient(logicConn)
	chatClient := logicv1.NewChatServiceClient(logicConn)

	require.NoError(t, callWithRetry(func() error {
		_, e := authClient.Register(ctx, &logicv1.RegisterRequest{Username: "alice", Password: "pass123", Nickname: "Alice"})
		return e
	}))
	require.NoError(t, callWithRetry(func() error {
		_, e := authClient.Register(ctx, &logicv1.RegisterRequest{Username: "bob", Password: "pass123", Nickname: "Bob"})
		return e
	}))

	// 路由表：bob 在线于 gatewayID
	require.NoError(t, routerRepo.SetUserGateway(ctx, &model.Router{
		Username:  "bob",
		GatewayID: gatewayID,
		RemoteIP:  "127.0.0.1",
		Timestamp: time.Now().UnixMilli(),
	}))

	aliceCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-username", "alice"))
	createResp, err := sessionClient.CreateSession(aliceCtx, &logicv1.CreateSessionRequest{
		Type:    commonv1.SessionType_SESSION_TYPE_DIRECT,
		Members: []string{"bob"},
	})
	require.NoError(t, err)

	sendResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: createResp.GetSessionId(),
		Payload: &logicv1.SendEventRequest_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content: "golden message",
			},
		},
	})
	require.NoError(t, err)
	require.NotZero(t, sendResp.GetEventId())

	// 断言 1：bob 实时收到 WS 推送（可能先收到系统消息，循环读取直到命中目标 event_id）
	var targetPacket *gatewayv1.WsPacket
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		require.NoError(t, bobWSClient.SetReadDeadline(time.Now().Add(800*time.Millisecond)))
		msgType, data, e := bobWSClient.ReadMessage()
		if e != nil {
			if ne, ok := e.(net.Error); ok && ne.Timeout() {
				continue
			}
			require.NoError(t, e)
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		packet, e := gwws.DecodePacket(data)
		require.NoError(t, e)
		if packet.GetEvent().GetEventId() == sendResp.GetEventId() {
			targetPacket = packet
			break
		}
	}
	require.NotNil(t, targetPacket, "应在时限内收到目标消息事件")
	require.Equal(t, "golden message", targetPacket.GetEvent().GetMessage().GetContent())
	require.Equal(t, sendResp.GetEventId(), targetPacket.GetEvent().GetEventId())

	// 断言 2：Inbox 落库
	require.Eventually(t, func() bool {
		items, e := messageRepo.GetInboxDelta(ctx, "bob", 0, 20)
		if e != nil {
			return false
		}
		for _, item := range items {
			if item.EventID == sendResp.GetEventId() {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond)
}

type singleGatewayPusherManager struct {
	gatewayID string
	client    *pusher.GatewayClient
}

func (m *singleGatewayPusherManager) Start() error { return nil }
func (m *singleGatewayPusherManager) Close()       {}
func (m *singleGatewayPusherManager) GetClient(gatewayID string) (*pusher.GatewayClient, error) {
	if gatewayID != m.gatewayID {
		return nil, fmt.Errorf("gateway client not found: %s", gatewayID)
	}
	return m.client, nil
}

func newGatewayClientWithRetry(t *testing.T, addr, gatewayID string, logger clog.Logger) *pusher.GatewayClient {
	t.Helper()
	var c *pusher.GatewayClient
	var err error
	require.Eventually(t, func() bool {
		c, err = pusher.NewClient(addr, gatewayID, 128, 1, logger)
		return err == nil
	}, 5*time.Second, 100*time.Millisecond)
	return c
}

func newWSConnForTest(t *testing.T, username string) (*gwws.Conn, *websocket.Conn, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	var serverConn *websocket.Conn
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConn = c
	}))

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return serverConn != nil }, 2*time.Second, 20*time.Millisecond)

	conn := gwws.NewConn(
		username,
		"trace-test",
		serverConn,
		clog.Discard(),
		gwws.NewDefaultHandler(clog.Discard(), nil, nil, nil),
		1024*1024,
		30*time.Second,
		60*time.Second,
	)
	conn.Run()
	return conn, clientConn, func() {
		_ = conn.Close()
		_ = clientConn.Close()
		srv.Close()
	}
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
		Name:            "golden-postgres",
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
	var connectErr error
	for range 10 {
		connectErr = pgConn.Connect(context.Background())
		if connectErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, connectErr)

	redisConn, err := connector.NewRedis(&connector.RedisConfig{
		Name:         "golden-redis",
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
		Name:          "golden-nats",
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
	dbInstance, err := db.New(&db.Config{Driver: "postgresql"}, db.WithPostgreSQLConnector(pgConn), db.WithLogger(logger))
	require.NoError(t, err)

	out := &infra{
		postgresConn: pgConn,
		redisConn:    redisConn,
		natsConn:     natsConn,
		mqClient:     mqClient,
		db:           dbInstance,
		containers:   []testcontainers.Container{pgContainer, redisContainer, natsContainer},
	}
	t.Cleanup(func() {
		_ = out.db.Close()
		_ = out.mqClient.Close()
		_ = out.natsConn.Close()
		_ = out.redisConn.Close()
		_ = out.postgresConn.Close()
		for _, c := range out.containers {
			_ = c.Terminate(context.Background())
		}
	})
	return out
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

func callWithRetry(fn func() error) error {
	deadline := time.Now().Add(3 * time.Second)
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
