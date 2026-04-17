package dispatcher

import (
	"context"
	"fmt"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
)

func (d *Dispatcher) handleReadReceipt(ctx context.Context, ev *commonv1.ChatEvent, targets []string) error {
	rr := ev.GetReadReceipt()
	if rr == nil {
		return fmt.Errorf("invalid read_receipt payload")
	}

	inboxes, err := buildInboxesForEvent(ev, targets)
	if err != nil {
		return fmt.Errorf("build inbox for read_receipt event: %w", err)
	}
	if len(inboxes) == 0 {
		return nil
	}
	if err := d.messageRepo.SaveInboxBatch(ctx, inboxes); err != nil {
		return fmt.Errorf("save inbox for read_receipt event: %w", err)
	}
	return nil
}
