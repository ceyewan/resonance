package event

import (
	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
)

// ParseMessageType 将数据库中的消息类型转换为协议枚举。
func ParseMessageType(raw int) commonv1.MessageType {
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

// FormatMessageType 将协议枚举转换为数据库存储值。
func FormatMessageType(t commonv1.MessageType) int {
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

// EventTypeFromChatEvent 从 ChatEvent payload 推导 inbox 事件类型。
func EventTypeFromChatEvent(ev *commonv1.ChatEvent) int {
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

// BuildMessageEventFromModel 将消息模型转换为 ChatEvent。
func BuildMessageEventFromModel(sessionID string, msg *model.MessageContent) *commonv1.ChatEvent {
	messageType := ParseMessageType(msg.MsgType)
	content := msg.Content
	recalled := msg.RecalledAt != nil
	if recalled {
		// The durable row is retained for idempotency/audit, but recalled content
		// must never be reconstructed into history responses or Agent prompts.
		messageType = commonv1.MessageType_MESSAGE_TYPE_UNSPECIFIED
		content = ""
	}
	return &commonv1.ChatEvent{
		EventId:      msg.EventID,
		SeqId:        msg.SeqID,
		SessionId:    sessionID,
		FromUsername: msg.SenderUsername,
		TimestampMs:  msg.CreatedAt.UnixMilli(),
		Payload: &commonv1.ChatEvent_Message{
			Message: &commonv1.Message{
				Type:     messageType,
				Content:  content,
				Recalled: recalled,
			},
		},
	}
}
