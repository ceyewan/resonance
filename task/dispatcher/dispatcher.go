package dispatcher

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/metrics"
	"go.opentelemetry.io/otel/attribute"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/repo"
	"github.com/ceyewan/resonance/task/observability"
	"github.com/ceyewan/resonance/task/pusher"
)

// Dispatcher 消息分发器
type Dispatcher struct {
	messageRepo repo.MessageRepo
	routerRepo  repo.RouterRepo
	pusherMgr   pusher.PusherManager
	logger      clog.Logger
}

func NewDispatcher(
	messageRepo repo.MessageRepo,
	routerRepo repo.RouterRepo,
	pusherMgr pusher.PusherManager,
	logger clog.Logger,
) *Dispatcher {
	return &Dispatcher{
		messageRepo: messageRepo,
		routerRepo:  routerRepo,
		pusherMgr:   pusherMgr,
		logger:      logger,
	}
}

// Handle 单入口处理：先存储，后推送。
// 存储失败返回 error 触发 Consumer NAK 重试；推送失败只记录日志与指标，不影响 ACK。
func (d *Dispatcher) Handle(ctx context.Context, mqEvent *mqv1.MQEvent) error {
	ev := mqEvent.GetEvent()
	if ev == nil {
		d.logger.Warn("skip empty mq event")
		return nil
	}

	ctx, endSpan := observability.StartSpan(ctx, "dispatcher.handle",
		attribute.Int64("event_id", ev.GetEventId()),
		attribute.String("session_id", ev.GetSessionId()),
		attribute.String("from_username", ev.GetFromUsername()),
	)
	defer endSpan()

	switch ev.GetPayload().(type) {
	case *commonv1.ChatEvent_Message:
		if err := d.handleMessage(ctx, ev, mqEvent.TargetUsernames); err != nil {
			return err
		}
	case *commonv1.ChatEvent_Recall:
		if err := d.handleRecall(ctx, ev, mqEvent.TargetUsernames); err != nil {
			return err
		}
	case *commonv1.ChatEvent_Edit:
		if err := d.handleEdit(ctx, ev, mqEvent.TargetUsernames); err != nil {
			return err
		}
	case *commonv1.ChatEvent_ReadReceipt:
		if err := d.handleReadReceipt(ctx, ev, mqEvent.TargetUsernames); err != nil {
			return err
		}
	case *commonv1.ChatEvent_SessionUpdate:
		if err := d.handleSessionUpdate(ctx, ev, mqEvent.TargetUsernames); err != nil {
			return err
		}
	default:
		// 避免未知 payload 导致反复 NAK。
		d.logger.Warn("skip unsupported payload",
			clog.Int64("event_id", ev.GetEventId()))
		return nil
	}

	d.pushToOnlineUsers(ctx, ev, mqEvent.TargetUsernames)
	return nil
}

func (d *Dispatcher) pushToOnlineUsers(ctx context.Context, ev *commonv1.ChatEvent, targets []string) {
	usernames := make([]string, 0, len(targets))
	for _, u := range targets {
		if u == ev.GetFromUsername() {
			continue
		}
		usernames = append(usernames, u)
	}
	if len(usernames) == 0 {
		return
	}

	routers, err := d.routerRepo.BatchGetUsersGateway(ctx, usernames)
	if err != nil {
		d.logger.Warn("failed to query online routes",
			clog.Int64("event_id", ev.GetEventId()),
			clog.Error(err))
		return
	}

	gatewayGroups := make(map[string][]string)
	for _, router := range routers {
		if router == nil {
			continue
		}
		gatewayGroups[router.GatewayID] = append(gatewayGroups[router.GatewayID], router.Username)
	}

	successCount := 0
	failedCount := 0
	for gatewayID, users := range gatewayGroups {
		client, err := d.pusherMgr.GetClient(gatewayID)
		if err != nil {
			failedCount += len(users)
			observability.RecordPushEnqueueFailed(ctx,
				metrics.L("gateway_id", gatewayID),
				metrics.L("reason", "client_not_found"),
			)
			continue
		}

		task := &pusher.PushTask{
			ToUsernames: users,
			Event:       ev,
		}
		if err := client.Enqueue(task); err != nil {
			failedCount += len(users)
			observability.RecordPushEnqueueFailed(ctx,
				metrics.L("gateway_id", gatewayID),
				metrics.L("reason", "queue_full"),
			)
			continue
		}

		successCount += len(users)
		observability.RecordPushEnqueue(ctx, metrics.L("gateway_id", gatewayID))
		observability.SetGatewayQueueDepth(ctx, gatewayID, client.QueueSize())
	}

	d.logger.Debug("push task enqueued",
		clog.Int64("event_id", ev.GetEventId()),
		clog.Int("total_targets", len(usernames)),
		clog.Int("enqueued_targets", successCount),
		clog.Int("failed_targets", failedCount))
}
