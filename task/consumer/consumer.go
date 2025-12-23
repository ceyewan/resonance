package consumer

import (
	"context"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/xerrors"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/task/dispatcher"
	"google.golang.org/protobuf/proto"
)

// Consumer MQ 消费者
type Consumer struct {
	mqClient   mq.Client
	dispatcher *dispatcher.Dispatcher
	config     ConsumerConfig
	logger     clog.Logger

	subscription mq.Subscription
	ctx          context.Context
	cancel       context.CancelFunc
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
	mqClient mq.Client,
	dispatcher *dispatcher.Dispatcher,
	config ConsumerConfig,
	logger clog.Logger,
) *Consumer {
	ctx, cancel := context.WithCancel(context.Background())

	return &Consumer{
		mqClient:   mqClient,
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

	// 使用队列订阅（负载均衡）
	sub, err := c.mqClient.QueueSubscribe(c.ctx, c.config.Topic, c.config.QueueGroup, c.handleMessage)
	if err != nil {
		return xerrors.Wrapf(err, "failed to subscribe to topic %s", c.config.Topic)
	}

	c.subscription = sub
	c.logger.Info("consumer started")
	return nil
}

// handleMessage 处理单条消息
func (c *Consumer) handleMessage(ctx context.Context, msg mq.Message) error {
	c.logger.Debug("received message",
		clog.String("subject", msg.Subject()),
		clog.Int("data_len", len(msg.Data())))

	// 解析 PushEvent
	event := &mqv1.PushEvent{}
	if err := proto.Unmarshal(msg.Data(), event); err != nil {
		c.logger.Error("failed to unmarshal push event", clog.Error(err))
		msg.Ack() // 无法解析的消息直接 Ack，避免重复消费
		return nil
	}

	c.logger.Info("processing push event",
		clog.Int64("msg_id", event.MsgId),
		clog.String("session_id", event.SessionId))

	// 处理消息（带重试）
	if err := c.processWithRetry(event); err != nil {
		c.logger.Error("failed to process push event after retries",
			clog.Int64("msg_id", event.MsgId),
			clog.Error(err))
		msg.Nak() // 处理失败，Nak 让消息重新入队
		return err
	}

	// 处理成功，Ack 确认
	msg.Ack()
	c.logger.Info("push event processed successfully",
		clog.Int64("msg_id", event.MsgId))

	return nil
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
	if c.subscription != nil {
		if err := c.subscription.Unsubscribe(); err != nil {
			c.logger.Error("failed to unsubscribe", clog.Error(err))
		}
	}

	c.logger.Info("consumer stopped")
	return nil
}
