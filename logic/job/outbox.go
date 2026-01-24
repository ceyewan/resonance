package job

import (
	"context"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/resonance/internal/model"
	"github.com/ceyewan/resonance/internal/repo"
)

const (
	BatchSize  = 100
	MaxRetries = 5
	TickerTime = time.Second
)

// OutboxRelay 负责扫描本地消息表并将未发送的消息补发到 MQ
type OutboxRelay struct {
	messageRepo repo.MessageRepo
	mqClient    mq.Client
	logger      clog.Logger
}

func NewOutboxRelay(messageRepo repo.MessageRepo, mqClient mq.Client, logger clog.Logger) *OutboxRelay {
	return &OutboxRelay{
		messageRepo: messageRepo,
		mqClient:    mqClient,
		logger:      logger.WithNamespace("outbox_relay"),
	}
}

// Start 启动补发任务
func (j *OutboxRelay) Start(ctx context.Context) {
	j.logger.Info("starting outbox relay job")
	ticker := time.NewTicker(TickerTime)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			j.logger.Info("outbox relay job stopped")
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						j.logger.Error("panic in outbox relay job", clog.Any("panic", r))
					}
				}()
				j.processPendingMessages(ctx)
			}()
		}
	}
}

func (j *OutboxRelay) processPendingMessages(ctx context.Context) {
	// 1. 获取待处理消息
	messages, err := j.messageRepo.GetPendingOutboxMessages(ctx, BatchSize)
	if err != nil {
		j.logger.Error("failed to get pending messages", clog.Error(err))
		return
	}

	if len(messages) == 0 {
		return
	}

	j.logger.Debug("processing pending outbox messages", clog.Int("count", len(messages)))

	for _, msg := range messages {
		j.relayMessage(ctx, msg)
	}
}

func (j *OutboxRelay) relayMessage(ctx context.Context, msg *model.MessageOutbox) {
	// 2. 发送到 MQ
	if err := j.mqClient.Publish(ctx, msg.Topic, msg.Payload); err != nil {
		j.logger.Warn("failed to relay message",
			clog.Int64("id", msg.ID),
			clog.Int64("msg_id", msg.MsgID),
			clog.Error(err))

		// 更新重试信息 (指数退避)
		retryCount := msg.RetryCount + 1
		if retryCount > MaxRetries {
			// 标记为失败，不再重试
			_ = j.messageRepo.UpdateOutboxStatus(ctx, msg.ID, model.OutboxStatusFailed)
			j.logger.Error("message reached max retries, marked as failed", clog.Int64("msg_id", msg.MsgID))
			return
		}

		nextRetry := time.Now().Add(time.Duration(retryCount*retryCount) * time.Second)
		_ = j.messageRepo.UpdateOutboxRetry(ctx, msg.ID, nextRetry, retryCount)
		return
	}

	// 3. 标记为已发送
	if err := j.messageRepo.UpdateOutboxStatus(ctx, msg.ID, model.OutboxStatusSent); err != nil {
		j.logger.Error("failed to update status after relay",
			clog.Int64("id", msg.ID),
			clog.Error(err))
	}
}
