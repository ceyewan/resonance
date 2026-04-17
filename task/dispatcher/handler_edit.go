package dispatcher

import (
	"context"
	"fmt"
	"time"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
)

func (d *Dispatcher) handleEdit(ctx context.Context, ev *commonv1.ChatEvent, targets []string) error {
	edit := ev.GetEdit()
	if edit == nil || edit.GetTargetEventId() == 0 {
		return fmt.Errorf("invalid edit payload")
	}

	if err := d.messageRepo.UpdateMessageContent(ctx, edit.GetTargetEventId(), edit.GetNewContent(), time.Now()); err != nil {
		return fmt.Errorf("update message content: %w", err)
	}

	inboxes, err := buildInboxesForEvent(ev, targets)
	if err != nil {
		return fmt.Errorf("build inbox for edit event: %w", err)
	}
	if len(inboxes) == 0 {
		return nil
	}
	if err := d.messageRepo.SaveInboxBatch(ctx, inboxes); err != nil {
		return fmt.Errorf("save inbox for edit event: %w", err)
	}
	return nil
}
