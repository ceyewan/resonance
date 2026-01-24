package dispatcher

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/xerrors"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/internal/model"
	"github.com/ceyewan/resonance/internal/repo"
	"github.com/ceyewan/resonance/task/pusher"
)

var (
	// ErrUserOffline 用户离线
	ErrUserOffline = xerrors.New("user offline")
)

// Dispatcher 消息分发器
type Dispatcher struct {
	sessionRepo repo.SessionRepo
	messageRepo repo.MessageRepo // Storage consumer uses this
	routerRepo  repo.RouterRepo  // Push consumer uses this
	pusherMgr   *pusher.Manager  // Push consumer uses this
	logger      clog.Logger
}

// NewDispatcher 创建消息分发器
func NewDispatcher(
	sessionRepo repo.SessionRepo,
	messageRepo repo.MessageRepo,
	routerRepo repo.RouterRepo,
	pusherMgr *pusher.Manager,
	logger clog.Logger,
) *Dispatcher {
	return &Dispatcher{
		sessionRepo: sessionRepo,
		messageRepo: messageRepo,
		routerRepo:  routerRepo,
		pusherMgr:   pusherMgr,
		logger:      logger,
	}
}

// DispatchStorage 处理存储任务（写扩散）
func (d *Dispatcher) DispatchStorage(ctx context.Context, event *mqv1.PushEvent) error {
	d.logger.Info("dispatching storage task",
		clog.Int64("msg_id", event.MsgId),
		clog.String("session_id", event.SessionId))

	// 1. 获取会话成员列表
	members, err := d.sessionRepo.GetMembers(ctx, event.SessionId)
	if err != nil {
		d.logger.Error("failed to get session members", clog.Error(err))
		return err
	}

	// 2. 执行写扩散 (Inbox)
	inboxes := make([]*model.Inbox, 0, len(members))
	for _, m := range members {
		// 发送者也需要在自己的信箱看到消息
		inboxes = append(inboxes, &model.Inbox{
			OwnerUsername: m.Username,
			SessionID:     event.SessionId,
			MsgID:         event.MsgId,
			SeqID:         event.SeqId,
			IsRead:        0,
		})
	}

	if err := d.messageRepo.SaveInbox(ctx, inboxes); err != nil {
		d.logger.Error("failed to save inboxes", clog.Error(err))
		return err // 存储失败需要重试
	}

	d.logger.Info("storage task completed",
		clog.Int64("msg_id", event.MsgId),
		clog.Int("inbox_count", len(inboxes)))

	return nil
}

// DispatchPush 处理推送任务（在线推送）
// 将消息投递到对应 Gateway 的推送队列，由 GatewayClient 的 loop 异步执行
func (d *Dispatcher) DispatchPush(ctx context.Context, event *mqv1.PushEvent) error {
	d.logger.Info("dispatching push task",
		clog.Int64("msg_id", event.MsgId),
		clog.String("session_id", event.SessionId))

	// 1. 获取会话成员列表
	members, err := d.sessionRepo.GetMembers(ctx, event.SessionId)
	if err != nil {
		d.logger.Error("failed to get session members", clog.Error(err))
		return err
	}

	// 2. 提取需要在线推送的用户名列表
	usernames := make([]string, 0, len(members))
	for _, m := range members {
		// 跳过发送者自己，发送者不需要在线推送（因为他是消息源）
		if m.Username == event.FromUsername {
			continue
		}
		usernames = append(usernames, m.Username)
	}

	if len(usernames) == 0 {
		d.logger.Info("no target users to push", clog.Int64("msg_id", event.MsgId))
		return nil
	}

	// 3. 批量获取用户网关路由
	routers, err := d.routerRepo.BatchGetUsersGateway(ctx, usernames)
	if err != nil {
		d.logger.Error("failed to batch get user gateways", clog.Error(err))
		return err // Redis 错误可以选择重试
	}

	// 4. 按 GatewayID 分组
	gatewayGroups := make(map[string][]string) // gatewayID -> []username
	for _, router := range routers {
		if router == nil {
			continue // 用户离线或无路由
		}
		gatewayGroups[router.GatewayID] = append(gatewayGroups[router.GatewayID], router.Username)
	}

	// 5. 构造推送消息
	pushMsg := &gatewayv1.PushMessage{
		MsgId:        event.MsgId,
		SeqId:        event.SeqId,
		SessionId:    event.SessionId,
		FromUsername: event.FromUsername,
		Content:      event.Content,
		Type:         event.Type,
		Timestamp:    event.Timestamp,
	}

	// 携带会话元数据（用于前端自动创建会话）
	if event.SessionName != "" || event.SessionType != 0 {
		pushMsg.SessionMeta = &gatewayv1.SessionMeta{
			Name: event.SessionName,
			Type: event.SessionType,
		}
	}

	// 6. 投递到各 Gateway 的推送队列
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

		// 投递任务到队列（非阻塞）
		task := &pusher.PushTask{
			ToUsernames: users,
			Message:     pushMsg,
		}

		if err := client.Enqueue(task); err != nil {
			d.logger.Error("failed to enqueue push task",
				clog.String("gateway_id", gatewayID),
				clog.Int("user_count", len(users)),
				clog.Error(err))
			continue
		}

		successCount += len(users)
		d.logger.Debug("enqueued push task",
			clog.String("gateway_id", gatewayID),
			clog.Int("user_count", len(users)),
			clog.Int("queue_size", client.QueueSize()))
	}

	d.logger.Info("push task enqueued",
		clog.Int64("msg_id", event.MsgId),
		clog.Int("total_targets", len(usernames)),
		clog.Int("enqueued_targets", successCount))

	return nil
}
