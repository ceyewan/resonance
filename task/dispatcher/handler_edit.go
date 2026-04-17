package dispatcher

import (
	"context"
	"fmt"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
)

// handleEdit 只负责写扩散 + 推送。
// 主事实(message_content.content / edited_at)应在 Logic 主事务内更新后再发 MQ,Task 不做业务主事实变更。
func (d *Dispatcher) handleEdit(ctx context.Context, ev *commonv1.ChatEvent, targets []string) error {
	edit := ev.GetEdit()
	if edit == nil || edit.GetTargetEventId() == 0 {
		return fmt.Errorf("invalid edit payload")
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
