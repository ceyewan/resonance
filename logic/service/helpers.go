package service

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
// 这是一个辅助函数，用于避免重复的 MQ 发布逻辑
//
// 参数：
//   - ctx: 上下文
//   - messageRepo: 消息仓储
//   - event: MQ 事件
//   - msgContent: 消息内容
//   - logger: 日志记录器
//
// 返回：
//   - *PublishMessageToMQResult: 发布结果（包含 OutboxID、Topic、EventData）
//   - error: 如果发生错误
func PublishMessageToMQ(
	ctx context.Context,
	messageRepo repo.MessageRepo,
	event *mqv1.MQEvent,
	msgContent *model.MessageContent,
	logger clog.Logger,
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

func parseMessageType(raw int) commonv1.MessageType {
	switch raw {
	case int(commonv1.MessageType_MESSAGE_TYPE_TEXT):
		return commonv1.MessageType_MESSAGE_TYPE_TEXT
	case int(commonv1.MessageType_MESSAGE_TYPE_IMAGE):
		return commonv1.MessageType_MESSAGE_TYPE_IMAGE
	case int(commonv1.MessageType_MESSAGE_TYPE_FILE):
		return commonv1.MessageType_MESSAGE_TYPE_FILE
	case int(commonv1.MessageType_MESSAGE_TYPE_SYSTEM):
		return commonv1.MessageType_MESSAGE_TYPE_SYSTEM
	case int(commonv1.MessageType_MESSAGE_TYPE_AI_STREAM):
		return commonv1.MessageType_MESSAGE_TYPE_AI_STREAM
	default:
		return commonv1.MessageType_MESSAGE_TYPE_UNSPECIFIED
	}
}

func formatMessageType(t commonv1.MessageType) int {
	switch t {
	case commonv1.MessageType_MESSAGE_TYPE_TEXT:
		return int(commonv1.MessageType_MESSAGE_TYPE_TEXT)
	case commonv1.MessageType_MESSAGE_TYPE_IMAGE:
		return int(commonv1.MessageType_MESSAGE_TYPE_IMAGE)
	case commonv1.MessageType_MESSAGE_TYPE_FILE:
		return int(commonv1.MessageType_MESSAGE_TYPE_FILE)
	case commonv1.MessageType_MESSAGE_TYPE_SYSTEM:
		return int(commonv1.MessageType_MESSAGE_TYPE_SYSTEM)
	case commonv1.MessageType_MESSAGE_TYPE_AI_STREAM:
		return int(commonv1.MessageType_MESSAGE_TYPE_AI_STREAM)
	default:
		return int(commonv1.MessageType_MESSAGE_TYPE_TEXT)
	}
}

func eventTypeFromChatEvent(ev *commonv1.ChatEvent) int {
	switch ev.GetPayload().(type) {
	case *commonv1.ChatEvent_Message:
		return model.InboxEventTypeMessage
	case *commonv1.ChatEvent_Recall:
		return model.InboxEventTypeMessageRecall
	case *commonv1.ChatEvent_Edit:
		return model.InboxEventTypeMessageEdit
	case *commonv1.ChatEvent_ReadReceipt:
		return model.InboxEventTypeReadReceipt
	case *commonv1.ChatEvent_SessionUpdate:
		return model.InboxEventTypeSessionUpdate
	default:
		return 0
	}
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

	eventType := eventTypeFromChatEvent(ev)
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
// 这个函数在后台尝试立即发布消息，不阻塞主流程
//
// 参数：
//   - mqClient: MQ 客户端
//   - outboxID: Outbox 记录 ID
//   - topic: MQ Topic
//   - data: 消息数据
//   - logger: 日志记录器
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
