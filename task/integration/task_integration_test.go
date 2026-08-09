package integration

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/genesis/mq"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
	"github.com/ceyewan/resonance/task/config"
	"github.com/ceyewan/resonance/task/consumer"
	"github.com/ceyewan/resonance/task/dispatcher"
	"github.com/ceyewan/resonance/task/pusher"
	"github.com/ceyewan/resonance/task/streaming"
)

func TestTaskIntegration_ConsumeEvent_PersistInbox(t *testing.T) {
	ctx := context.Background()
	logger := clog.Discard()
	infra := setupInfra(t, logger)
	dbInstance := infra.db
	require.NoError(t, dbInstance.DB(ctx).AutoMigrate(model.AllModels()...))

	messageRepo, err := repo.NewMessageRepo(dbInstance, repo.WithMessageRepoLogger(logger))
	require.NoError(t, err)
	routerRepo, err := repo.NewRouterRepo(infra.redisConn, repo.WithLogger(logger))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = routerRepo.Close()
		_ = messageRepo.Close()
	})

	require.NoError(t, routerRepo.SetUserGateway(ctx, &model.Router{
		Username:  "bob",
		GatewayID: "gw-1",
		RemoteIP:  "127.0.0.1",
		Timestamp: time.Now().UnixMilli(),
	}))

	pushMgr := &fakePusherManager{}
	d := dispatcher.NewDispatcher(messageRepo, routerRepo, pushMgr, logger)
	c := consumer.NewConsumer(
		infra.mqClient,
		d.Handle,
		config.ConsumerConfig{
			Topic:         "resonance.chat.event.v1",
			QueueGroup:    "task-it-group",
			WorkerCount:   1,
			MaxRetry:      1,
			RetryInterval: 1,
		},
		logger,
	)
	c.SetName("storage")
	require.NoError(t, c.Start())
	t.Cleanup(func() {
		_ = c.Stop()
	})

	ev := &mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:      9001,
			SeqId:        11,
			SessionId:    "s_task_it_1",
			FromUsername: "alice",
			TimestampMs:  time.Now().UnixMilli(),
			Payload: &commonv1.ChatEvent_Message{
				Message: &commonv1.Message{
					Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
					Content: "task integration message",
				},
			},
		},
		TargetUsernames: []string{"alice", "bob"},
	}
	raw, err := proto.Marshal(ev)
	require.NoError(t, err)
	require.NoError(t, infra.mqClient.Publish(ctx, "resonance.chat.event.v1", raw))

	require.Eventually(t, func() bool {
		items, e := messageRepo.GetInboxDelta(ctx, "bob", 0, 20)
		if e != nil || len(items) == 0 {
			return false
		}
		for _, it := range items {
			if it.EventID == 9001 {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond)

	inboxes, err := messageRepo.GetInboxDelta(ctx, "bob", 0, 20)
	require.NoError(t, err)
	found := false
	for _, item := range inboxes {
		if item.EventID != 9001 {
			continue
		}
		found = true
		require.Equal(t, int64(11), item.SeqID)
		require.Equal(t, model.InboxEventTypeMessage, item.EventType)
		decoded := &commonv1.ChatEvent{}
		require.NoError(t, proto.Unmarshal(item.Payload, decoded))
		require.Equal(t, "task integration message", decoded.GetMessage().GetContent())
	}
	require.True(t, found, "bob 的 inbox 中应包含 event_id=9001")
	require.True(t, pushMgr.getClientCalled, "应尝试走在线推送路径（即使 client 不存在）")

	var inboxCountBefore int64
	require.NoError(t, dbInstance.DB(ctx).Model(&model.Inbox{}).Count(&inboxCountBefore).Error)
	streamClient := &capturePushClient{}
	streamPushers := &capturePusherManager{client: streamClient}
	streamDispatcher, err := streaming.NewDispatcher(routerRepo, streamPushers, logger)
	require.NoError(t, err)
	streamConsumer, err := streaming.NewConsumer(infra.mqClient, streamDispatcher.Handle, config.ConsumerConfig{
		Topic: "resonance.agent.stream.v1", QueueGroup: "task-it-agent-stream",
		DLQTopic: "resonance.agent.stream.v1.dlq", WorkerCount: 1, MaxRetry: 1,
	}, 64<<10, logger)
	require.NoError(t, err)
	require.NoError(t, streamConsumer.Start())
	t.Cleanup(func() { _ = streamConsumer.Stop() })

	streamEvent := &mqv1.AgentStreamEvent{
		TenantId: "default", RunId: "run-it-1", StreamId: "run-it-1", SessionId: "s_task_it_1",
		FromUsername: "resonance-agent", TargetUsernames: []string{"bob"}, Sequence: 1,
		SourceEventId: 9001, FinalClientMsgId: "agent:run-it-1:final",
		Payload: &mqv1.AgentStreamEvent_Chunk{Chunk: &mqv1.AgentStreamChunk{Delta: "ephemeral"}},
	}
	streamRaw, err := proto.Marshal(streamEvent)
	require.NoError(t, err)
	require.NoError(t, infra.mqClient.Publish(ctx, "resonance.agent.stream.v1", streamRaw))
	require.Eventually(t, func() bool { return streamClient.count() == 1 }, 5*time.Second, 50*time.Millisecond)
	task := streamClient.first()
	require.Equal(t, "ephemeral", task.Stream.GetStreamChunk().GetDelta())
	require.Equal(t, uint64(1), task.Stream.GetStreamChunk().GetStreamSequence())
	var inboxCountAfter int64
	require.NoError(t, dbInstance.DB(ctx).Model(&model.Inbox{}).Count(&inboxCountAfter).Error)
	require.Equal(t, inboxCountBefore, inboxCountAfter, "Agent Stream must never write Inbox")
}

type fakePusherManager struct {
	getClientCalled bool
}

type capturePusherManager struct{ client pusher.Client }

func (m *capturePusherManager) Start() error { return nil }
func (m *capturePusherManager) Close()       {}
func (m *capturePusherManager) GetClient(string) (pusher.Client, error) {
	return m.client, nil
}

type capturePushClient struct {
	mu    sync.Mutex
	tasks []*pusher.PushTask
}

func (c *capturePushClient) Enqueue(task *pusher.PushTask) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tasks = append(c.tasks, task)
	return nil
}
func (c *capturePushClient) QueueSize() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tasks)
}
func (c *capturePushClient) count() int { return c.QueueSize() }
func (c *capturePushClient) first() *pusher.PushTask {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.tasks) == 0 {
		return nil
	}
	return c.tasks[0]
}

func (m *fakePusherManager) Start() error { return nil }
func (m *fakePusherManager) Close()       {}
func (m *fakePusherManager) GetClient(gatewayID string) (pusher.Client, error) {
	m.getClientCalled = true
	return nil, fmt.Errorf("gateway client not found: %s", gatewayID)
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
		// The image starts a temporary PostgreSQL during initialization, then
		// restarts it. Waiting for the port alone can race with that restart.
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(90 * time.Second),
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
		Name:            "task-it-postgres",
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
		Name:         "task-it-redis",
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
		Name:          "task-it-nats",
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
