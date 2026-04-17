package dispatcher

import (
	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
	"google.golang.org/protobuf/proto"
)

func buildInboxesForEvent(ev *commonv1.ChatEvent, targets []string) ([]*model.Inbox, error) {
	if ev == nil || len(targets) == 0 {
		return nil, nil
	}

	payload, err := proto.Marshal(ev)
	if err != nil {
		return nil, err
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
	return items, nil
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
