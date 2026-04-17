package dispatcher

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/metrics"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	logicservice "github.com/ceyewan/resonance/logic/service"
	"github.com/ceyewan/resonance/repo"
	"github.com/ceyewan/resonance/task/observability"
	"github.com/ceyewan/resonance/task/pusher"
	"go.opentelemetry.io/otel/attribute"
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

// DispatchStorage 处理存储任务（写扩散）
func (d *Dispatcher) DispatchStorage(ctx context.Context, mqEvent *mqv1.MQEvent) error {
	ev := mqEvent.GetEvent()
	ctx, endSpan := observability.StartSpan(ctx, "dispatcher.storage",
		attribute.Int64("event_id", ev.GetEventId()),
		attribute.String("session_id", ev.GetSessionId()),
	)
	defer endSpan()

	inboxes := logicservice.BuildInboxItems(ev, mqEvent.TargetUsernames)
	if len(inboxes) == 0 {
		return nil
	}
	if err := d.messageRepo.SaveInboxBatch(ctx, inboxes); err != nil {
		return err
	}
	return nil
}

// DispatchPush 处理推送任务（在线推送）
func (d *Dispatcher) DispatchPush(ctx context.Context, mqEvent *mqv1.MQEvent) error {
	ev := mqEvent.GetEvent()
	ctx, endSpan := observability.StartSpan(ctx, "dispatcher.push",
		attribute.Int64("event_id", ev.GetEventId()),
		attribute.String("session_id", ev.GetSessionId()),
		attribute.String("from_username", ev.GetFromUsername()),
	)
	defer endSpan()

	usernames := make([]string, 0, len(mqEvent.TargetUsernames))
	for _, u := range mqEvent.TargetUsernames {
		if u == ev.GetFromUsername() {
			continue
		}
		usernames = append(usernames, u)
	}
	if len(usernames) == 0 {
		return nil
	}

	routers, err := d.routerRepo.BatchGetUsersGateway(ctx, usernames)
	if err != nil {
		return err
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

	return nil
}
