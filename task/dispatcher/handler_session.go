package dispatcher

import (
	"context"
	"fmt"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
)

func (d *Dispatcher) handleSessionUpdate(ctx context.Context, ev *commonv1.ChatEvent, targets []string) error {
	su := ev.GetSessionUpdate()
	if su == nil {
		return fmt.Errorf("invalid session_update payload")
	}

	inboxes, err := buildInboxesForEvent(ev, targets)
	if err != nil {
		return fmt.Errorf("build inbox for session_update event: %w", err)
	}
	if len(inboxes) == 0 {
		return nil
	}
	if err := d.messageRepo.SaveInboxBatch(ctx, inboxes); err != nil {
		return fmt.Errorf("save inbox for session_update event: %w", err)
	}
	return nil
}
