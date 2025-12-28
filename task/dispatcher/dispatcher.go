package dispatcher

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/xerrors"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/internal/repo"
	"github.com/ceyewan/resonance/task/pusher"
)

var (
	// ErrUserOffline 用户离线
	ErrUserOffline = xerrors.New("user offline")
)

// Dispatcher 消息分发器（写扩散）
type Dispatcher struct {
	sessionRepo repo.SessionRepo
	routerRepo  repo.RouterRepo
	pusherMgr   *pusher.Manager
	logger      clog.Logger
}

// NewDispatcher 创建消息分发器
func NewDispatcher(
	sessionRepo repo.SessionRepo,
	routerRepo repo.RouterRepo,
	pusherMgr *pusher.Manager,
	logger clog.Logger,
) *Dispatcher {
	return &Dispatcher{
		sessionRepo: sessionRepo,
		routerRepo:  routerRepo,
		pusherMgr:   pusherMgr,
		logger:      logger,
	}
}

// Dispatch 分发消息（写扩散）
func (d *Dispatcher) Dispatch(ctx context.Context, event *mqv1.PushEvent) error {
	d.logger.Info("dispatching message",
		clog.Int64("msg_id", event.MsgId),
		clog.String("session_id", event.SessionId))

	// 1. 获取会话成员列表
	members, err := d.sessionRepo.GetMembers(ctx, event.SessionId)
	if err != nil {
		d.logger.Error("failed to get session members", clog.Error(err))
		return err
	}

	// 提取用户名列表
	usernames := make([]string, 0, len(members))
	for _, m := range members {
		// 跳过发送者自己
		if m.Username == event.FromUsername {
			continue
		}
		usernames = append(usernames, m.Username)
	}

	if len(usernames) == 0 {
		d.logger.Info("no target users to push", clog.Int64("msg_id", event.MsgId))
		return nil
	}

	// 2. 批量获取用户网关路由
	// RouterRepo 增加了 BatchGetUsersGateway 方法
	routers, err := d.routerRepo.BatchGetUsersGateway(ctx, usernames)
	if err != nil {
		d.logger.Error("failed to batch get user gateways", clog.Error(err))
		return err // 可以选择重试或部分失败
	}

	// 3. 按 GatewayID 分组
	gatewayGroups := make(map[string][]string) // gatewayID -> []username
	for _, router := range routers {
		if router == nil {
			continue // 用户离线或无路由
		}
		gatewayGroups[router.GatewayID] = append(gatewayGroups[router.GatewayID], router.Username)
	}

	// 4. 构造推送消息
	pushMsg := &gatewayv1.PushMessage{
		MsgId:        event.MsgId,
		SeqId:        event.SeqId,
		SessionId:    event.SessionId,
		FromUsername: event.FromUsername,
		Content:      event.Content,
		Type:         event.Type,
		Timestamp:    event.Timestamp,
	}

	// 5. 并发推送给各个 Gateway
	// TODO: 可以使用 Worker Pool 限制并发数
	successCount := 0
	for gatewayID, users := range gatewayGroups {
		// 获取 Pusher Client
		client, err := d.pusherMgr.GetClient(gatewayID)
		if err != nil {
			d.logger.Warn("gateway client not found",
				clog.String("gateway_id", gatewayID),
				clog.Int("user_count", len(users)))
			continue
		}

		// 批量推送到对应的 Gateway
		if err := client.PushBatch(ctx, users, pushMsg); err != nil {
			d.logger.Error("failed to push batch to gateway",
				clog.String("gateway_id", gatewayID),
				clog.Int("user_count", len(users)),
				clog.Error(err))
			continue
		}

		successCount += len(users)
		d.logger.Debug("pushed batch to gateway",
			clog.String("gateway_id", gatewayID),
			clog.Int("user_count", len(users)))
	}

	d.logger.Info("dispatch completed",
		clog.Int64("msg_id", event.MsgId),
		clog.Int("total_targets", len(usernames)),
		clog.Int("online_targets", successCount))

	return nil
}
