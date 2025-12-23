package dispatcher

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/xerrors"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/im-sdk/repo"
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
	pusher      *pusher.GatewayPusher
	logger      clog.Logger
}

// NewDispatcher 创建消息分发器
func NewDispatcher(
	sessionRepo repo.SessionRepo,
	routerRepo repo.RouterRepo,
	pusher *pusher.GatewayPusher,
	logger clog.Logger,
) *Dispatcher {
	return &Dispatcher{
		sessionRepo: sessionRepo,
		routerRepo:  routerRepo,
		pusher:      pusher,
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

	// 提取用户名列表（GetMembers 返回的是 SessionMember 列表）
	usernames := make([]string, 0, len(members))
	for _, m := range members {
		usernames = append(usernames, m.Username)
	}

	// 2. 构造推送消息
	pushMsg := &gatewayv1.PushMessage{
		MsgId:        event.MsgId,
		SeqId:        event.SeqId,
		SessionId:    event.SessionId,
		FromUsername: event.FromUsername,
		ToUsername:   event.ToUsername,
		Content:      event.Content,
		Type:         event.Type,
		Timestamp:    event.Timestamp,
	}

	// 3. 写扩散：推送给所有在线成员
	successCount := 0
	for _, username := range usernames {
		// 跳过发送者自己（发送者已经在客户端显示了）
		if username == event.FromUsername {
			continue
		}

		// 通过 RouterRepo 获取用户的 GatewayID
		gatewayID, err := d.getUserGateway(ctx, username)
		if err != nil {
			if xerrors.Is(err, ErrUserOffline) {
				d.logger.Debug("user offline, skip push",
					clog.String("username", username))
			} else {
				d.logger.Error("failed to get user gateway",
					clog.String("username", username),
					clog.Error(err))
			}
			continue
		}

		// 推送到对应的 Gateway
		if err := d.pusher.Push(ctx, gatewayID, username, pushMsg); err != nil {
			d.logger.Error("failed to push to user",
				clog.String("username", username),
				clog.String("gateway_id", gatewayID),
				clog.Error(err))
			continue
		}

		successCount++
		d.logger.Debug("pushed to user",
			clog.String("username", username),
			clog.String("gateway_id", gatewayID))
	}

	d.logger.Info("dispatch completed",
		clog.Int64("msg_id", event.MsgId),
		clog.Int("total_members", len(usernames)),
		clog.Int("success_count", successCount))

	return nil
}

// getUserGateway 获取用户的 GatewayID
func (d *Dispatcher) getUserGateway(ctx context.Context, username string) (string, error) {
	router, err := d.routerRepo.GetUserGateway(ctx, username)
	if err != nil {
		return "", xerrors.Wrapf(err, "failed to get user gateway: %s", username)
	}

	if router == nil {
		return "", xerrors.Wrapf(ErrUserOffline, "user %s has no gateway", username)
	}

	return router.GatewayID, nil
}
