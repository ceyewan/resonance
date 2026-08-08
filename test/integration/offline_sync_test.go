package integration

import (
	"context"
	"testing"
	"time"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	logicserver "github.com/ceyewan/resonance/logic/server"
	logicservice "github.com/ceyewan/resonance/logic/service"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
	taskcfg "github.com/ceyewan/resonance/task/config"
	"github.com/ceyewan/resonance/task/consumer"
	"github.com/ceyewan/resonance/task/dispatcher"
	"github.com/ceyewan/resonance/task/pusher"
)

func TestOfflineSync_GoldenPath(t *testing.T) {
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

	// Task：仅消费并写 Inbox，push 使用空 manager（bob 离线）
	taskDispatcher := dispatcher.NewDispatcher(messageRepo, routerRepo, &noopPusherManager{}, logger)
	taskConsumer := consumer.NewConsumer(
		infra.mqClient,
		taskDispatcher.Handle,
		taskcfg.ConsumerConfig{
			Topic:         "resonance.chat.event.v1",
			QueueGroup:    "golden-offline-sync",
			WorkerCount:   1,
			MaxRetry:      1,
			RetryInterval: 1,
		},
		logger,
	)
	taskConsumer.SetName("storage")
	require.NoError(t, taskConsumer.Start())
	t.Cleanup(func() { _ = taskConsumer.Stop() })

	// Logic
	authenticator, err := auth.New(&auth.Config{
		SecretKey:      "offline-sync-secret-key-with-32-plus",
		Issuer:         "resonance-integration",
		AccessTokenTTL: 24 * time.Hour,
	}, auth.WithLogger(logger))
	require.NoError(t, err)
	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 21})
	require.NoError(t, err)
	sessionIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 21})
	require.NoError(t, err)
	sequencer, err := idgen.NewSequencer(&idgen.SequencerConfig{
		Driver:    "redis",
		KeyPrefix: "it:offline:seq",
		Step:      1,
	}, idgen.WithRedisConnector(infra.redisConn), idgen.WithLogger(logger))
	require.NoError(t, err)

	identityRepo, err := repo.NewIdentityRepo(infra.db)
	require.NoError(t, err)
	authSvc := logicservice.NewAuthService(userRepo, identityRepo, sessionRepo, authenticator, logger)
	sessionSvc := logicservice.NewSessionService(sessionRepo, messageRepo, userRepo, sessionIDGen, msgIDGen, sequencer, infra.mqClient, logger, logicservice.WithTenantMembershipReader(identityRepo), logicservice.WithLegacyGlobalSessionAuthorizationForTests())
	chatSvc := logicservice.NewChatService(sessionRepo, messageRepo, msgIDGen, sequencer, infra.mqClient, logger, logicservice.WithLegacyGlobalChatAuthorizationForTests())
	presenceSvc := logicservice.NewPresenceService(routerRepo, logger)

	logicAddr := mustFreeAddr(t)
	logicGRPC := logicserver.NewGRPCServer(logicAddr, logger, authSvc, sessionSvc, chatSvc, presenceSvc, logicserver.WithLegacyUsernameAuthForTests())
	go func() { _ = logicGRPC.Start() }()
	t.Cleanup(logicGRPC.Stop)

	logicConn := dialWithRetry(t, logicAddr)
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

	aliceCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-username", "alice"))
	bobCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-username", "bob"))

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
				Content: "offline delta message",
			},
		},
	})
	require.NoError(t, err)

	// 等待 Task 落 Inbox
	require.Eventually(t, func() bool {
		items, e := messageRepo.GetInboxDelta(ctx, "bob", 0, 50)
		if e != nil {
			return false
		}
		for _, it := range items {
			if it.EventID == sendResp.GetEventId() {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond)

	// bob 重连后拉离线增量
	deltaResp, err := sessionClient.PullInboxDelta(bobCtx, &logicv1.PullInboxDeltaRequest{
		CursorId: 0,
		Limit:    50,
	})
	require.NoError(t, err)
	require.NotZero(t, deltaResp.GetNextCursorId())
	require.False(t, deltaResp.GetHasMore())

	found := false
	for _, ev := range deltaResp.GetEvents() {
		if ev.GetEvent().GetEventId() == sendResp.GetEventId() {
			found = true
			require.Equal(t, "offline delta message", ev.GetEvent().GetMessage().GetContent())
			break
		}
	}
	require.True(t, found, "离线增量中应包含刚发送的消息")
}

type noopPusherManager struct{}

func (m *noopPusherManager) Start() error { return nil }
func (m *noopPusherManager) Close()       {}
func (m *noopPusherManager) GetClient(gatewayID string) (pusher.Client, error) {
	return nil, context.Canceled
}
