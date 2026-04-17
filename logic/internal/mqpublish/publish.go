package mqpublish

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/logic/observability"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/event"
	"github.com/ceyewan/resonance/repo"
	"google.golang.org/protobuf/proto"
)

// PublishMessageToMQResult 发布消息到 MQ 的结果
type PublishMessageToMQResult struct {
	OutboxID  int64
	Topic     string
	EventData []byte
}

// PublishMessageToMQ 发布消息到 MQ 并保存到 Outbox
func PublishMessageToMQ(
	ctx context.Context,
	messageRepo repo.MessageRepo,
	event *mqv1.MQEvent,
	msgContent *model.MessageContent,
) (*PublishMessageToMQResult, error) {
	// 1. 注入 Trace Context 到 MQ 事件，用于链路追踪
	event.TraceHeaders = make(map[string]string)
	observability.InjectTraceContext(ctx, event.TraceHeaders)

	// 2. Marshal 事件
	eventData, err := proto.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal mq event: %w", err)
	}

	// 3. 获取 Topic (从 protobuf 扩展字段)
	topic := string(proto.GetExtension(event.ProtoReflect().Descriptor().Options(), commonv1.E_DefaultTopic).(string))

	// 4. 创建 Outbox 记录
	outbox := &model.MessageOutbox{
		EventID:       msgContent.EventID,
		Topic:         topic,
		Payload:       eventData,
		Status:        model.OutboxStatusPending,
		NextRetryTime: time.Now(),
	}

	// 5. 使用事务保存消息、更新序列号、记录 Outbox
	if err := messageRepo.SaveMessageWithOutbox(ctx, msgContent, outbox); err != nil {
		return nil, fmt.Errorf("save message with outbox: %w", err)
	}

	return &PublishMessageToMQResult{
		OutboxID:  outbox.ID,
		Topic:     topic,
		EventData: eventData,
	}, nil
}

// BuildInboxItems 构建 Inbox 写扩散记录，供 Task 和补偿流程复用。
func BuildInboxItems(ev *commonv1.ChatEvent, targets []string) []*model.Inbox {
	if ev == nil || len(targets) == 0 {
		return nil
	}

	payload, err := proto.Marshal(ev)
	if err != nil {
		return nil
	}

	eventType := event.EventTypeFromChatEvent(ev)
	items := make([]*model.Inbox, 0, len(targets))
	for _, username := range targets {
		items = append(items, &model.Inbox{
			OwnerUsername: username,
			SessionID:     ev.GetSessionId(),
			SeqID:         ev.GetSeqId(),
			EventID:       ev.GetEventId(),
			EventType:     eventType,
			Payload:       payload,
		})
	}
	return items
}

// PublishMessageToMQAsync 异步发布消息到 MQ (Look-aside 优化)
func PublishMessageToMQAsync(
	mqClient mq.MQ,
	outboxID int64,
	topic string,
	data []byte,
	logger clog.Logger,
) {
	go func() {
		// 使用独立的超时 context，避免受到 RPC context 取消的影响
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := mqClient.Publish(ctx, topic, data); err != nil {
			logger.Warn("failed to publish message to mq",
				clog.Int64("outbox_id", outboxID),
				clog.String("topic", topic),
				clog.Error(err))
			// 不返回错误，由 Outbox Job 后台补发
			return
		}

		logger.Debug("message published to mq successfully",
			clog.Int64("outbox_id", outboxID),
			clog.String("topic", topic))
	}()
}
