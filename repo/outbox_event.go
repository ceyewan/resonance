package repo

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
)

func advanceSessionMaxSeqFromOutbox(tx *gorm.DB, outbox *model.MessageOutbox) error {
	if outbox == nil || len(outbox.Payload) == 0 {
		return nil
	}

	var mqEvent mqv1.MQEvent
	if err := proto.Unmarshal(outbox.Payload, &mqEvent); err != nil {
		return fmt.Errorf("unmarshal outbox mq event: %w", err)
	}
	ev := mqEvent.GetEvent()
	if ev == nil || ev.GetSessionId() == "" || ev.GetSeqId() <= 0 {
		return nil
	}

	result := tx.Model(&model.Session{}).
		Where("session_id = ? AND max_seq_id < ?", ev.GetSessionId(), ev.GetSeqId()).
		Update("max_seq_id", ev.GetSeqId())
	if result.Error != nil {
		return fmt.Errorf("advance session max_seq_id: %w", result.Error)
	}
	return nil
}
