package integration

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

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

// TestMessageRecall_GoldenPath：alice 发消息 → alice 撤回 → bob 实时收到撤回推送，DB recalled_at 已设置，Inbox 含撤回事件。
func TestMessageRecall_GoldenPath(t *testing.T) {
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

	// Gateway Push 服务（bob 在线）
	connMgr := gwws.NewManager(logger, nil, nil, nil)
	bobConn, bobWSClient, wsCleanup := newWSConnForRecallTest(t, "bob")
	defer wsCleanup()
	require.NoError(t, connMgr.AddConnection("bob", bobConn))

	gatewayID := "gw-recall-1"
	gatewayAddr := mustFreeAddr(t)
	gatewayGRPC := gwserver.NewGRPCServer(gatewayAddr, logger, pushserver.NewService(connMgr, logger))
	go func() { _ = gatewayGRPC.Start() }()
	t.Cleanup(gatewayGRPC.Stop)

	gatewayClient := newGatewayClientWithRetry(t, gatewayAddr, gatewayID, logger)
	t.Cleanup(func() { _ = gatewayClient.Close() })
	pusherMgr := &recallSingleGatewayPusherManager{
		gatewayID: gatewayID,
		client:    gatewayClient,
	}

	// Task
	taskDispatcher := dispatcher.NewDispatcher(messageRepo, routerRepo, pusherMgr, logger)
	taskConsumer := consumer.NewConsumer(
		infra.mqClient,
		taskDispatcher.Handle,
		taskcfg.ConsumerConfig{
			Topic:         "resonance.chat.event.v1",
			QueueGroup:    "golden-recall",
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
		SecretKey:      "recall-golden-secret-key-with-32-plus",
		Issuer:         "resonance-integration",
		AccessTokenTTL: 24 * time.Hour,
	}, auth.WithLogger(logger))
	require.NoError(t, err)
	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 4})
	require.NoError(t, err)
	sessionIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 4})
	require.NoError(t, err)
	sequencer, err := idgen.NewSequencer(&idgen.SequencerConfig{
		Driver:    "redis",
		KeyPrefix: "it:recall:seq",
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
	sessionID := createResp.GetSessionId()

	// alice 发一条消息
	sendResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: sessionID,
		Payload: &logicv1.SendEventRequest_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content: "recall me",
			},
		},
	})
	require.NoError(t, err)
	msgEventID := sendResp.GetEventId()

	// 等待消息落 Inbox（确认消息已被 Task 处理）
	require.Eventually(t, func() bool {
		items, e := messageRepo.GetInboxDelta(ctx, "bob", 0, 20)
		if e != nil {
			return false
		}
		for _, it := range items {
			if it.EventID == msgEventID {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "消息应落入 bob 的 Inbox")

	// alice 撤回该消息
	recallResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: sessionID,
		Payload: &logicv1.SendEventRequest_Recall{
			Recall: &commonv1.MessageRecall{TargetEventId: msgEventID},
		},
	})
	require.NoError(t, err)
	require.NotZero(t, recallResp.GetEventId())
	require.NotZero(t, recallResp.GetSeqId())
	recallEventID := recallResp.GetEventId()

	// 断言 1：DB 中原消息的 recalled_at 已设置
	require.Eventually(t, func() bool {
		msg, e := messageRepo.GetMessageByEventID(ctx, msgEventID)
		return e == nil && msg.RecalledAt != nil
	}, 3*time.Second, 50*time.Millisecond, "原消息应在 DB 中标记为已撤回")

	// 断言 2：bob 实时收到撤回事件 WS 推送
	var recallPacket *gatewayv1.WsPacket
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
		if packet.GetEvent().GetEventId() == recallEventID {
			recallPacket = packet
			break
		}
	}
	require.NotNil(t, recallPacket, "应在时限内收到撤回事件推送")
	require.Equal(t, msgEventID, recallPacket.GetEvent().GetRecall().GetTargetEventId())

	// 断言 3：撤回事件落入 bob 的 Inbox（event_type = InboxEventTypeMessageRecall）
	require.Eventually(t, func() bool {
		items, e := messageRepo.GetInboxDelta(ctx, "bob", 0, 50)
		if e != nil {
			return false
		}
		for _, it := range items {
			if it.EventID == recallEventID && it.EventType == model.InboxEventTypeMessageRecall {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "撤回事件应落入 bob 的 Inbox")
}

// TestMessageRecall_PermissionDenied：bob 无法撤回 alice 发的消息。
func TestMessageRecall_PermissionDenied(t *testing.T) {
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

	// Task（无需推送）
	taskDispatcher := dispatcher.NewDispatcher(messageRepo, routerRepo, &noopPusherManager{}, logger)
	taskConsumer := consumer.NewConsumer(
		infra.mqClient,
		taskDispatcher.Handle,
		taskcfg.ConsumerConfig{
			Topic:         "resonance.chat.event.v1",
			QueueGroup:    "recall-permission-denied",
			WorkerCount:   1,
			MaxRetry:      1,
			RetryInterval: 1,
		},
		logger,
	)
	taskConsumer.SetName("storage")
	require.NoError(t, taskConsumer.Start())
	t.Cleanup(func() { _ = taskConsumer.Stop() })

	authenticator, err := auth.New(&auth.Config{
		SecretKey:      "recall-perm-secret-key-with-32-plus",
		Issuer:         "resonance-integration",
		AccessTokenTTL: 24 * time.Hour,
	}, auth.WithLogger(logger))
	require.NoError(t, err)
	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 5})
	require.NoError(t, err)
	sessionIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 5})
	require.NoError(t, err)
	sequencer, err := idgen.NewSequencer(&idgen.SequencerConfig{
		Driver:    "redis",
		KeyPrefix: "it:recall:perm:seq",
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
	sessionID := createResp.GetSessionId()

	// alice 发消息
	sendResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: sessionID,
		Payload: &logicv1.SendEventRequest_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content: "alice's message",
			},
		},
	})
	require.NoError(t, err)

	// bob 试图撤回 alice 的消息 → PermissionDenied
	_, err = chatClient.SendEvent(bobCtx, &logicv1.SendEventRequest{
		SessionId: sessionID,
		Payload: &logicv1.SendEventRequest_Recall{
			Recall: &commonv1.MessageRecall{TargetEventId: sendResp.GetEventId()},
		},
	})
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestMessageRecall_OfflineSync：alice 撤回消息后，bob 离线重连时可通过 PullInboxDelta 拉到撤回事件。
func TestMessageRecall_OfflineSync(t *testing.T) {
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

	// Task（bob 离线，noopPusherManager）
	taskDispatcher := dispatcher.NewDispatcher(messageRepo, routerRepo, &noopPusherManager{}, logger)
	taskConsumer := consumer.NewConsumer(
		infra.mqClient,
		taskDispatcher.Handle,
		taskcfg.ConsumerConfig{
			Topic:         "resonance.chat.event.v1",
			QueueGroup:    "recall-offline-sync",
			WorkerCount:   1,
			MaxRetry:      1,
			RetryInterval: 1,
		},
		logger,
	)
	taskConsumer.SetName("storage")
	require.NoError(t, taskConsumer.Start())
	t.Cleanup(func() { _ = taskConsumer.Stop() })

	authenticator, err := auth.New(&auth.Config{
		SecretKey:      "recall-offline-secret-key-with-32-plus",
		Issuer:         "resonance-integration",
		AccessTokenTTL: 24 * time.Hour,
	}, auth.WithLogger(logger))
	require.NoError(t, err)
	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 6})
	require.NoError(t, err)
	sessionIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 6})
	require.NoError(t, err)
	sequencer, err := idgen.NewSequencer(&idgen.SequencerConfig{
		Driver:    "redis",
		KeyPrefix: "it:recall:offline:seq",
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
	sessionID := createResp.GetSessionId()

	// alice 发消息
	sendResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: sessionID,
		Payload: &logicv1.SendEventRequest_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content: "message to be recalled offline",
			},
		},
	})
	require.NoError(t, err)
	msgEventID := sendResp.GetEventId()

	// 等待消息落 Inbox
	require.Eventually(t, func() bool {
		items, e := messageRepo.GetInboxDelta(ctx, "bob", 0, 20)
		if e != nil {
			return false
		}
		for _, it := range items {
			if it.EventID == msgEventID {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond)

	// alice 撤回消息
	recallResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: sessionID,
		Payload: &logicv1.SendEventRequest_Recall{
			Recall: &commonv1.MessageRecall{TargetEventId: msgEventID},
		},
	})
	require.NoError(t, err)
	recallEventID := recallResp.GetEventId()

	// 等待撤回事件落 Inbox
	require.Eventually(t, func() bool {
		items, e := messageRepo.GetInboxDelta(ctx, "bob", 0, 50)
		if e != nil {
			return false
		}
		for _, it := range items {
			if it.EventID == recallEventID {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond)

	// bob 重连后拉离线增量，应包含撤回事件
	deltaResp, err := sessionClient.PullInboxDelta(bobCtx, &logicv1.PullInboxDeltaRequest{
		CursorId: 0,
		Limit:    50,
	})
	require.NoError(t, err)

	var foundRecall bool
	for _, ev := range deltaResp.GetEvents() {
		if ev.GetEvent().GetEventId() == recallEventID {
			foundRecall = true
			require.Equal(t, msgEventID, ev.GetEvent().GetRecall().GetTargetEventId())
			break
		}
	}
	require.True(t, foundRecall, "离线增量中应包含撤回事件")
}

// recallSingleGatewayPusherManager 是测试专用的单网关推送管理器（避免与 singleGatewayPusherManager 冲突）。
type recallSingleGatewayPusherManager struct {
	gatewayID string
	client    *pusher.GatewayClient
}

func (m *recallSingleGatewayPusherManager) Start() error { return nil }
func (m *recallSingleGatewayPusherManager) Close()       {}
func (m *recallSingleGatewayPusherManager) GetClient(gatewayID string) (pusher.Client, error) {
	if gatewayID != m.gatewayID {
		return nil, context.Canceled
	}
	return m.client, nil
}

func newWSConnForRecallTest(t *testing.T, username string) (*gwws.Conn, *websocket.Conn, func()) {
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
		"trace-recall-test",
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
