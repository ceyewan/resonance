package job

import (
	"context"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
	"github.com/ceyewan/resonance/logic/config"
)

// OutboxRelay 负责扫描本地消息表并将未发送的消息补发到 MQ
type OutboxRelay struct {
	messageRepo repo.MessageRepo
	mqClient    mq.MQ
	logger      clog.Logger
	config      *config.OutboxConfig
}

func NewOutboxRelay(messageRepo repo.MessageRepo, mqClient mq.MQ, logger clog.Logger, cfg *config.OutboxConfig) *OutboxRelay {
	return &OutboxRelay{
		messageRepo: messageRepo,
		mqClient:    mqClient,
		logger:      logger.WithNamespace("outbox_relay"),
		config:      cfg,
	}
}

// Start 启动补发任务
func (j *OutboxRelay) Start(ctx context.Context) {
	j.logger.Info("starting outbox relay job")
	ticker := time.NewTicker(j.config.GetTickerTime())
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
	messages, err := j.messageRepo.GetPendingOutboxMessages(ctx, j.config.GetBatchSize())
	if err != nil {
		j.logger.Error("failed to get pending messages", clog.Error(err))
		return
	}

	if len(messages) == 0 {
		return
	}

	j.logger.Debug("processing pending outbox messages", clog.Int("count", len(messages)))

	// 2. 使用 Worker Pool 并发处理消息
	j.processMessagesWithWorkerPool(ctx, messages)
}

// processMessagesWithWorkerPool 使用 Worker Pool 并发处理消息
func (j *OutboxRelay) processMessagesWithWorkerPool(ctx context.Context, messages []*model.MessageOutbox) {
	// 创建消息通道和信号量
	msgChan := make(chan *model.MessageOutbox, len(messages))
	var wg sync.WaitGroup

	// 启动 Worker
	for i := 0; i < j.config.GetWorkerCount() && i < len(messages); i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for msg := range msgChan {
				j.relayMessage(ctx, msg)
			}
		}(i)
	}

	// 发送消息到通道
	for _, msg := range messages {
		msgChan <- msg
	}
	close(msgChan)

	// 等待所有 Worker 完成
	wg.Wait()
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
		if retryCount > j.config.GetMaxRetries() {
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
