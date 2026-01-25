package consumer

import (
	"context"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/metrics"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/xerrors"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/task/config"
	"github.com/ceyewan/resonance/task/observability"
	"go.opentelemetry.io/otel/attribute"
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
	name     string // 消费者名称（用于指标区分）

	subscription mq.Subscription
	jobsCh       chan mq.Message // 任务通道
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup // 等待所有 worker 退出

	// 指标
	processDuration metrics.Histogram // 处理耗时
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

// SetName 设置消费者名称
func (c *Consumer) SetName(name string) {
	c.name = name
}

// SetProcessDuration 设置处理耗时指标
func (c *Consumer) SetProcessDuration(histogram metrics.Histogram) {
	c.processDuration = histogram
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
// TODO: 实现背压机制
// 当队列满时，当前实现会阻塞在 select 上，可能导致 MQ 的 Subscribe 回调阻塞
// 改进方案：
//  1. 添加队列满的显式处理（返回错误或记录指标）
//  2. 考虑使用可配置的背压策略（如丢弃最旧的消息、拒绝新消息等）
//
// 当前不实现的原因：
//   - NATS Core 模式下没有原生背压支持
//   - 简单的丢弃策略可能导致消息丢失
//   - 需要配合 MQ 客户端的流控机制
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
	start := time.Now()

	// 1. 解析 PushEvent
	event := &mqv1.PushEvent{}
	if err := proto.Unmarshal(msg.Data(), event); err != nil {
		c.logger.Error("failed to unmarshal push event",
			clog.Int("data_len", len(msg.Data())),
			clog.Error(err))

		// TODO: P0 - 消息解析失败时记录到死信队列或数据库
		// 当前行为：直接 Ack，消息永久丢失
		// 等升级到 NATS JetStream 后，可以利用其 Nak + 死信队列功能
		// 短期改进：可以将无法解析的原始消息记录到数据库的 dead_letter 表
		msg.Ack()
		return nil
	}

	// 2. 从 MQ Headers 中提取 Trace Context（如果 Logic 端已经注入）
	// 同时从 PushEvent.trace_headers 中提取（如果 Logic 端使用了 protobuf 传递）
	// 优先使用 PushEvent.trace_headers，因为它是更可靠的传递方式
	ctx = observability.ExtractTraceContext(ctx, event.TraceHeaders)

	// 3. 创建处理 Span（如果 Trace 已启用）
	spanName := "consumer.process"
	if c.name != "" {
		spanName = "consumer." + c.name + ".process"
	}
	ctx, endSpan := observability.StartSpan(ctx, spanName,
		attribute.Int64("msg_id", event.MsgId),
		attribute.String("session_id", event.SessionId),
		attribute.String("from_username", event.FromUsername),
	)
	defer endSpan()

	c.logger.Debug("processing push event",
		clog.Int64("msg_id", event.MsgId),
		clog.String("session_id", event.SessionId))

	// 4. 处理消息（带重试）
	if err := c.processWithRetry(ctx, event); err != nil {
		c.logger.Error("failed to process push event after retries",
			clog.Int64("msg_id", event.MsgId),
			clog.Error(err))

		// 记录失败指标
		c.recordMetrics(ctx, start, "fail", err)

		msg.Nak() // 处理失败，Nak 让消息重新入队
		return err
	}

	// 5. 处理成功，Ack 确认
	msg.Ack()
	c.logger.Debug("push event processed successfully",
		clog.Int64("msg_id", event.MsgId))

	// 记录成功指标
	c.recordMetrics(ctx, start, "success", nil)

	return nil
}

// processWithRetry 带重试的处理逻辑
func (c *Consumer) processWithRetry(ctx context.Context, event *mqv1.PushEvent) error {
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
		if err := c.handler(ctx, event); err != nil {
			lastErr = err
			continue
		}

		return nil
	}

	return lastErr
}

// recordMetrics 记录处理指标
func (c *Consumer) recordMetrics(ctx context.Context, start time.Time, status string, err error) {
	duration := time.Since(start)

	// 使用传入的 histogram 或默认指标
	if c.processDuration != nil {
		labels := []metrics.Label{
			metrics.L("status", status),
			metrics.L("queue_group", c.config.QueueGroup),
		}
		c.processDuration.Record(ctx, duration.Seconds(), labels...)
	}

	// 如果是 storage consumer，使用专门的指标
	if c.name == "storage" {
		observability.RecordStorageProcess(ctx, duration,
			metrics.L("status", status),
			metrics.L("queue_group", c.config.QueueGroup),
		)
	}

	// 如果是 push consumer，使用专门的指标
	if c.name == "push" {
		observability.RecordPushProcess(ctx, duration,
			metrics.L("status", status),
			metrics.L("queue_group", c.config.QueueGroup),
		)
	}
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
