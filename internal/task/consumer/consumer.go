package consumer

import (
	"context"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	mqv1 "github.com/ceyewan/resonance/im-api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/internal/task/dispatcher"
	"google.golang.org/protobuf/proto"
)

// Consumer MQ 消费者
type Consumer struct {
	subscriber mq.Subscriber
	dispatcher *dispatcher.Dispatcher
	config     ConsumerConfig
	logger     clog.Logger

	ctx    context.Context
	cancel context.CancelFunc
}

// ConsumerConfig 消费者配置
type ConsumerConfig struct {
	Topic         string
	QueueGroup    string
	WorkerCount   int
	MaxRetry      int
	RetryInterval time.Duration
}

// NewConsumer 创建消费者
func NewConsumer(
	subscriber mq.Subscriber,
	dispatcher *dispatcher.Dispatcher,
	config ConsumerConfig,
	logger clog.Logger,
) *Consumer {
	ctx, cancel := context.WithCancel(context.Background())

	return &Consumer{
		subscriber: subscriber,
		dispatcher: dispatcher,
		config:     config,
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start 启动消费者
func (c *Consumer) Start() error {
	c.logger.Info("starting consumer",
		clog.String("topic", c.config.Topic),
		clog.String("queue_group", c.config.QueueGroup),
		clog.Int("worker_count", c.config.WorkerCount))

	// 订阅主题
	msgChan, err := c.subscriber.Subscribe(c.ctx, c.config.Topic, c.config.QueueGroup)
	if err != nil {
		c.logger.Error("failed to subscribe", clog.Error(err))
		return err
	}

	// 启动多个 worker 并发处理
	for i := 0; i < c.config.WorkerCount; i++ {
		go c.worker(i, msgChan)
	}

	c.logger.Info("consumer started")
	return nil
}

// worker 处理消息的工作协程
func (c *Consumer) worker(id int, msgChan <-chan mq.Message) {
	c.logger.Info("worker started", clog.Int("worker_id", id))

	for {
		select {
		case <-c.ctx.Done():
			c.logger.Info("worker stopped", clog.Int("worker_id", id))
			return

		case msg, ok := <-msgChan:
			if !ok {
				c.logger.Warn("message channel closed", clog.Int("worker_id", id))
				return
			}

			c.handleMessage(id, msg)
		}
	}
}

// handleMessage 处理单条消息
func (c *Consumer) handleMessage(workerID int, msg mq.Message) {
	c.logger.Debug("received message",
		clog.Int("worker_id", workerID),
		clog.String("subject", msg.Subject()))

	// 解析 PushEvent
	event := &mqv1.PushEvent{}
	if err := proto.Unmarshal(msg.Data(), event); err != nil {
		c.logger.Error("failed to unmarshal push event",
			clog.Int("worker_id", workerID),
			clog.Error(err))
		msg.Ack() // 无法解析的消息直接 Ack，避免重复消费
		return
	}

	c.logger.Info("processing push event",
		clog.Int("worker_id", workerID),
		clog.Int64("msg_id", event.MsgId),
		clog.String("session_id", event.SessionId))

	// 处理消息（带重试）
	if err := c.processWithRetry(event); err != nil {
		c.logger.Error("failed to process push event after retries",
			clog.Int("worker_id", workerID),
			clog.Int64("msg_id", event.MsgId),
			clog.Error(err))
		msg.Nak() // 处理失败，Nak 让消息重新入队
		return
	}

	// 处理成功，Ack 确认
	msg.Ack()
	c.logger.Info("push event processed successfully",
		clog.Int("worker_id", workerID),
		clog.Int64("msg_id", event.MsgId))
}

// processWithRetry 带重试的处理逻辑
func (c *Consumer) processWithRetry(event *mqv1.PushEvent) error {
	var lastErr error

	for i := 0; i < c.config.MaxRetry; i++ {
		if i > 0 {
			c.logger.Warn("retrying push event",
				clog.Int64("msg_id", event.MsgId),
				clog.Int("attempt", i+1),
				clog.Int("max_retry", c.config.MaxRetry))
			time.Sleep(c.config.RetryInterval)
		}

		// 调用 Dispatcher 进行写扩散
		if err := c.dispatcher.Dispatch(c.ctx, event); err != nil {
			lastErr = err
			continue
		}

		return nil
	}

	return lastErr
}

// Stop 停止消费者
func (c *Consumer) Stop() error {
	c.logger.Info("stopping consumer")
	c.cancel()

	// 取消订阅
	if err := c.subscriber.Unsubscribe(); err != nil {
		c.logger.Error("failed to unsubscribe", clog.Error(err))
		return err
	}

	c.logger.Info("consumer stopped")
	return nil
}

