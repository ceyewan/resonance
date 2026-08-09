package integration

import (
	"context"
	"net"
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

func TestMessageEdit_GoldenPath(t *testing.T) {
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

	connMgr := gwws.NewManager(logger, nil, nil, nil)
	bobConn, bobWSClient, wsCleanup := newWSConnForRecallTest(t, "bob")
	defer wsCleanup()
	require.NoError(t, connMgr.AddConnection("bob", bobConn))

	gatewayID := "gw-edit-1"
	gatewayAddr := mustFreeAddr(t)
	gatewayGRPC := gwserver.NewGRPCServer(gatewayAddr, logger, pushserver.NewService(connMgr, logger))
	go func() { _ = gatewayGRPC.Start() }()
	t.Cleanup(gatewayGRPC.Stop)

	gatewayClient := newGatewayClientWithRetry(t, gatewayAddr, gatewayID, logger)
	t.Cleanup(func() { _ = gatewayClient.Close() })
	pusherMgr := &editSingleGatewayPusherManager{gatewayID: gatewayID, client: gatewayClient}

	taskDispatcher := dispatcher.NewDispatcher(messageRepo, routerRepo, pusherMgr, logger)
	taskConsumer := consumer.NewConsumer(
		infra.mqClient,
		taskDispatcher.Handle,
		taskcfg.ConsumerConfig{Topic: "resonance.chat.event.v1", QueueGroup: "golden-edit", WorkerCount: 1, MaxRetry: 1, RetryInterval: 1},
		logger,
	)
	taskConsumer.SetName("storage")
	require.NoError(t, taskConsumer.Start())
	t.Cleanup(func() { _ = taskConsumer.Stop() })

	authenticator, err := auth.New(&auth.Config{SecretKey: "edit-golden-secret-key-with-32-plus", Issuer: "resonance-integration", AccessTokenTTL: 24 * time.Hour}, auth.WithLogger(logger))
	require.NoError(t, err)
	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 7})
	require.NoError(t, err)
	sessionIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 7})
	require.NoError(t, err)
	sequencer, err := idgen.NewSequencer(&idgen.SequencerConfig{Driver: "redis", KeyPrefix: "it:edit:seq", Step: 1}, idgen.WithRedisConnector(infra.redisConn), idgen.WithLogger(logger))
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

	require.NoError(t, routerRepo.SetUserGateway(ctx, &model.Router{Username: "bob", GatewayID: gatewayID, RemoteIP: "127.0.0.1", Timestamp: time.Now().UnixMilli()}))

	aliceCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-username", "alice"))
	bobCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-username", "bob"))
	createResp, err := sessionClient.CreateSession(aliceCtx, &logicv1.CreateSessionRequest{Type: commonv1.SessionType_SESSION_TYPE_DIRECT, Members: []string{"bob"}})
	require.NoError(t, err)
	sessionID := createResp.GetSessionId()

	sendResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: sessionID,
		Payload:   &logicv1.SendEventRequest_Message{Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "hello before edit"}},
	})
	require.NoError(t, err)
	msgEventID := sendResp.GetEventId()

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

	editResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: sessionID,
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: msgEventID, NewContent: "hello after edit"}},
	})
	require.NoError(t, err)
	editEventID := editResp.GetEventId()

	require.Eventually(t, func() bool {
		msg, e := messageRepo.GetMessageByEventID(ctx, msgEventID)
		return e == nil && msg.Content == "hello after edit" && msg.EditedAt != nil
	}, 3*time.Second, 50*time.Millisecond)

	var editPacket *gatewayv1.WsPacket
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
		if packet.GetEvent().GetEventId() == editEventID {
			editPacket = packet
			break
		}
	}
	require.NotNil(t, editPacket)
	require.Equal(t, msgEventID, editPacket.GetEvent().GetEdit().GetTargetEventId())
	require.Equal(t, "hello after edit", editPacket.GetEvent().GetEdit().GetNewContent())

	require.Eventually(t, func() bool {
		items, e := messageRepo.GetInboxDelta(ctx, "bob", 0, 50)
		if e != nil {
			return false
		}
		for _, it := range items {
			if it.EventID == editEventID && it.EventType == model.InboxEventTypeMessageEdit {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond)

	deltaResp, err := sessionClient.PullInboxDelta(bobCtx, &logicv1.PullInboxDeltaRequest{CursorId: 0, Limit: 50})
	require.NoError(t, err)
	var foundEdit bool
	for _, ev := range deltaResp.GetEvents() {
		if ev.GetEvent().GetEventId() == editEventID {
			foundEdit = true
			require.Equal(t, msgEventID, ev.GetEvent().GetEdit().GetTargetEventId())
			require.Equal(t, "hello after edit", ev.GetEvent().GetEdit().GetNewContent())
			break
		}
	}
	require.True(t, foundEdit)
}

func TestMessageEdit_PermissionDenied(t *testing.T) {
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

	taskDispatcher := dispatcher.NewDispatcher(messageRepo, routerRepo, &noopPusherManager{}, logger)
	taskConsumer := consumer.NewConsumer(
		infra.mqClient,
		taskDispatcher.Handle,
		taskcfg.ConsumerConfig{Topic: "resonance.chat.event.v1", QueueGroup: "edit-permission-denied", WorkerCount: 1, MaxRetry: 1, RetryInterval: 1},
		logger,
	)
	taskConsumer.SetName("storage")
	require.NoError(t, taskConsumer.Start())
	t.Cleanup(func() { _ = taskConsumer.Stop() })

	authenticator, err := auth.New(&auth.Config{SecretKey: "edit-perm-secret-key-with-32-plus", Issuer: "resonance-integration", AccessTokenTTL: 24 * time.Hour}, auth.WithLogger(logger))
	require.NoError(t, err)
	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 8})
	require.NoError(t, err)
	sessionIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: 8})
	require.NoError(t, err)
	sequencer, err := idgen.NewSequencer(&idgen.SequencerConfig{Driver: "redis", KeyPrefix: "it:edit:perm:seq", Step: 1}, idgen.WithRedisConnector(infra.redisConn), idgen.WithLogger(logger))
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
	createResp, err := sessionClient.CreateSession(aliceCtx, &logicv1.CreateSessionRequest{Type: commonv1.SessionType_SESSION_TYPE_DIRECT, Members: []string{"bob"}})
	require.NoError(t, err)

	sendResp, err := chatClient.SendEvent(aliceCtx, &logicv1.SendEventRequest{
		SessionId: createResp.GetSessionId(),
		Payload:   &logicv1.SendEventRequest_Message{Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "alice message"}},
	})
	require.NoError(t, err)

	_, err = chatClient.SendEvent(bobCtx, &logicv1.SendEventRequest{
		SessionId: createResp.GetSessionId(),
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: sendResp.GetEventId(), NewContent: "bob cannot edit"}},
	})
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

type editSingleGatewayPusherManager struct {
	gatewayID string
	client    *pusher.GatewayClient
}

func (m *editSingleGatewayPusherManager) Start() error { return nil }
func (m *editSingleGatewayPusherManager) Close()       {}
func (m *editSingleGatewayPusherManager) GetClient(gatewayID string) (pusher.Client, error) {
	if gatewayID != m.gatewayID {
		return nil, context.Canceled
	}
	return m.client, nil
}
