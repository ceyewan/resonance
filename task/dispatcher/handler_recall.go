package dispatcher

import (
	"context"
	"fmt"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
)

// handleRecall 只负责写扩散 + 推送。
// 主事实(message_content.recalled_at)应在 Logic 主事务内更新后再发 MQ,Task 不做业务主事实变更。
func (d *Dispatcher) handleRecall(ctx context.Context, ev *commonv1.ChatEvent, targets []string) error {
	recall := ev.GetRecall()
	if recall == nil || recall.GetTargetEventId() == 0 {
		return fmt.Errorf("invalid recall payload")
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
