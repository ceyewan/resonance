package consumer

import (
	"context"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/xerrors"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/task/config"
	"google.golang.org/protobuf/proto"
)

// HandlerFunc 消息处理函数
type HandlerFunc func(context.Context, *mqv1.PushEvent) error

// Consumer MQ 消费者
type Consumer struct {
	mqClient mq.Client
	handler  HandlerFunc
	config   config.ConsumerConfig
	logger   clog.Logger

	subscription mq.Subscription
	jobsCh       chan mq.Message // 任务通道
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup // 等待所有 worker 退出
}

// NewConsumer 创建消费者
func NewConsumer(
	mqClient mq.Client,
	handler HandlerFunc,
	config config.ConsumerConfig,
	logger clog.Logger,
) *Consumer {
	ctx, cancel := context.WithCancel(context.Background())

	if config.WorkerCount <= 0 {
		config.WorkerCount = 10 // 默认 10 个 worker
	}

	return &Consumer{
		mqClient: mqClient,
		handler:  handler,
		config:   config,
		logger:   logger,
		jobsCh:   make(chan mq.Message, config.WorkerCount*10),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start 启动消费者
func (c *Consumer) Start() error {
	c.logger.Info("starting consumer",
		clog.String("topic", c.config.Topic),
		clog.String("queue_group", c.config.QueueGroup),
		clog.Int("worker_count", c.config.WorkerCount))

	// 1. 启动 Worker Pool
	for i := 0; i < c.config.WorkerCount; i++ {
		c.wg.Add(1)
		go c.worker(i)
	}

	// 2. 使用队列订阅（负载均衡）
	sub, err := c.mqClient.Subscribe(c.ctx, c.config.Topic, c.receiveMessage, mq.WithQueueGroup(c.config.QueueGroup), mq.WithManualAck())
	if err != nil {
		return xerrors.Wrapf(err, "failed to subscribe to topic %s", c.config.Topic)
	}

	c.subscription = sub
	c.logger.Info("consumer started")
	return nil
}

// receiveMessage 接收消息并放入任务通道
func (c *Consumer) receiveMessage(ctx context.Context, msg mq.Message) error {
	select {
	case c.jobsCh <- msg:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

// worker 工作协程
func (c *Consumer) worker(id int) {
	defer c.wg.Done()
	c.logger.Debug("worker started", clog.Int("worker_id", id))

	for {
		select {
		case msg := <-c.jobsCh:
			c.handleMessage(c.ctx, msg)
		case <-c.ctx.Done():
			// 优雅关闭：处理完 jobsCh 中剩余的消息
			c.drainJobs(id)
			c.logger.Debug("worker stopped", clog.Int("worker_id", id))
			return
		}
	}
}

// drainJobs 处理剩余的任务
func (c *Consumer) drainJobs(workerID int) {
	for {
		select {
		case msg := <-c.jobsCh:
			c.handleMessage(c.ctx, msg)
		default:
			// 队列已空
			return
		}
	}
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

	c.logger.Debug("processing push event",
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
	c.logger.Debug("push event processed successfully",
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
			time.Sleep(time.Duration(c.config.RetryInterval) * time.Second)
		}

		// 调用注入的处理函数
		if err := c.handler(c.ctx, event); err != nil {
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

	// 1. 取消订阅，停止接收新消息
	if c.subscription != nil {
		if err := c.subscription.Unsubscribe(); err != nil {
			c.logger.Error("failed to unsubscribe", clog.Error(err))
		}
	}

	// 2. 关闭任务通道，不再接收新任务
	close(c.jobsCh)

	// 3. 取消 context，触发 worker 优雅退出
	c.cancel()

	// 4. 等待所有 worker 处理完剩余任务
	c.wg.Wait()

	c.logger.Info("consumer stopped")
	return nil
}
