package dispatcher

import (
	"context"

	"github.com/ceyewan/genesis/cache"
	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/im-api/gen/go/gateway/v1"
	mqv1 "github.com/ceyewan/resonance/im-api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/im-sdk/repo"
	"github.com/ceyewan/resonance/internal/task/pusher"
)

// Dispatcher 消息分发器（写扩散）
type Dispatcher struct {
	sessionRepo repo.SessionRepository
	cache       cache.Cache
	pusher      *pusher.GatewayPusher
	logger      clog.Logger
}

// NewDispatcher 创建消息分发器
func NewDispatcher(
	sessionRepo repo.SessionRepository,
	cache cache.Cache,
	pusher *pusher.GatewayPusher,
	logger clog.Logger,
) *Dispatcher {
	return &Dispatcher{
		sessionRepo: sessionRepo,
		cache:       cache,
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
	members, err := d.sessionRepo.GetSessionMembers(ctx, event.SessionId)
	if err != nil {
		d.logger.Error("failed to get session members", clog.Error(err))
		return err
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
	for _, username := range members {
		// 跳过发送者自己（发送者已经在客户端显示了）
		if username == event.FromUsername {
			continue
		}

		// 检查用户是否在线
		online, gatewayAddr, err := d.isUserOnline(ctx, username)
		if err != nil {
			d.logger.Error("failed to check user online status",
				clog.String("username", username),
				clog.Error(err))
			continue
		}

		if !online {
			d.logger.Debug("user offline, skip push",
				clog.String("username", username))
			continue
		}

		// 推送到对应的 Gateway
		if err := d.pusher.PushToUser(ctx, gatewayAddr, username, pushMsg); err != nil {
			d.logger.Error("failed to push to user",
				clog.String("username", username),
				clog.String("gateway", gatewayAddr),
				clog.Error(err))
			continue
		}

		successCount++
		d.logger.Debug("pushed to user",
			clog.String("username", username),
			clog.String("gateway", gatewayAddr))
	}

	d.logger.Info("dispatch completed",
		clog.Int64("msg_id", event.MsgId),
		clog.Int("total_members", len(members)),
		clog.Int("success_count", successCount))

	return nil
}

// isUserOnline 检查用户是否在线并返回所在的 Gateway 地址
func (d *Dispatcher) isUserOnline(ctx context.Context, username string) (bool, string, error) {
	key := "user:online:" + username
	gatewayAddr, err := d.cache.Get(ctx, key)
	if err != nil {
		return false, "", err
	}

	if gatewayAddr == "" {
		return false, "", nil
	}

	return true, gatewayAddr, nil
}

