package dispatcher

import (
	"context"
	"fmt"
	"time"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
)

func (d *Dispatcher) handleRecall(ctx context.Context, ev *commonv1.ChatEvent, targets []string) error {
	recall := ev.GetRecall()
	if recall == nil || recall.GetTargetEventId() == 0 {
		return fmt.Errorf("invalid recall payload")
	}

	if err := d.messageRepo.MarkMessageRecalled(ctx, recall.GetTargetEventId(), time.Now()); err != nil {
		return fmt.Errorf("mark message recalled: %w", err)
	}

	inboxes, err := buildInboxesForEvent(ev, targets)
	if err != nil {
		return fmt.Errorf("build inbox for recall event: %w", err)
	}
	if len(inboxes) == 0 {
		return nil
	}
	if err := d.messageRepo.SaveInboxBatch(ctx, inboxes); err != nil {
		return fmt.Errorf("save inbox for recall event: %w", err)
	}
	return nil
}
