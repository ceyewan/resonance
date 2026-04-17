package dispatcher

import (
	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/event"
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
	return items, nil
}
